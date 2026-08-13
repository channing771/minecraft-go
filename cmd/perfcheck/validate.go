package main

import (
	"fmt"
	"strings"

	"github.com/channing771/mornlea/internal/client"
)

func validateReportProvenance(label string, report client.PerfReport) error {
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
			return fmt.Errorf("%s provenance %s 为空", label, field.name)
		}
	}
	return nil
}

func appendV6AbsoluteFailures(failures []string, report client.PerfReport) []string {
	for _, name := range []string{"still", "flying"} {
		phase, ok := report.Phases[name]
		if !ok {
			failures = append(failures, fmt.Sprintf("当前 v6 报告缺少阶段 %q", name))
			continue
		}
		if phase.FPS < 100 {
			failures = append(failures, fmt.Sprintf("%s fps %.1f < 100", name, phase.FPS))
		}
		if phase.P99MS >= 12 {
			failures = append(failures, fmt.Sprintf("%s p99 %.3f ms >= 12 ms", name, phase.P99MS))
		}
		if phase.PeakRSSBytes >= 2<<30 {
			failures = append(failures, fmt.Sprintf("%s peak RSS %d >= 2GiB", name, phase.PeakRSSBytes))
		}
	}
	if report.Ticks.P99MS >= 10 {
		failures = append(failures, fmt.Sprintf("tick p99 %.3f ms >= 10 ms", report.Ticks.P99MS))
	}
	if report.Ticks.MaxMS >= 50 {
		failures = append(failures, fmt.Sprintf("tick max %.3f ms >= 50 ms", report.Ticks.MaxMS))
	}
	if report.Protocol.EncodeP99MS >= 1 {
		failures = append(failures, fmt.Sprintf(
			"protocol encode p99 %.3f ms >= 1 ms", report.Protocol.EncodeP99MS,
		))
	}
	if report.Protocol.DecodeP99MS >= 1 {
		failures = append(failures, fmt.Sprintf(
			"protocol decode p99 %.3f ms >= 1 ms", report.Protocol.DecodeP99MS,
		))
	}
	if report.PlayerPersistence.P99MS >= 5 {
		failures = append(failures, fmt.Sprintf(
			"player persistence p99 %.3f ms >= 5 ms", report.PlayerPersistence.P99MS,
		))
	}
	if report.PlayerPersistence.MaxMS >= 20 {
		failures = append(failures, fmt.Sprintf(
			"player persistence max %.3f ms >= 20 ms", report.PlayerPersistence.MaxMS,
		))
	}
	multiplayer := report.Multiplayer
	if multiplayer.PeakRSSBytes >= 2<<30 {
		failures = append(failures, fmt.Sprintf("multiplayer peak RSS %d >= 2GiB", multiplayer.PeakRSSBytes))
	}
	if multiplayer.OutboxHighWater > 512 {
		failures = append(failures, fmt.Sprintf("outbox high-water %d > 512", multiplayer.OutboxHighWater))
	}
	if multiplayer.PlayerJobsHighWater > 16 {
		failures = append(failures, fmt.Sprintf("player jobs high-water %d > 16", multiplayer.PlayerJobsHighWater))
	}
	if multiplayer.PlayerDoneHighWater > 2 {
		failures = append(failures, fmt.Sprintf("player done high-water %d > 2", multiplayer.PlayerDoneHighWater))
	}
	return failures
}

