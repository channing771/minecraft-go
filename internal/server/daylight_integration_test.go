package server_test

import (
	"context"
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/world"
)

// roofTestGenerator 是昼夜纵向测试的固定夹具：Y<=groundTop 是地面，
// Y=roofY 是一整层屋顶，只在出生列上方留一个洞，形成真实的遮蔽空间。
type roofTestGenerator struct{}

const (
	roofY     = int32(3)
	groundTop = int32(0)
)

// roofHole 是屋顶上唯一的洞，位于出生列正上方。
var roofHole = core.ChunkPos{X: 0, Z: 0}

func (roofTestGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			for y := int32(core.MinY); y <= groundTop; y++ {
				block := core.StoneID
				switch {
				case y == core.MinY:
					block = core.BedrockID
				case y == groundTop:
					block = core.GrassID
				case y == groundTop-1:
					block = core.DirtID
				}
				chunk.SetBlock(x, y, z, block)
			}
			column := core.ChunkPos{
				X: position.X<<core.SectionShift + int32(x),
				Z: position.Z<<core.SectionShift + int32(z),
			}
			if column != roofHole {
				chunk.SetBlock(x, roofY, z, core.StoneID)
			}
		}
	}
	chunk.Compact()
	return chunk
}

// topFaceSkyLight 对镜像中包含 position 的区段网格化，
// 返回覆盖该方块 +Y 面的 quad 天空光。找不到该面时返回 -1。
func topFaceSkyLight(
	t *testing.T,
	mirror *client.Mirror,
	position core.BlockPos,
) int {
	t.Helper()
	get := func(pos core.ChunkPos) *world.Chunk {
		chunk, loaded := mirror.Chunk(core.Overworld, pos)
		if !loaded {
			return nil
		}
		return chunk.Chunk
	}
	neighborhood := world.NeighborhoodAt(get, position.Chunk(), position.SectionIndex())
	if neighborhood == nil {
		t.Fatalf("区块 %+v 未加载", position.Chunk())
	}

	lx, ly, lz := position.Local()
	for _, quad := range mesh.MeshSection(neighborhood, assets.NewRegistry()) {
		if quad.Face != mesh.FacePosY || int(quad.Y) != ly {
			continue
		}
		// FacePosY 的 quad 沿 z 展开 W、沿 x 展开 H。
		if int(quad.X) <= lx && lx < int(quad.X)+int(quad.H) &&
			int(quad.Z) <= lz && lz < int(quad.Z)+int(quad.W) {
			return int(quad.Light >> 4)
		}
	}
	return -1
}

func mirrorColumnTop(t *testing.T, mirror *client.Mirror, position core.BlockPos) int32 {
	t.Helper()
	chunk, loaded := mirror.Chunk(core.Overworld, position.Chunk())
	if !loaded {
		t.Fatalf("区块 %+v 未加载", position.Chunk())
	}
	lx, _, lz := position.Local()
	return chunk.Chunk.HighestOpaque(lx, lz)
}

// TestAuthoritativeRoofChangeDrivesMirrorSkyLight 证明权威方块变更经由
// 协议、Mirror 和网格化改变直射天空光：移除屋顶后下方恢复满天空光，
// 重新放置后再次变暗，且最终镜像与权威区块 hash 一致。
func TestAuthoritativeRoofChangeDrivesMirrorSkyLight(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 256
	running := newMemoryAttachedWorldWithHotbar(
		config, serverEndpoint, roofTestGenerator{}, stockedTestHotbar(core.ItemStone),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := running.Shutdown(ctx); err != nil {
			t.Errorf("关服：%v", err)
		}
		_ = clientEndpoint.Close()
	})
	mirror := client.NewMirror()

	// 洞下方的地面露天，其余地面被屋顶遮蔽。
	underHole := core.BlockPos{X: roofHole.X, Y: groundTop, Z: roofHole.Z}
	underRoof := core.BlockPos{X: 1, Y: groundTop, Z: 0}
	holeBlock := core.BlockPos{X: roofHole.X, Y: roofY, Z: roofHole.Z}
	interactionChunk := underHole.Chunk()

	stepUntil(t, running, clientEndpoint, mirror, func() bool {
		chunk, chunkOK := mirror.Chunk(core.Overworld, interactionChunk)
		player, playerOK := playerStateForExternalTest(running)
		return chunkOK && chunk.Revision == 1 && playerOK && player.Ready
	})

	// 权威快照本身就形成屋内/露天的可观察差异。
	if got := mirrorColumnTop(t, mirror, underHole); got != groundTop {
		t.Fatalf("洞下列顶 = %d，想要 %d", got, groundTop)
	}
	if got := mirrorColumnTop(t, mirror, underRoof); got != roofY {
		t.Fatalf("屋顶下列顶 = %d，想要 %d", got, roofY)
	}
	if got := topFaceSkyLight(t, mirror, underHole); got != 15 {
		t.Fatalf("洞下地面初始天空光 = %d，想要 15", got)
	}
	if got := topFaceSkyLight(t, mirror, underRoof); got != 0 {
		t.Fatalf("屋顶下地面初始天空光 = %d，想要 0", got)
	}

	// 权威放置补上屋顶洞：下方必须变暗。
	sendClientMessage(t, clientEndpoint, network.PlaceBlock{
		Sequence: 1, Yaw: 0, Pitch: 1.0, Slot: 0,
	})
	placed := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 1, 2,
	)
	if placed.Block != core.StoneID || placed.Position != holeBlock {
		t.Fatalf("放置结果 = %+v，想要在 %+v 处补上屋顶", placed, holeBlock)
	}
	if got := mirrorColumnTop(t, mirror, underHole); got != roofY {
		t.Fatalf("补洞后列顶 = %d，想要 %d", got, roofY)
	}
	if got := topFaceSkyLight(t, mirror, underHole); got != 0 {
		t.Fatalf("补洞后地面天空光 = %d，想要 0", got)
	}

	// 权威移除同一方块：下方必须恢复满天空光。
	sendClientMessage(t, clientEndpoint, network.PlayerInput{
		Sequence: 2, Yaw: 0, Pitch: 1.0, Mining: true,
	})
	broken := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 2, 3,
	)
	if broken.Block != core.AirID || broken.Position != holeBlock {
		t.Fatalf("挖掘结果 = %+v，想要移除 %+v", broken, holeBlock)
	}
	if got := mirrorColumnTop(t, mirror, underHole); got != groundTop {
		t.Fatalf("移除后列顶 = %d，想要 %d", got, groundTop)
	}
	if got := topFaceSkyLight(t, mirror, underHole); got != 15 {
		t.Fatalf("移除后地面天空光 = %d，想要 15", got)
	}
	// 相邻仍被遮蔽的列不得被误标亮。
	if got := topFaceSkyLight(t, mirror, underRoof); got != 0 {
		t.Fatalf("相邻遮蔽列天空光 = %d，想要保持 0", got)
	}

	// 派生光照不改变权威内容：最终 hash 与 revision 必须一致。
	authoritativeHash, authoritativeRevision, authoritativeOK :=
		running.ChunkHash(core.Overworld, interactionChunk)
	mirrorHash, mirrorRevision, mirrorOK := mirror.Hash(core.Overworld, interactionChunk)
	if !authoritativeOK || !mirrorOK ||
		authoritativeRevision != mirrorRevision || authoritativeHash != mirrorHash {
		t.Fatalf(
			"最终一致性失败: authoritative=(%x,%d,%v) mirror=(%x,%d,%v)",
			authoritativeHash, authoritativeRevision, authoritativeOK,
			mirrorHash, mirrorRevision, mirrorOK,
		)
	}
}
