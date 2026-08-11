//go:build darwin

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"sync/atomic"

	"minecraft-go/internal/client"
	"minecraft-go/internal/network"
)

func readFloat32(data []byte, offset int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
}

type benchmarkBlockingServerStream struct {
	entered chan struct{}
	release chan struct{}
}

func (stream *benchmarkBlockingServerStream) Send(
	ctx context.Context,
	_ network.State,
	_ network.ServerPacket,
) error {
	stream.entered <- struct{}{}
	select {
	case <-stream.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*benchmarkBlockingServerStream) Recv(
	context.Context,
	network.State,
) (network.ClientPacket, error) {
	return nil, errors.New("unused benchmark Recv")
}

func (*benchmarkBlockingServerStream) Peer() string { return "benchmark-blocking" }
func (*benchmarkBlockingServerStream) Close() error { return nil }

type benchmarkCloseErrorClientEndpoint struct {
	network.ClientEndpoint
	err        error
	closeCalls atomic.Int32
}

func (endpoint *benchmarkCloseErrorClientEndpoint) Close() error {
	endpoint.closeCalls.Add(1)
	return errors.Join(endpoint.ClientEndpoint.Close(), endpoint.err)
}

type benchmarkCloseErrorServerEndpoint struct {
	network.ServerEndpoint
	err        error
	closeCalls atomic.Int32
}

func (endpoint *benchmarkCloseErrorServerEndpoint) Close() error {
	endpoint.closeCalls.Add(1)
	return errors.Join(endpoint.ServerEndpoint.Close(), endpoint.err)
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

func completeBenchmarkReport() client.PerfReport {
	report := validBenchmarkReport()
	report.ScenarioVersion = 14
	report.Hardware = "test-hardware"
	report.OS = "test-os"
	report.GoVersion = "test-go"
	report.GitCommit = "test-commit"
	report.Framebuffer = "2560x1440"
	report.Phases = map[string]client.PhaseSummary{
		"still":  {Frames: 1000, FPS: 100, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4, PeakRSSBytes: 1},
		"flying": {Frames: 1000, FPS: 100, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4, PeakRSSBytes: 1},
	}
	report.Ticks = client.PhaseSummary{Frames: 200, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4}
	report.Persistence = client.PersistenceSummary{Snapshots: 1, P50MS: 1, P95MS: 2, P99MS: 3, MaxMS: 4}
	report.Multiplayer = validMultiplayerSummary()
	return report
}
