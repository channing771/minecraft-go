//go:build darwin

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"minecraft-go/internal/client"
)

func writeBenchmarkReport(outputPath string, report client.PerfReport) error {
	return writeBenchmarkReportWithFS(outputPath, report, defaultBenchmarkReportFS())
}

type benchmarkReportFile interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
	Chmod(os.FileMode) error
	Name() string
}

type benchmarkReportDirectory interface {
	Sync() error
	Close() error
}

type benchmarkReportFS struct {
	createTemp    func(string, string) (benchmarkReportFile, error)
	rename        func(string, string) error
	remove        func(string) error
	readFile      func(string) ([]byte, error)
	openDirectory func(string) (benchmarkReportDirectory, error)
}

func defaultBenchmarkReportFS() benchmarkReportFS {
	return benchmarkReportFS{
		createTemp: func(directory, pattern string) (benchmarkReportFile, error) {
			return os.CreateTemp(directory, pattern)
		},
		rename:   os.Rename,
		remove:   os.Remove,
		readFile: os.ReadFile,
		openDirectory: func(path string) (benchmarkReportDirectory, error) {
			return os.Open(path)
		},
	}
}

func writeBenchmarkReportWithFS(
	outputPath string,
	report client.PerfReport,
	fs benchmarkReportFS,
) error {
	if err := validateBenchmarkReport(report); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("编码性能报告: %w", err)
	}
	data = append(data, '\n')
	oldData, readErr := fs.readFile(outputPath)
	hadOld := readErr == nil
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("读取现有性能报告: %w", readErr)
	}
	if err := writeSyncedBenchmarkTemp(outputPath, data, fs); err != nil {
		return err
	}
	if err := syncBenchmarkReportDirectory(filepath.Dir(outputPath), fs); err != nil {
		rollbackErr := rollbackBenchmarkReport(outputPath, oldData, hadOld, fs)
		return errors.Join(fmt.Errorf("同步性能报告目录: %w", err), rollbackErr)
	}
	return nil
}

func writeSyncedBenchmarkTemp(outputPath string, data []byte, fs benchmarkReportFS) (returnErr error) {
	directory := filepath.Dir(outputPath)
	pattern := "." + filepath.Base(outputPath) + ".tmp-*"
	file, err := fs.createTemp(directory, pattern)
	if err != nil {
		return fmt.Errorf("创建性能报告临时文件: %w", err)
	}
	tempPath := file.Name()
	closed := false
	promoted := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, file.Close())
		}
		if !promoted {
			if removeErr := fs.remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				returnErr = errors.Join(returnErr, removeErr)
			}
		}
	}()
	if err := file.Chmod(0o644); err != nil {
		return fmt.Errorf("设置性能报告临时文件权限: %w", err)
	}
	if err := writeBenchmarkReportBytes(file, data); err != nil {
		return fmt.Errorf("写性能报告临时文件: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步性能报告临时文件: %w", err)
	}
	if err := file.Close(); err != nil {
		closed = true
		return fmt.Errorf("关闭性能报告临时文件: %w", err)
	}
	closed = true
	if err := fs.rename(tempPath, outputPath); err != nil {
		return fmt.Errorf("替换性能报告: %w", err)
	}
	promoted = true
	return nil
}

