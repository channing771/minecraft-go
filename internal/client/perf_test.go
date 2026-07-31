package client_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"minecraft-go/internal/client"
)

func TestPerfSamplerPercentiles(t *testing.T) {
	s := client.NewPerfSampler(100)
	for i := 1; i <= 100; i++ {
		s.Add(client.FrameSample{FrameMS: float64(i)})
	}
	got := s.Summary(123)
	if got.P50MS != 50 || got.P95MS != 95 || got.P99MS != 99 || got.MaxMS != 100 {
		t.Fatalf("分位数错误: p50=%v p95=%v p99=%v max=%v",
			got.P50MS, got.P95MS, got.P99MS, got.MaxMS)
	}
	if got.PeakRSSBytes != 123 {
		t.Fatalf("峰值 RSS = %d，想要 123", got.PeakRSSBytes)
	}
}

func TestPerfSamplerZeroSamples(t *testing.T) {
	got := client.NewPerfSampler(4).Summary(77)
	if got.Frames != 0 || got.P50MS != 0 || got.P95MS != 0 ||
		got.P99MS != 0 || got.MaxMS != 0 || got.PeakRSSBytes != 77 {
		t.Fatalf("零样本摘要 = %+v", got)
	}
}

func TestPerfSamplerRingKeepsNewestSamples(t *testing.T) {
	s := client.NewPerfSampler(3)
	for i := 1; i <= 5; i++ {
		s.Add(client.FrameSample{FrameMS: float64(i)})
	}
	got := s.Summary(0)
	if got.Frames != 3 || got.P50MS != 4 || got.MaxMS != 5 {
		t.Fatalf("环形缓冲摘要错误: %+v", got)
	}
}

func TestPerfReportJSONRoundTripIncludesTicksAndPersistence(t *testing.T) {
	want := client.PerfReport{
		ScenarioVersion: 3,
		Hardware:        "test-machine",
		SnapshotSeconds: 1.25,
		LoadSeconds:     2.5,
		Ticks: client.PhaseSummary{
			Frames: 100,
			P50MS:  0.5,
			P95MS:  1.5,
			P99MS:  2.5,
			MaxMS:  4,
		},
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got client.PerfReport
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON roundtrip = %+v，想要 %+v", got, want)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(data, &shape); err != nil {
		t.Fatal(err)
	}
	persistence, ok := shape["persistence"]
	if !ok {
		t.Fatal("PerfReport JSON 缺少 persistence 摘要")
	}
	var summary map[string]json.RawMessage
	if err := json.Unmarshal(persistence, &summary); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"snapshots", "p50_ms", "p95_ms", "p99_ms", "max_ms"} {
		if _, ok := summary[field]; !ok {
			t.Fatalf("persistence JSON 缺少字段 %q: %s", field, persistence)
		}
	}
}

func TestPerfReportV5IncludesTransportProtocolAndPlayerPersistence(t *testing.T) {
	data := []byte(`{
  "scenario_version": 5,
  "transport": "tcp",
  "hardware": "test-machine",
  "framebuffer": "2560x1440",
  "phases": {
    "still": {"frames": 6000, "fps": 100, "p99_ms": 9, "peak_rss_bytes": 1024},
    "flying": {"frames": 12000, "fps": 100, "p99_ms": 9, "peak_rss_bytes": 1024}
  },
  "ticks": {"frames": 3600, "p99_ms": 2, "max_ms": 4},
  "persistence": {"snapshots": 64, "p50_ms": 0.1, "p95_ms": 0.2, "p99_ms": 0.3, "max_ms": 0.4},
  "protocol": {"encode_p99_ms": 0.01, "decode_p99_ms": 0.02, "bytes": 19},
  "player_persistence": {"snapshots": 32, "p50_ms": 0.01, "p95_ms": 0.02, "p99_ms": 0.03, "max_ms": 0.04}
}`)
	var report client.PerfReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.ScenarioVersion != 5 || report.Transport != "tcp" {
		t.Fatalf("scenario/transport = %d/%q，想要 5/tcp", report.ScenarioVersion, report.Transport)
	}
	if strings.TrimSpace(report.Hardware) == "" || strings.TrimSpace(report.Framebuffer) == "" {
		t.Fatalf("benchmark 标识不完整: hardware=%q framebuffer=%q",
			report.Hardware, report.Framebuffer)
	}
	for _, name := range []string{"still", "flying"} {
		phase, ok := report.Phases[name]
		if !ok || phase.Frames == 0 || phase.FPS == 0 || phase.P99MS == 0 || phase.PeakRSSBytes == 0 {
			t.Fatalf("%s 阶段指标不完整: %+v, present=%v", name, phase, ok)
		}
	}
	if report.Ticks.Frames == 0 || report.Ticks.P99MS == 0 || report.Ticks.MaxMS == 0 {
		t.Fatalf("tick 指标不完整: %+v", report.Ticks)
	}
	if report.Persistence.Snapshots == 0 || report.Persistence.P50MS == 0 ||
		report.Persistence.P95MS == 0 || report.Persistence.P99MS == 0 ||
		report.Persistence.MaxMS == 0 {
		t.Fatalf("persistence 指标不完整: %+v", report.Persistence)
	}
	if report.Protocol.EncodeP99MS == 0 || report.Protocol.DecodeP99MS == 0 || report.Protocol.Bytes == 0 {
		t.Fatalf("protocol 指标不完整: %+v", report.Protocol)
	}
	if report.PlayerPersistence.Snapshots == 0 || report.PlayerPersistence.P99MS == 0 {
		t.Fatalf("player persistence 指标不完整: %+v", report.PlayerPersistence)
	}
}
