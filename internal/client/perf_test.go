package client_test

import (
	"encoding/json"
	"reflect"
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

func TestPerfReportJSONRoundTripIncludesTicks(t *testing.T) {
	want := client.PerfReport{
		ScenarioVersion: 2,
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
}
