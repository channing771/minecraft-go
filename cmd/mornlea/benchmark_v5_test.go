//go:build darwin

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/client"
)

func TestBenchmarkScenarioVersionIncludesStaticBlockLightWorkload(t *testing.T) {
	if scenarioVersion != 15 {
		t.Fatalf("scenarioVersion=%d，想要静态方块光 workload 之后的 v15", scenarioVersion)
	}
}

func TestValidateBenchmarkReportRequiresPersistenceSamples(t *testing.T) {
	report := client.PerfReport{
		Transport: "memory",
		Phases: map[string]client.PhaseSummary{
			"still":  {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
			"flying": {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
		},
		Ticks:             client.PhaseSummary{P99MS: 9, MaxMS: 49},
		Protocol:          client.ProtocolSummary{EncodeP99MS: 0.01, DecodeP99MS: 0.01, Bytes: 19},
		PlayerPersistence: client.PersistenceSummary{Snapshots: 1, P99MS: 0.1, MaxMS: 0.1},
	}
	err := validateBenchmarkReport(report)
	if err == nil || !strings.Contains(err.Error(), "persistence snapshots=0") {
		t.Fatalf("zero persistence validation error=%v", err)
	}
	report.Persistence.Snapshots = 1
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("one persistence sample unexpectedly rejected: %v", err)
	}
}

func TestValidateBenchmarkReportTickP99UserOverrideBoundary(t *testing.T) {
	report := client.PerfReport{
		Transport: "memory",
		Phases: map[string]client.PhaseSummary{
			"still":  {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
			"flying": {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
		},
		Ticks:             client.PhaseSummary{P99MS: 9.999, MaxMS: 49},
		Persistence:       client.PersistenceSummary{Snapshots: 1},
		Protocol:          client.ProtocolSummary{EncodeP99MS: 0.01, DecodeP99MS: 0.01, Bytes: 19},
		PlayerPersistence: client.PersistenceSummary{Snapshots: 1, P99MS: 0.1, MaxMS: 0.1},
	}
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("v7 10ms tick p99 gate rejected 9.999ms: %v", err)
	}

	report.Ticks.P99MS = 10
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("10ms tick p99 should only be recorded: %v", err)
	}
}

func TestValidateBenchmarkReportRejectsNonPositivePersistenceSnapshots(t *testing.T) {
	for _, field := range []string{"world", "player"} {
		for _, snapshots := range []int64{0, -1} {
			t.Run(fmt.Sprintf("%s/%d", field, snapshots), func(t *testing.T) {
				report := validBenchmarkReport()
				if field == "world" {
					report.Persistence.Snapshots = snapshots
				} else {
					report.PlayerPersistence.Snapshots = snapshots
				}
				if err := validateBenchmarkReport(report); err == nil {
					t.Fatalf("%s snapshots=%d unexpectedly accepted", field, snapshots)
				}
			})
		}
	}
	if err := validateBenchmarkReport(validBenchmarkReport()); err != nil {
		t.Fatalf("positive persistence snapshots rejected: %v", err)
	}
}

func TestWriteBenchmarkReportDoesNotOverwriteAcceptedOutputOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accepted.json")
	const accepted = "accepted baseline\n"
	if err := os.WriteFile(path, []byte(accepted), 0o644); err != nil {
		t.Fatal(err)
	}
	report := client.PerfReport{
		Transport: "memory",
		Phases: map[string]client.PhaseSummary{
			"still":  {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
			"flying": {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
		},
		Ticks:             client.PhaseSummary{P99MS: 9, MaxMS: 49},
		Persistence:       client.PersistenceSummary{Snapshots: 1},
		Protocol:          client.ProtocolSummary{EncodeP99MS: 0.01, DecodeP99MS: 0.01, Bytes: 0},
		PlayerPersistence: client.PersistenceSummary{Snapshots: 1, P99MS: 0.1, MaxMS: 0.1},
	}
	if err := writeBenchmarkReport(path, report); err == nil {
		t.Fatal("invalid report unexpectedly written")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != accepted {
		t.Fatalf("failed report replaced accepted output: %q", got)
	}
}

func TestWriteBenchmarkReportAtomicallyPreservesExistingOutputOnIOFailure(t *testing.T) {
	testErr := errors.New("injected report I/O failure")
	cases := []struct {
		name   string
		mutate func(*benchmarkReportFS)
	}{
		{
			name: "temp write",
			mutate: func(fs *benchmarkReportFS) {
				createTemp := fs.createTemp
				fs.createTemp = func(directory, pattern string) (benchmarkReportFile, error) {
					file, err := createTemp(directory, pattern)
					return &faultBenchmarkReportFile{benchmarkReportFile: file, writeErr: testErr}, err
				}
			},
		},
		{
			name: "temp sync",
			mutate: func(fs *benchmarkReportFS) {
				createTemp := fs.createTemp
				fs.createTemp = func(directory, pattern string) (benchmarkReportFile, error) {
					file, err := createTemp(directory, pattern)
					return &faultBenchmarkReportFile{benchmarkReportFile: file, syncErr: testErr}, err
				}
			},
		},
		{
			name: "rename",
			mutate: func(fs *benchmarkReportFS) {
				fs.rename = func(string, string) error { return testErr }
			},
		},
		{
			name: "directory sync",
			mutate: func(fs *benchmarkReportFS) {
				openDirectory := fs.openDirectory
				fs.openDirectory = func(path string) (benchmarkReportDirectory, error) {
					directory, err := openDirectory(path)
					return &faultBenchmarkReportDirectory{
						benchmarkReportDirectory: directory,
						syncErr:                  testErr,
					}, err
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "accepted.json")
			const accepted = "accepted baseline\n"
			if err := os.WriteFile(path, []byte(accepted), 0o644); err != nil {
				t.Fatal(err)
			}
			fs := defaultBenchmarkReportFS()
			test.mutate(&fs)
			if err := writeBenchmarkReportWithFS(path, validBenchmarkReport(), fs); err == nil {
				t.Fatal("injected I/O failure unexpectedly succeeded")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != accepted {
				t.Fatalf("I/O failure replaced accepted output: %q", got)
			}
		})
	}
}

func TestWriteBenchmarkReportAtomicPromotionIsComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeBenchmarkReport(path, validBenchmarkReport()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' || !strings.Contains(string(data), `"scenario_version": 5`) {
		t.Fatalf("promoted report is incomplete: %q", data)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".report.json.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("successful promotion left temporary files: %v", matches)
	}
}

type faultBenchmarkReportFile struct {
	benchmarkReportFile
	writeErr error
	syncErr  error
}

func (file *faultBenchmarkReportFile) Write(data []byte) (int, error) {
	if file.writeErr != nil {
		return 0, file.writeErr
	}
	return file.benchmarkReportFile.Write(data)
}

func (file *faultBenchmarkReportFile) Sync() error {
	if file.syncErr != nil {
		return file.syncErr
	}
	return file.benchmarkReportFile.Sync()
}

type faultBenchmarkReportDirectory struct {
	benchmarkReportDirectory
	syncErr error
}

func (directory *faultBenchmarkReportDirectory) Sync() error {
	if directory.syncErr != nil {
		return directory.syncErr
	}
	return directory.benchmarkReportDirectory.Sync()
}

func validBenchmarkReport() client.PerfReport {
	return client.PerfReport{
		ScenarioVersion: 5,
		Transport:       "memory",
		Hardware:        "test-hardware",
		OS:              "test-os",
		GoVersion:       "test-go",
		GitCommit:       "test-commit",
		Framebuffer:     "2560x1440",
		Phases: map[string]client.PhaseSummary{
			"still":  {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
			"flying": {FPS: 100, P99MS: 11, PeakRSSBytes: 1},
		},
		Ticks:             client.PhaseSummary{P99MS: 9, MaxMS: 49},
		Persistence:       client.PersistenceSummary{Snapshots: 1},
		Protocol:          client.ProtocolSummary{EncodeP99MS: 0.01, DecodeP99MS: 0.01, Bytes: 19},
		PlayerPersistence: client.PersistenceSummary{Snapshots: 1, P50MS: 0.01, P95MS: 0.01, P99MS: 0.1, MaxMS: 0.1},
	}
}

func TestSaveRecorderPercentilesAndReset(t *testing.T) {
	recorder := newSaveRecorder(100)
	for millisecond := 1; millisecond <= 100; millisecond++ {
		recorder.add(time.Duration(millisecond) * time.Millisecond)
	}
	got := recorder.summary()
	if got.Snapshots != 100 || got.P50MS != 50 || got.P95MS != 95 ||
		got.P99MS != 99 || got.MaxMS != 100 {
		t.Fatalf("save summary=%+v，想要 100 samples 与 50/95/99/100ms", got)
	}

	recorder.reset()
	if got := recorder.summary(); got.Snapshots != 0 || got.P50MS != 0 ||
		got.P95MS != 0 || got.P99MS != 0 || got.MaxMS != 0 {
		t.Fatalf("reset 后 save summary=%+v，想要全零", got)
	}
}

func TestSaveRecorderIsConcurrencySafeAndBounded(t *testing.T) {
	const workers = 8
	const samplesPerWorker = 1000
	recorder := newSaveRecorder(workers * samplesPerWorker)
	var group sync.WaitGroup
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for sample := range samplesPerWorker {
				recorder.add(time.Duration(worker+sample+1) * time.Microsecond)
			}
		}()
	}
	group.Wait()
	got := recorder.summary()
	if got.Snapshots != workers*samplesPerWorker {
		t.Fatalf("concurrent snapshots=%d，想要 %d", got.Snapshots, workers*samplesPerWorker)
	}
	if got.P50MS <= 0 || got.P95MS < got.P50MS || got.P99MS < got.P95MS ||
		got.MaxMS < got.P99MS {
		t.Fatalf("concurrent percentiles 非单调: %+v", got)
	}

	bounded := newSaveRecorder(2)
	bounded.add(time.Millisecond)
	bounded.add(2 * time.Millisecond)
	bounded.add(3 * time.Millisecond)
	if got := bounded.summary(); got.Snapshots != 2 || got.P50MS != 2 || got.MaxMS != 3 {
		t.Fatalf("bounded recorder=%+v，想要仅保留最新 2 个样本", got)
	}
}