func validateV6Report(label string, report client.PerfReport) error {
	if report.LoadSeconds <= 0 {
		return fmt.Errorf("%s v6 load_seconds 必须大于零: %f", label, report.LoadSeconds)
	}
	if report.SnapshotSeconds <= 0 {
		return fmt.Errorf("%s v6 snapshot_seconds 必须大于零: %f", label, report.SnapshotSeconds)
	}
	for _, name := range []string{"still", "flying"} {
		phase, ok := report.Phases[name]
		if !ok {
			return fmt.Errorf("%s v6 缺少 %s 阶段", label, name)
		}
		if phase.Frames <= 0 || phase.FPS <= 0 || phase.P50MS <= 0 || phase.P95MS <= 0 ||
			phase.P99MS <= 0 || phase.MaxMS <= 0 || phase.PeakRSSBytes == 0 {
			return fmt.Errorf("%s v6 %s 阶段指标不完整: %+v", label, name, phase)
		}
		if phase.P50MS > phase.P95MS || phase.P95MS > phase.P99MS || phase.P99MS > phase.MaxMS {
			return fmt.Errorf("%s v6 %s 阶段分位数非单调: %+v", label, name, phase)
		}
		if phase.DroppedRingBufferSamples > 0 {
			return fmt.Errorf("%s v6 %s dropped ring-buffer samples=%d", label, name, phase.DroppedRingBufferSamples)
		}
	}
	if len(report.Phases) != 2 {
		return fmt.Errorf("%s v6 phases 必须精确包含 still/flying: %v", label, report.Phases)
	}
	if report.Ticks.Frames != 200 {
		return fmt.Errorf("%s v6 ticks frames 必须为 200: %d", label, report.Ticks.Frames)
	}
	if report.Ticks.FPS != 0 {
		return fmt.Errorf("%s v6 ticks fps 必须为零: %f", label, report.Ticks.FPS)
	}
	if report.Ticks.P50MS <= 0 || report.Ticks.P95MS <= 0 || report.Ticks.P99MS <= 0 ||
		report.Ticks.MaxMS <= 0 {
		return fmt.Errorf("%s v6 ticks 指标不完整: %+v", label, report.Ticks)
	}
	if report.Ticks.P50MS > report.Ticks.P95MS || report.Ticks.P95MS > report.Ticks.P99MS ||
		report.Ticks.P99MS > report.Ticks.MaxMS {
		return fmt.Errorf("%s v6 ticks 分位数非单调: %+v", label, report.Ticks)
	}
	if report.Ticks.DroppedRingBufferSamples > 0 {
		return fmt.Errorf("%s v6 ticks dropped ring-buffer samples=%d", label, report.Ticks.DroppedRingBufferSamples)
	}
	persistence := report.Persistence
	if persistence.Snapshots <= 0 || persistence.P50MS <= 0 || persistence.P95MS <= 0 ||
		persistence.P99MS <= 0 || persistence.MaxMS <= 0 {
		return fmt.Errorf("%s v6 persistence 指标不完整: %+v", label, persistence)
	}
	if persistence.P50MS > persistence.P95MS || persistence.P95MS > persistence.P99MS ||
		persistence.P99MS > persistence.MaxMS {
		return fmt.Errorf("%s v6 persistence 分位数非单调: %+v", label, persistence)
	}
	if report.Multiplayer.InterestDiff.Samples != 1600 {
		return fmt.Errorf(
			"%s v6 interest_diff samples 必须为 1600: %d",
			label,
			report.Multiplayer.InterestDiff.Samples,
		)
	}
	// v8–v11 逐次计时取 2048；v12 起改为批量分摊，样本数相应减少。
	remoteGPUCompletionSamples := 256
	switch {
	case report.ScenarioVersion >= 12:
		remoteGPUCompletionSamples = client.ScenarioV12GPUCompletionSamples
	case report.ScenarioVersion >= 8:
		remoteGPUCompletionSamples = client.ScenarioV8GPUCompletionSamples
	}
	latencies := []struct {
		name       string
		summary    client.LatencySummary
		minSamples int
	}{
		{name: "remote_state_encode", summary: report.Multiplayer.RemoteStateEncode, minSamples: 256},
		{name: "remote_state_decode", summary: report.Multiplayer.RemoteStateDecode, minSamples: 256},
		{name: "interest_diff", summary: report.Multiplayer.InterestDiff, minSamples: 1600},
		{name: "roster_apply", summary: report.Multiplayer.RosterApply, minSamples: 256},
		{name: "interpolation", summary: report.Multiplayer.Interpolation, minSamples: 256},
		{name: "avatar_submit", summary: report.Multiplayer.AvatarSubmit, minSamples: 256},
		{name: "name_tag_submit", summary: report.Multiplayer.NameTagSubmit, minSamples: 256},
		{name: "remote_gpu_complete", summary: report.Multiplayer.RemoteGPUComplete, minSamples: remoteGPUCompletionSamples},
	}
	for _, latency := range latencies {
		value := latency.summary
		if value.Samples < latency.minSamples || value.P50MS <= 0 || value.P95MS <= 0 ||
			value.P99MS <= 0 || value.MaxMS <= 0 {
			return fmt.Errorf("%s v6 %s 指标不完整或样本过低: %+v", label, latency.name, value)
		}
		if value.P50MS > value.P95MS || value.P95MS > value.P99MS || value.P99MS > value.MaxMS {
			return fmt.Errorf("%s v6 %s 分位数非单调: %+v", label, latency.name, value)
		}
	}
	multiplayer := report.Multiplayer
	if multiplayer.ServerOutboundBytes == 0 || multiplayer.OutboxHighWater < 0 ||
		multiplayer.PlayerJobsHighWater < 0 || multiplayer.PlayerDoneHighWater < 0 ||
		multiplayer.PeakRSSBytes == 0 {
		return fmt.Errorf("%s v6 multiplayer 标量指标不完整: %+v", label, multiplayer)
	}
	return nil
}

