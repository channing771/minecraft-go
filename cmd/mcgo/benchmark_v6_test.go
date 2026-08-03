//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/server"
)

func TestScenarioV6ContainsSevenSortedUnicodeRemotePlayers(t *testing.T) {
	if scenarioVersion != 6 {
		t.Fatalf("scenarioVersion=%d, want 6", scenarioVersion)
	}
	scenario := newMultiplayerBenchmarkScenario()
	if !scenario.LocalPlayerID.Valid() {
		t.Fatalf("local PlayerID invalid: %x", scenario.LocalPlayerID)
	}
	if len(scenario.Spawns) != 7 || len(scenario.Tags) != 7 {
		t.Fatalf("spawns/tags = %d/%d, want 7/7", len(scenario.Spawns), len(scenario.Tags))
	}
	for index, spawn := range scenario.Spawns {
		if spawn.PlayerID == scenario.LocalPlayerID || !spawn.PlayerID.Valid() ||
			spawn.DisplayName == "" || len([]rune(spawn.DisplayName)) == len(spawn.DisplayName) {
			t.Fatalf("spawn[%d] does not prove distinct Unicode identity: %+v", index, spawn)
		}
		if scenario.Tags[index].PlayerID != spawn.PlayerID ||
			scenario.Tags[index].Text != spawn.DisplayName {
			t.Fatalf("tag[%d]=%+v does not match spawn=%+v", index, scenario.Tags[index], spawn)
		}
	}
	batch := scenario.States(42)
	if batch.ServerTick != 42 || len(batch.Players) != 7 {
		t.Fatalf("tick batch = %+v, want tick 42/count 7", batch)
	}
	for index := 1; index < len(batch.Players); index++ {
		if bytes.Compare(batch.Players[index-1].PlayerID[:], batch.Players[index].PlayerID[:]) >= 0 {
			t.Fatalf("states not strictly sorted at %d", index)
		}
	}
	if err := batch.Validate(); err != nil {
		t.Fatalf("scenario batch invalid: %v", err)
	}
	for _, tag := range scenario.Tags {
		if !strings.ContainsAny(tag.Text, "界月星河山海云") {
			t.Fatalf("tag is not the fixed Unicode fixture: %q", tag.Text)
		}
	}
}

func TestScenarioV6RenderFrameSamplesExistingRemotePassesExactlyOnce(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "星河", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	timing := newMultiplayerRenderTiming()
	app.multiplayerRenderTiming = timing
	var nowCalls int
	app.multiplayerRenderNow = func() time.Time {
		nowCalls++
		return time.Unix(0, int64(nowCalls)*int64(time.Millisecond))
	}
	rendered, err := app.renderFrame(1)
	if err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{"terrain pass", "avatar pass", "name-tag pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("passes=%v want exactly one existing pass each: %v", got, want)
	}
	avatar, nameTag := timing.Summaries()
	if avatar != (client.LatencySummary{Samples: 1, P50MS: 1, P95MS: 1, P99MS: 1, MaxMS: 1}) {
		t.Fatalf("avatar timing=%+v, want one 1ms actual Render sample", avatar)
	}
	if nameTag != (client.LatencySummary{Samples: 1, P50MS: 2, P95MS: 2, P99MS: 2, MaxMS: 2}) {
		t.Fatalf("name-tag timing=%+v, want Prepare+Render sum of 2ms", nameTag)
	}
	if nowCalls != 6 {
		t.Fatalf("clock reads=%d, want exactly three start/end pairs", nowCalls)
	}
}

func TestScenarioV6NilRenderTimingNeverReadsBenchmarkClock(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "月海", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	app.multiplayerRenderTiming = nil
	app.multiplayerRenderNow = func() time.Time {
		panic("nil benchmark timing read wall clock")
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("nil timing renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{"terrain pass", "avatar pass", "name-tag pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nil timing changed passes=%v want=%v", got, want)
	}
}

func TestScenarioV6NameTagFailurePublishesNoRenderTimingSample(t *testing.T) {
	wantErr := errors.New("injected glyph flush failure")
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: wantErr})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "云野", 1, mgl32.Vec3{0, 0, -4})); err != nil {
		t.Fatal(err)
	}
	timing := newMultiplayerRenderTiming()
	app.multiplayerRenderTiming = timing
	app.multiplayerRenderNow = time.Now
	rendered, err := app.renderFrame(1)
	if rendered || !errors.Is(err, wantErr) {
		t.Fatalf("renderFrame=(%v,%v), want wrapped glyph error", rendered, err)
	}
	avatar, nameTag := timing.Summaries()
	if avatar.Samples != 0 || nameTag.Samples != 0 {
		t.Fatalf("failed frame published successful timing: avatar=%+v nameTag=%+v", avatar, nameTag)
	}
	if got := dev.lastPasses(); len(got) != 0 {
		t.Fatalf("glyph failure encoded passes=%v, want none", got)
	}
}

