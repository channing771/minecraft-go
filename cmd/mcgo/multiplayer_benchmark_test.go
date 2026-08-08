//go:build darwin

package main

import (
	"strings"
	"testing"
	"time"
)

func TestFormatTickBoundaryOverrunReportsEachSegment(t *testing.T) {
	scheduled := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	signal := benchmarkServerTickSignal{
		measured:  true,
		scheduled: scheduled,
		published: scheduled.Add(30 * time.Millisecond),
		duration:  25 * time.Millisecond,
	}
	now := scheduled.Add(150 * time.Millisecond)
	got := formatTickBoundaryOverrun(signal, now, 3)
	for _, want := range []string{
		"总耗时 150ms",
		"超出 100ms",
		"tick 自身 25ms",
		"调度→发布 30ms",
		"发布→收到 120ms",
		"队列深度 3",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("分解缺少 %q，实际消息：%s", want, got)
		}
	}
}

func TestFormatTickBoundaryOverrunHandlesMissingPublishTime(t *testing.T) {
	scheduled := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	signal := benchmarkServerTickSignal{measured: true, scheduled: scheduled}
	got := formatTickBoundaryOverrun(signal, scheduled.Add(80*time.Millisecond), 0)
	if !strings.Contains(got, "发布时刻缺失") {
		t.Fatalf("发布时刻为零值时未标注，实际消息：%s", got)
	}
	if strings.Contains(got, "发布→收到") {
		t.Fatalf("发布时刻为零值时不应报出无意义的分段，实际消息：%s", got)
	}
}
