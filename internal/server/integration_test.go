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
	running := newMemoryAttachedWorldWithHotbar(
		config, serverEndpoint, server.FlatTestGenerator{}, stockedTestHotbar(core.ItemStone),
	)
	mirror := client.NewMirror()

	interactionChunk := (core.BlockPos{X: 0, Y: 1, Z: -6}).Chunk()
	stepUntil(t, running, clientEndpoint, mirror, func() bool {
		chunk, chunkOK := mirror.Chunk(core.Overworld, interactionChunk)
		player, playerOK := playerStateForExternalTest(running)
		return chunkOK && chunk.Revision == 1 && playerOK && player.Ready
	})

	pitch := float32(-0.2)
	// M4B：挖掘只产生地面掉落物，放置改用登录时已确认的快捷栏物品。
	sendClientMessage(t, clientEndpoint, network.BreakBlock{
		Sequence: 1,
		Yaw:      0,
		Pitch:    pitch,
	})
	broken := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 1, 2,
	)
	if broken.Block != core.AirID {
		t.Fatalf("挖掘结果 = %+v，想要空气", broken)
	}

	// 障碍消失后需要压低视角才能命中六格内的地面。
	sendClientMessage(t, clientEndpoint, network.PlaceBlock{
		Sequence: 2,
		Yaw:      0,
		Pitch:    -0.6,
		Slot:     0,
	})
	placed := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 2, 3,
	)
	if placed.Position == broken.Position || placed.Block != core.StoneID {
		t.Fatalf("放置结果 = %+v，想要放下快捷栏中的石头", placed)
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

// awaitInteractionChange 等待唯一一份 base→new 的 delta，并返回其中唯一的方块变化。
func awaitInteractionChange(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
	mirror *client.Mirror,
	chunk core.ChunkPos,
	baseRevision uint64,
	newRevision uint64,
) network.BlockChange {
	t.Helper()
	var matching []network.BlockChanges
	stepUntilCollect(t, running, endpoint, mirror, func(message network.ServerMessage) {
		if delta, ok := message.(network.BlockChanges); ok && delta.Chunk == chunk {
			matching = append(matching, delta)
		}
	}, func() bool {
		_, revision, ok := mirror.Hash(core.Overworld, chunk)
		return ok && revision == newRevision
	})
	if len(matching) != 1 || matching[0].BaseRevision != baseRevision ||
		matching[0].NewRevision != newRevision || len(matching[0].Changes) != 1 {
		t.Fatalf(
			"交互 delta = %+v，想要唯一 %d→%d 的单方块变化",
			matching,
			baseRevision,
			newRevision,
		)
	}
	change := matching[0].Changes[0]
	assertMirrorBlock(t, mirror, change.Position, change.Block)
	return change
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
		switch message.(type) {
		case network.HotbarState, network.ItemDropUpserts, network.ItemDropRemoves:
			// 快捷栏与掉落物由独立的只读镜像消费，不进入世界镜像。
			continue
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