func TestBenchmarkServerMeasuredWindowSendsOneSequencePerCompletedTick(t *testing.T) {
	epoch := newBenchmarkServerEpoch()
	testCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sent := make(chan uint64, 1)
	var sequences []uint64
	var events []string
	var statsCalls, rssCalls int
	sendInputs := func(_ context.Context, sequence uint64) error {
		events = append(events, "send")
		sequences = append(sequences, sequence)
		sent <- sequence
		return nil
	}
	if err := epoch.beginMeasurement(func() error {
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
				epoch.signals <- benchmarkServerTickSignal{measured: true}
			case <-testCtx.Done():
				return
			}
		}
	}()
	summary, err := runBenchmarkServerMeasuredWindow(
		testCtx,
		epoch,
		8,
		sendInputs,
		func() server.HostStats {
			events = append(events, "tick")
			statsCalls++
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
	if len(events) != 2*benchmarkServerMeasuredTicks {
		t.Fatalf("events=%d want=%d", len(events), 2*benchmarkServerMeasuredTicks)
	}
	for index, event := range events {
		want := "send"
		if index%2 == 1 {
			want = "tick"
		}
		if event != want {
			t.Fatalf("event[%d]=%q want=%q; inputs are not tick-driven", index, event, want)
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

func TestScenarioV6EightSessionServerProbeIsRealAndBounded(t *testing.T) {
	multiplayer, ticks, err := measureMultiplayerServerProbe(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if multiplayer.ServerOutboundBytes == 0 ||
		multiplayer.InterestDiff.Samples != benchmarkServerInterestSamples ||
		ticks.Frames != benchmarkServerMeasuredTicks ||
		multiplayer.OutboxHighWater > benchmarkOutboxLimit ||
		multiplayer.PlayerJobsHighWater > 16 || multiplayer.PlayerDoneHighWater > 2 ||
		multiplayer.PeakRSSBytes == 0 || multiplayer.PeakRSSBytes >= 2<<30 {
		t.Fatalf("incomplete bounded server probe: multiplayer=%+v ticks=%+v", multiplayer, ticks)
	}
}

func TestPerformanceThresholdsRejectTickP99AtTenMilliseconds(t *testing.T) {
	report := validBenchmarkReport()
	report.ScenarioVersion = 6
	report.Ticks.P99MS = 9.999
	report.Multiplayer = validMultiplayerSummary()
	if err := validateBenchmarkReport(report); err != nil {
		t.Fatalf("9.999ms tick p99 rejected: %v", err)
	}
	report.Ticks.P99MS = 10
	if err := validateBenchmarkReport(report); err == nil || !strings.Contains(err.Error(), ">= 10 ms") {
		t.Fatalf("10ms tick p99 boundary error=%v", err)
	}
}

func validMultiplayerSummary() client.MultiplayerSummary {
	latency := client.LatencySummary{Samples: 256, P50MS: 0.001, P95MS: 0.002, P99MS: 0.003, MaxMS: 0.004}
	return client.MultiplayerSummary{
		RemoteStateEncode: latency, RemoteStateDecode: latency,
		InterestDiff: client.LatencySummary{Samples: 1000, P50MS: 0.001, P95MS: 0.002, P99MS: 0.003, MaxMS: 0.004},
		RosterApply:  latency, Interpolation: latency, AvatarSubmit: latency,
		NameTagSubmit: latency, RemoteGPUComplete: latency,
		ServerOutboundBytes: 1, OutboxHighWater: 1, PlayerJobsHighWater: 1,
		PlayerDoneHighWater: 1, PeakRSSBytes: 1,
	}
}
