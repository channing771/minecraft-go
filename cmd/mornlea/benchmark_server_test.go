//go:build darwin

package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
)

func TestBenchmarkServerMeasuredWindowSendsOneSequencePerCompletedTick(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sent := make(chan uint64, 1)
	statsObserved := make(chan struct{}, 1)
	var sequences []uint64
	var statsCalls, rssCalls int
	sendInputs := func(_ context.Context, sequence uint64) error {
		sequences = append(sequences, sequence)
		sent <- sequence
		return nil
	}
	if err := epoch.beginMeasurement(testCtx, runBenchmarkTestInputBoundary, func() error {
		return sendInputs(testCtx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		for range benchmarkServerMeasuredTicks {
			select {
			case <-sent:
				epoch.observeTick(time.Millisecond)
				select {
				case <-statsObserved:
				case <-testCtx.Done():
					return
				}
				select {
				case <-time.After(2 * time.Millisecond):
				case <-testCtx.Done():
					return
				}
			case <-testCtx.Done():
				return
			}
		}
	}()
	summary, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		runBenchmarkTestInputBoundary,
		sendInputs,
		func() server.HostStats {
			statsCalls++
			statsObserved <- struct{}{}
			return server.HostStats{
				ActivePlayers: 8, MaxSessionOutboxDepth: 5,
				PlayerSaveJobDepth: 6, PlayerSaveDoneDepth: 1,
			}
		},
		func() (uint64, error) {
			rssCalls++
			return 123, nil
		},
	)
	<-publisherDone
	if err != nil {
		t.Fatal(err)
	}
	if len(sequences) != benchmarkServerMeasuredTicks {
		t.Fatalf("input sequences=%d want=%d", len(sequences), benchmarkServerMeasuredTicks)
	}
	for index, sequence := range sequences {
		if want := uint64(index + 1); sequence != want {
			t.Fatalf("sequence[%d]=%d want=%d", index, sequence, want)
		}
	}
	if statsCalls != benchmarkServerMeasuredTicks || rssCalls != 10 {
		t.Fatalf("stats/rss calls=%d/%d want=%d/10", statsCalls, rssCalls, benchmarkServerMeasuredTicks)
	}
	if summary != (benchmarkServerWindowSummary{
		outboxHigh: 5, jobsHigh: 6, doneHigh: 1, peakRSS: 123,
	}) {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestBenchmarkServerMeasuredWindowRejectsTickAdvanceBeforeStatsBoundary(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sent := make(chan uint64, 1)
	sendInputs := func(_ context.Context, sequence uint64) error {
		sent <- sequence
		return nil
	}
	if err := epoch.beginMeasurement(testCtx, runBenchmarkTestInputBoundary, func() error {
		return sendInputs(testCtx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	publisherDone := make(chan struct{})
	go func() {
		defer close(publisherDone)
		select {
		case <-sent:
			epoch.observeTick(time.Millisecond)
			epoch.observeTick(time.Millisecond)
		case <-testCtx.Done():
		}
	}()
	releaseStats := make(chan struct{})
	var statsCalls atomic.Int64
	controllerDone := make(chan error, 1)
	go func() {
		_, err := runBenchmarkServerMeasuredWindow(
			testCtx,
			epoch,
			8,
			runBenchmarkTestInputBoundary,
			sendInputs,
			func() server.HostStats {
				if statsCalls.Add(1) == 1 {
					<-releaseStats
				}
				return server.HostStats{ActivePlayers: 8}
			},
			func() (uint64, error) { return 1, nil },
		)
		controllerDone <- err
	}()
	var controllerErr error
	select {
	case controllerErr = <-controllerDone:
	case <-time.After(100 * time.Millisecond):
		close(releaseStats)
		controllerErr = <-controllerDone
	}
	<-publisherDone
	if controllerErr == nil || !strings.Contains(controllerErr.Error(), "Stats") {
		t.Fatalf("tick advanced before Stats boundary error=%v", controllerErr)
	}
	if got := statsCalls.Load(); got != 1 {
		t.Fatalf("stats calls=%d want=1 fail-fast sample", got)
	}
}

func TestBenchmarkServerMeasuredWindowArmsInputsInsideStepBoundary(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	insideBoundary := false
	inputBoundary := func(_ context.Context, _ uint64, action func() error) error {
		insideBoundary = true
		defer func() { insideBoundary = false }()
		return action()
	}
	stopAfterAssertion := errors.New("stop after boundary assertion")
	sendInputs := func(_ context.Context, sequence uint64) error {
		if !insideBoundary {
			return errors.New("input sent outside step boundary")
		}
		if sequence == 2 {
			return stopAfterAssertion
		}
		return nil
	}
	if err := epoch.beginMeasurement(testCtx, inputBoundary, func() error {
		return sendInputs(testCtx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	epoch.observeTick(time.Millisecond)
	_, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		inputBoundary,
		sendInputs,
		func() server.HostStats { return server.HostStats{ActivePlayers: 8} },
		func() (uint64, error) { return 1, nil },
	)
	if !errors.Is(err, stopAfterAssertion) {
		t.Fatalf("input boundary error=%v, want assertion stop", err)
	}
}

func TestBenchmarkServerMeasuredWindowRejectsInputBoundaryPastTickDeadline(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := epoch.beginMeasurement(testCtx, runBenchmarkTestInputBoundary, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	epoch.observeTick(fixedBenchmarkFrameDuration - 20*time.Millisecond)
	blockingBoundary := func(ctx context.Context, _ uint64, _ func() error) error {
		<-ctx.Done()
		return ctx.Err()
	}
	_, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		blockingBoundary,
		func(context.Context, uint64) error { return nil },
		func() server.HostStats { return server.HostStats{ActivePlayers: 8} },
		func() (uint64, error) { return 1, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "50ms") {
		t.Fatalf("input boundary deadline error=%v", err)
	}
}

func TestBenchmarkServerInputDeadlineUsesScheduledTickTime(t *testing.T) {
	scheduled := time.Now().Add(100 * time.Millisecond)
	deadline, err := benchmarkServerInputDeadline(benchmarkServerTickSignal{
		scheduled: scheduled,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := scheduled.Add(fixedBenchmarkFrameDuration); !deadline.Equal(want) {
		t.Fatalf("input deadline=%s want scheduled deadline=%s", deadline, want)
	}
}

func TestBenchmarkServerInputDeadlineRejectsDelayedStepStart(t *testing.T) {
	_, err := benchmarkServerInputDeadline(benchmarkServerTickSignal{
		scheduled: time.Now().Add(-fixedBenchmarkFrameDuration),
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "50ms") {
		t.Fatalf("delayed step deadline error=%v", err)
	}
}

func TestCanonicalCountingServerStreamFreezesMeasurementAtSendStart(t *testing.T) {
	for _, test := range []struct {
		name                       string
		measuringAtStart, atFinish bool
		wantCount                  bool
	}{
		{name: "measured send finishes after close", measuringAtStart: true, wantCount: true},
		{name: "warm-up send finishes after open", atFinish: true, wantCount: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			epoch := newBenchmarkServerEpoch()
			if test.measuringAtStart {
				epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
			}
			inner := &benchmarkBlockingServerStream{
				entered: make(chan struct{}, 1),
				release: make(chan struct{}),
			}
			codec, err := network.NewCodec()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = codec.Close() })
			var counted atomic.Uint64
			stream := &canonicalCountingServerStream{
				inner: inner, codec: codec, bytes: &counted, epoch: epoch,
			}
			done := make(chan error, 1)
			go func() {
				done <- stream.Send(
					context.Background(), network.StatePlay,
					network.PlayerState{ServerTick: 1},
				)
			}()
			<-inner.entered
			if test.atFinish {
				epoch.phase.Store(uint32(benchmarkServerEpochMeasuring))
			} else {
				epoch.phase.Store(uint32(benchmarkServerEpochDone))
			}
			close(inner.release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if got := counted.Load() > 0; got != test.wantCount {
				t.Fatalf("counted=%v want=%v bytes=%d", got, test.wantCount, counted.Load())
			}
		})
	}
}

func TestScenarioV7EightSessionServerProbeIsRealAndBounded(t *testing.T) {
	// 收集预算而非阈值。measureMultiplayerServerProbe 要求 >= 10s，此前恰好
	// 传 10s，预算等于被调用方下限是不健康的构造，因此放宽到 30s；放宽不动
	// 下面任何一条界限断言。
	//
	// 但要说清楚：**这不是本测试在 CI 上变红的成因**。实测四次红的断言都是
	// multiplayer_benchmark_server.go 的 "server input boundary 已错过 50ms tick
	// deadline"，耗时 2.43s–7.75s，远在原预算之内——预算从来不是绑定约束，
	// 放宽它对那一形态无效。真正的成因见
	// docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md §4，
	// 需要单独处理。
	multiplayer, ticks, err := measureMultiplayerServerProbe(30 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if multiplayer.ServerOutboundBytes == 0 ||
		multiplayer.InterestDiff.Samples != benchmarkServerInterestSamples ||
		ticks.Frames != benchmarkServerMeasuredTicks ||
		multiplayer.PeakRSSBytes == 0 {
		t.Fatalf("incomplete bounded server probe: multiplayer=%+v ticks=%+v", multiplayer, ticks)
	}
}

func TestBenchmarkServerProbeValidityIgnoresHighWaterButRejectsOverflow(t *testing.T) {
	report := completeBenchmarkReport()
	report.Multiplayer.OutboxHighWater = 999
	report.Multiplayer.PlayerJobsHighWater = 999
	report.Multiplayer.PlayerDoneHighWater = 999
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("队列高水位不应使完整报告失败: %v", err)
	}
	if !validBenchmarkServerProbe(false, 1, benchmarkServerInterestSamples, benchmarkServerMeasuredTicks, 1) {
		t.Fatal("完整服务端探针被拒绝")
	}
	if validBenchmarkServerProbe(true, 1, benchmarkServerInterestSamples, benchmarkServerMeasuredTicks, 1) {
		t.Fatal("真实 overflow 未被拒绝")
	}
	report.Multiplayer.ServerOutboundBytes = 0
	if err := validateBenchmarkReport(report); err == nil {
		t.Fatal("真实探针数据缺失未被拒绝")
	}
}

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