func appendV6MultiplayerRegressions(
	failures []string,
	baseline client.MultiplayerSummary,
	current client.MultiplayerSummary,
	threshold float64,
	includeServerProbe bool,
) []string {
	latencies := []struct {
		name              string
		baseline, current client.LatencySummary
		floors            regressionFloors
	}{
		{name: "remote_state_encode", baseline: baseline.RemoteStateEncode, current: current.RemoteStateEncode, floors: uniformFloors(latencyNoiseFloorMS)},
		{name: "remote_state_decode", baseline: baseline.RemoteStateDecode, current: current.RemoteStateDecode, floors: uniformFloors(latencyNoiseFloorMS)},
		{name: "roster_apply", baseline: baseline.RosterApply, current: current.RosterApply, floors: uniformFloors(latencyNoiseFloorMS)},
		{name: "interpolation", baseline: baseline.Interpolation, current: current.Interpolation, floors: uniformFloors(latencyNoiseFloorMS)},
		{name: "avatar_submit", baseline: baseline.AvatarSubmit, current: current.AvatarSubmit, floors: uniformFloors(latencyNoiseFloorMS)},
		{name: "name_tag_submit", baseline: baseline.NameTagSubmit, current: current.NameTagSubmit, floors: uniformFloors(latencyNoiseFloorMS)},
		{
			name:     "remote_gpu_complete",
			baseline: baseline.RemoteGPUComplete,
			current:  current.RemoteGPUComplete,
			floors:   uniformFloors(gpuCompletionResolutionMS(baseline.RemoteGPUCompleteBatch)),
		},
	}
	for _, latency := range latencies {
		failures = appendStableSummaryRegressions(
			failures, latency.name,
			latency.baseline.P50MS, latency.baseline.P95MS, latency.baseline.P99MS,
			latency.current.P50MS, latency.current.P95MS, latency.current.P99MS,
			threshold, latency.floors,
		)
	}
	if includeServerProbe {
		failures = appendRegression(
			failures, "multiplayer", "server_outbound_bytes",
			float64(baseline.ServerOutboundBytes), float64(current.ServerOutboundBytes), threshold,
		)
		failures = appendRegression(
			failures, "multiplayer", "peak_rss_bytes",
			float64(baseline.PeakRSSBytes), float64(current.PeakRSSBytes), threshold,
		)
	}
	return failures
}

func validateV5Report(label string, report client.PerfReport) error {
	if report.Transport != "memory" && report.Transport != "tcp" {
		return fmt.Errorf("%s v5 transport 无效: %q", label, report.Transport)
	}
	if report.Protocol.EncodeP99MS <= 0 || report.Protocol.DecodeP99MS <= 0 || report.Protocol.Bytes == 0 {
		return fmt.Errorf("%s v5 protocol 指标不完整: %+v", label, report.Protocol)
	}
	player := report.PlayerPersistence
	if player.Snapshots <= 0 || player.P50MS <= 0 || player.P95MS <= 0 ||
		player.P99MS <= 0 || player.MaxMS <= 0 {
		return fmt.Errorf("%s v5 player_persistence 指标不完整: %+v", label, player)
	}
	if player.P50MS > player.P95MS || player.P95MS > player.P99MS || player.P99MS > player.MaxMS {
		return fmt.Errorf("%s v5 player_persistence 分位数非单调: %+v", label, player)
	}
	return nil
}
