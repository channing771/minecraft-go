package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
)

func TestAuthoritativeInteractionRoundTrip(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 256
	running := newMemoryAttachedWorldForExternalTest(config, serverEndpoint, server.FlatTestGenerator{})
	mirror := client.NewMirror()

	interactionChunk := (core.BlockPos{X: 0, Y: 1, Z: -6}).Chunk()
	stepUntil(t, running, clientEndpoint, mirror, func() bool {
		chunk, chunkOK := mirror.Chunk(core.Overworld, interactionChunk)
		player, playerOK := playerStateForExternalTest(running)
		return chunkOK && chunk.Revision == 1 && playerOK && player.Ready
	})

	pitch := float32(-0.2)
	placements := []struct {
		position core.BlockPos
		block    core.BlockID
	}{
		{position: core.BlockPos{X: 0, Y: 1, Z: -5}, block: core.StoneID},
		{position: core.BlockPos{X: 0, Y: 1, Z: -4}, block: core.DirtID},
		{position: core.BlockPos{X: 0, Y: 1, Z: -3}, block: core.GrassID},
	}
	for index, placement := range placements {
		sequence := uint64(index + 1)
		sendClientMessage(t, clientEndpoint, network.PlaceBlock{
			Sequence: sequence,
			Yaw:      0,
			Pitch:    pitch,
			Block:    placement.block,
		})
		assertContiguousInteraction(
			t,
			running,
			clientEndpoint,
			mirror,
			sequence,
			sequence+1,
			placement.position,
			placement.block,
		)
	}

	breakPosition := placements[len(placements)-1].position
	sendClientMessage(t, clientEndpoint, network.BreakBlock{
		Sequence: 4,
		Yaw:      0,
		Pitch:    pitch,
	})
	assertContiguousInteraction(
		t, running, clientEndpoint, mirror, 4, 5, breakPosition, core.AirID,
	)

	assertMirrorBlock(t, mirror, breakPosition, core.AirID)
	for _, placement := range placements[:len(placements)-1] {
		assertMirrorBlock(t, mirror, placement.position, placement.block)
	}
	authoritativeHash, authoritativeRevision, authoritativeOK := running.ChunkHash(
		core.Overworld,
		interactionChunk,
	)
	mirrorHash, mirrorRevision, mirrorOK := mirror.Hash(core.Overworld, interactionChunk)
	if !authoritativeOK || !mirrorOK ||
		authoritativeRevision != mirrorRevision ||
		authoritativeHash != mirrorHash {
		t.Fatalf(
			"交互后一致性失败: authoritative=(%x,%d,%v) mirror=(%x,%d,%v)",
			authoritativeHash,
			authoritativeRevision,
			authoritativeOK,
			mirrorHash,
			mirrorRevision,
			mirrorOK,
		)
	}

	serverContext, cancelServer := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- running.Run(serverContext) }()
	cancelServer()
	select {
	case err := <-serverDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Server.Run 退出错误 = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消后 Server.Run 未在 1 秒内退出")
	}
	if err := clientEndpoint.Close(); err != nil {
		t.Fatalf("关闭客户端端点: %v", err)
	}
}

func assertContiguousInteraction(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	baseRevision uint64,
	newRevision uint64,
	position core.BlockPos,
	wantBlock core.BlockID,
) {
	t.Helper()
	var matching []network.BlockChanges
	stepUntilCollect(t, running, endpoint, mirror, func(message network.ServerMessage) {
		if delta, ok := message.(network.BlockChanges); ok && delta.Chunk == position.Chunk() {
			matching = append(matching, delta)
		}
	}, func() bool {
		_, revision, ok := mirror.Hash(core.Overworld, position.Chunk())
		return ok && revision == newRevision
	})
	if len(matching) != 1 ||
		matching[0].BaseRevision != baseRevision ||
		matching[0].NewRevision != newRevision {
		t.Fatalf(
			"交互 delta = %+v，想要唯一 %d→%d",
			matching,
			baseRevision,
			newRevision,
		)
	}
	assertMirrorBlock(t, mirror, position, wantBlock)
}

func assertMirrorBlock(
	t *testing.T,
	mirror *client.Mirror,
	position core.BlockPos,
	want core.BlockID,
) {
	t.Helper()
	got, loaded := mirror.BlockAt(core.Overworld, position)
	if !loaded || got != want {
		t.Fatalf("BlockAt(%+v) = %d,%v，想要 %d,true", position, got, loaded, want)
	}
}

func sendClientMessage(
	t *testing.T,
	endpoint network.ClientEndpoint,
	message network.ClientMessage,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := endpoint.Send(ctx, message); err != nil {
		t.Fatalf("发送 %#v: %v", message, err)
	}
}

func stepUntil(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	done func() bool,
) {
	t.Helper()
	stepUntilCollect(t, running, endpoint, mirror, nil, done)
}

func stepUntilCollect(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	collect func(network.ServerMessage),
	done func() bool,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !done() {
		result := running.StepForTest()
		drainServerMessages(t, endpoint, mirror, collect, result.Tick)
		if time.Now().After(deadline) {
			t.Fatalf("等待权威状态超时；mirror center=%+v", mirrorChunkSummary(mirror))
		}
	}
}

func drainServerMessages(
	t *testing.T,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	collect func(network.ServerMessage),
	throughTick uint64,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		if collect != nil {
			collect(message)
		}
		if state, ok := message.(network.PlayerState); ok {
			if state.ServerTick == throughTick {
				return
			}
			if state.ServerTick > throughTick {
				t.Fatalf("PlayerState tick=%d，跳过目标 tick=%d", state.ServerTick, throughTick)
			}
			continue
		}
		update, err := mirror.Apply(message)
		if err != nil {
			t.Fatalf("Mirror.Apply(%T): %v", message, err)
		}
		if update.Resync != nil {
			t.Fatalf("无头一致性场景意外需要 resync: %+v", update.Resync)
		}
		if update.Rejected != nil {
			t.Fatalf("权威命令被拒绝: %+v", update.Rejected)
		}
	}
}

func mirrorChunkSummary(mirror *client.Mirror) any {
	chunk, ok := mirror.Chunk(core.Overworld, core.ChunkPos{})
	if !ok {
		return "missing"
	}
	return struct {
		Revision uint64
		Desynced bool
	}{Revision: chunk.Revision, Desynced: chunk.Desynced}
}