func writeBenchmarkReportBytes(file io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func syncBenchmarkReportDirectory(path string, fs benchmarkReportFS) error {
	directory, err := fs.openDirectory(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func rollbackBenchmarkReport(
	outputPath string,
	oldData []byte,
	hadOld bool,
	fs benchmarkReportFS,
) error {
	var rollbackErr error
	if hadOld {
		rollbackErr = writeSyncedBenchmarkTemp(outputPath, oldData, fs)
	} else if err := fs.remove(outputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		rollbackErr = err
	}
	return errors.Join(rollbackErr, syncBenchmarkReportDirectory(filepath.Dir(outputPath), fs))
}

func validateBenchmarkReport(report client.PerfReport) error {
	var failures []string
	if report.Persistence.Snapshots <= 0 {
		failures = append(failures, "persistence snapshots=0")
	}
	if report.Transport != "memory" && report.Transport != "tcp" {
		failures = append(failures, fmt.Sprintf("transport=%q", report.Transport))
	}
	if report.Protocol.Bytes == 0 || report.Protocol.EncodeP99MS <= 0 || report.Protocol.DecodeP99MS <= 0 {
		failures = append(failures, fmt.Sprintf("protocol 指标不完整: %+v", report.Protocol))
	}
	if report.PlayerPersistence.Snapshots <= 0 {
		failures = append(failures, "player_persistence snapshots=0")
	}
	if report.ScenarioVersion >= 6 {
		for _, field := range []struct {
			name, value string
		}{
			{name: "hardware", value: report.Hardware},
			{name: "os", value: report.OS},
			{name: "go_version", value: report.GoVersion},
			{name: "git_commit", value: report.GitCommit},
			{name: "framebuffer", value: report.Framebuffer},
		} {
			if strings.TrimSpace(field.value) == "" {
				failures = append(failures, field.name+" 为空")
			}
		}
		for _, name := range []string{"still", "flying"} {
			phase, ok := report.Phases[name]
			if !ok || phase.Frames <= 0 || phase.FPS <= 0 || phase.P50MS <= 0 || phase.P95MS <= 0 ||
				phase.P99MS <= 0 || phase.MaxMS <= 0 || phase.PeakRSSBytes == 0 {
				failures = append(failures, name+" 阶段指标不完整")
				continue
			}
			if phase.DroppedRingBufferSamples > 0 {
				failures = append(failures, name+" dropped ring-buffer samples")
			}
		}
		if len(report.Phases) != 2 {
			failures = append(failures, "phases 必须精确包含 still/flying")
		}
		if report.Ticks.Frames != 200 || report.Ticks.P50MS <= 0 || report.Ticks.P95MS <= 0 ||
			report.Ticks.P99MS <= 0 || report.Ticks.MaxMS <= 0 || report.Ticks.DroppedRingBufferSamples > 0 {
			failures = append(failures, "ticks 指标不完整或 dropped ring-buffer samples")
		}
		for name, summary := range map[string]client.LatencySummary{
			"remote_state_encode": report.Multiplayer.RemoteStateEncode,
			"remote_state_decode": report.Multiplayer.RemoteStateDecode,
			"interest_diff":       report.Multiplayer.InterestDiff,
			"roster_apply":        report.Multiplayer.RosterApply,
			"interpolation":       report.Multiplayer.Interpolation,
			"avatar_submit":       report.Multiplayer.AvatarSubmit,
			"name_tag_submit":     report.Multiplayer.NameTagSubmit,
			"remote_gpu_complete": report.Multiplayer.RemoteGPUComplete,
		} {
			minimum := 256
			if name == "interest_diff" {
				minimum = 1000
			}
			if name == "remote_gpu_complete" {
				minimum = gpuCompletionMinSamples(report.ScenarioVersion)
			}
			if summary.Samples < minimum || summary.P50MS <= 0 || summary.P95MS <= 0 ||
				summary.P99MS <= 0 || summary.MaxMS <= 0 {
				failures = append(failures, fmt.Sprintf("%s 指标不完整或 samples < %d: %+v", name, minimum, summary))
			}
		}
		multiplayer := report.Multiplayer
		if multiplayer.ServerOutboundBytes == 0 {
			failures = append(failures, "server_outbound_bytes=0")
		}
		if multiplayer.PeakRSSBytes == 0 {
			failures = append(failures, "multiplayer 峰值 RSS=0")
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	return nil
}

func benchmarkPerformanceRecords(report client.PerfReport) []string {
	var records []string
	if report.Protocol.EncodeP99MS >= 1 || report.Protocol.DecodeP99MS >= 1 {
		records = append(records, fmt.Sprintf("protocol p99 超过 1ms: %+v", report.Protocol))
	}
	if report.PlayerPersistence.P99MS >= 5 || report.PlayerPersistence.MaxMS >= 20 {
		records = append(records, fmt.Sprintf("player_persistence 超过 p99/max 5/20ms: %+v", report.PlayerPersistence))
	}
	if report.ScenarioVersion >= 6 {
		multiplayer := report.Multiplayer
		if multiplayer.OutboxHighWater > 512 {
			records = append(records, fmt.Sprintf("outbox high-water %d > 512", multiplayer.OutboxHighWater))
		}
		if multiplayer.PlayerJobsHighWater > 16 {
			records = append(records, fmt.Sprintf("player jobs high-water %d > 16", multiplayer.PlayerJobsHighWater))
		}
		if multiplayer.PlayerDoneHighWater > 2 {
			records = append(records, fmt.Sprintf("player done high-water %d > 2", multiplayer.PlayerDoneHighWater))
		}
		if multiplayer.PeakRSSBytes >= 2<<30 {
			records = append(records, fmt.Sprintf("multiplayer peak RSS %d >= 2GiB", multiplayer.PeakRSSBytes))
		}
	}
	for name, phase := range report.Phases {
		if phase.FPS < 100 {
			records = append(records, fmt.Sprintf("%s fps %.1f < 100", name, phase.FPS))
		}
		if phase.P99MS >= 12 {
			records = append(records, fmt.Sprintf("%s p99 %.3f ms >= 12 ms", name, phase.P99MS))
		}
		if phase.PeakRSSBytes >= 2<<30 {
			records = append(records, fmt.Sprintf("%s peak RSS %d >= 2GiB", name, phase.PeakRSSBytes))
		}
	}
	if report.Ticks.P99MS >= 10 {
		records = append(records, fmt.Sprintf("tick p99 %.3f ms >= 10 ms", report.Ticks.P99MS))
	}
	if report.Ticks.MaxMS >= 50 {
		records = append(records, fmt.Sprintf("tick max %.3f ms >= 50 ms", report.Ticks.MaxMS))
	}
	return records
}
