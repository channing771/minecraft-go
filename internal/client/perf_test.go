package client_test

import (
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
