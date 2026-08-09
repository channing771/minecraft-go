package server_test

import (
	"context"
	"runtime"
	"testing"
	"time"

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

// topFaceSkyLight 返回已由 Mesher 收敛的区段中覆盖 position 的 +Y 面天空光。
// 找不到该面时返回 -1。
func topFaceSkyLight(
	t *testing.T,
	section client.MeshedSection,
	position core.BlockPos,
) int {
	t.Helper()
	lx, ly, lz := position.Local()
	for _, quad := range section.Quads {
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

// awaitMeshedSection 驱动既有调度器，等待目标区段的最新权威 revision 网格结果。
func awaitMeshedSection(
	t *testing.T,
	mesher *client.Mesher,
	mirror *client.Mirror,
	position core.BlockPos,
	revision uint64,
) client.MeshedSection {
	t.Helper()
	key := meshedSectionKey(position)
	deadline := time.Now().Add(waitDeadline)
	for {
		mesher.Schedule(mirror, 1)
		for _, section := range mesher.Drain(mirror, 1) {
			if section.Dimension != key.Dimension || section.Pos != key.Pos {
				continue
			}
			for _, stamp := range section.Stamps {
				if stamp.Dimension == key.Dimension && stamp.Chunk == position.Chunk() {
					if !stamp.Present || stamp.Revision != revision {
						t.Fatalf("网格 revision = %+v，想要 %d", stamp, revision)
					}
					return section
				}
			}
			t.Fatalf("网格结果缺少中心区块 stamp: %+v", section.Stamps)
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待区段 %+v 的 revision %d 网格超时；stats=%+v", key, revision, mesher.Stats())
		}
		runtime.Gosched()
	}
}

func meshedSectionKey(position core.BlockPos) core.SectionKey {
	return core.SectionKey{
		Dimension: core.Overworld,
		Pos: core.SectionPos{
			X: position.Chunk().X,
			Y: int32(position.SectionIndex()),
			Z: position.Chunk().Z,
		},
	}
}

// drainMesher 消费当前 dirty backlog，确保下一次冻结的只有指定区段任务。
func drainMesher(t *testing.T, mesher *client.Mesher, mirror *client.Mirror) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		mesher.Schedule(mirror, 1)
		mesher.Drain(mirror, 1)
		stats := mesher.Stats()
		if stats.DirtySections == 0 && stats.QueuedJobs == 0 &&
			stats.InFlightJobs == 0 && stats.ReadyResults == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 Mesher 收敛超时: %+v", stats)
		}
		runtime.Gosched()
	}
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
// 协议、Mirror 和网格化改变天空光：移除屋顶后下方恢复满天空光，
// 重新放置后只保留相邻开口传播的天空光，且最终镜像与权威区块 hash 一致。
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
	mesher := client.NewMesher(assets.NewRegistry(), 1)
	t.Cleanup(mesher.Close)

	// 洞下方的地面露天，其余地面被屋顶遮蔽。
	underHole := core.BlockPos{X: roofHole.X, Y: groundTop, Z: roofHole.Z}
	oneStep := core.BlockPos{X: 1, Y: groundTop, Z: 0}
	lastLit := core.BlockPos{X: 14, Y: groundTop, Z: 0}
	firstDark := core.BlockPos{X: 15, Y: groundTop, Z: 0}
	holeBlock := core.BlockPos{X: roofHole.X, Y: roofY, Z: roofHole.Z}
	interactionChunk := underHole.Chunk()

	stepUntil(t, running, clientEndpoint, mirror, func() bool {
		chunk, chunkOK := mirror.Chunk(core.Overworld, interactionChunk)
		player, playerOK := playerStateForExternalTest(running)
		return chunkOK && chunk.Revision == 1 && playerOK && player.Ready
	}, mesher)
	initial := awaitMeshedSection(t, mesher, mirror, underHole, 1)
	drainMesher(t, mesher, mirror)

	// 权威快照本身就形成屋内/露天的可观察差异。
	if got := mirrorColumnTop(t, mirror, underHole); got != groundTop {
		t.Fatalf("洞下列顶 = %d，想要 %d", got, groundTop)
	}
	if got := mirrorColumnTop(t, mirror, oneStep); got != roofY {
		t.Fatalf("屋顶下列顶 = %d，想要 %d", got, roofY)
	}
	for _, check := range []struct {
		name     string
		position core.BlockPos
		want     int
	}{
		{"洞下", underHole, 15},
		{"一格传播", oneStep, 14},
		{"最后亮格", lastLit, 1},
		{"首个暗格", firstDark, 0},
	} {
		if got := topFaceSkyLight(t, initial, check.position); got != check.want {
			t.Fatalf("%s地面初始天空光 = %d，想要 %d", check.name, got, check.want)
		}
	}

	// 权威放置补上屋顶洞：下方必须变暗。
	sectionKey := meshedSectionKey(underHole)
	releaseRevision1 := mesher.BlockForTest(sectionKey)
	mesher.MarkDirty(sectionKey)
	mesher.Schedule(mirror, 1)
	waitForMesherStats(t, mesher, func(stats client.MesherStats) bool {
		return stats.DirtySections == 1 && stats.QueuedJobs == 0 &&
			stats.InFlightJobs == 1 && stats.ReadyResults == 0
	})
	sendClientMessage(t, clientEndpoint, network.PlaceBlock{
		Sequence: 1, Yaw: 0, Pitch: 1.0, Slot: 0,
	})
	placed := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 1, 2, mesher,
	)
	if placed.Block != core.StoneID || placed.Position != holeBlock {
		t.Fatalf("放置结果 = %+v，想要在 %+v 处补上屋顶", placed, holeBlock)
	}
	releaseRevision1()
	waitForMesherStats(t, mesher, func(stats client.MesherStats) bool {
		return stats.QueuedJobs == 0 && stats.InFlightJobs == 0 && stats.ReadyResults == 1
	})
	if stale := mesher.Drain(mirror, 1); len(stale) != 0 {
		t.Fatalf("封洞后接受了 revision 1 的旧网格: %+v", stale)
	}
	if stats := mesher.Stats(); stats.ReadyResults != 0 {
		t.Fatalf("封洞后旧网格未被 Drain 消费: %+v", stats)
	}
	sealed := awaitMeshedSection(t, mesher, mirror, underHole, 2)
	drainMesher(t, mesher, mirror)
	if got := mirrorColumnTop(t, mirror, underHole); got != roofY {
		t.Fatalf("补洞后列顶 = %d，想要 %d", got, roofY)
	}
	for _, check := range []struct {
		name     string
		position core.BlockPos
	}{
		{"洞下", underHole},
		{"一格传播", oneStep},
		{"最后亮格", lastLit},
		{"首个暗格", firstDark},
	} {
		if got := topFaceSkyLight(t, sealed, check.position); got != 0 {
			t.Fatalf("补洞后%s地面天空光 = %d，想要 0", check.name, got)
		}
	}

	// 权威移除同一方块：下方必须恢复满天空光。
	releaseRevision2 := mesher.BlockForTest(sectionKey)
	mesher.MarkDirty(sectionKey)
	mesher.Schedule(mirror, 1)
	waitForMesherStats(t, mesher, func(stats client.MesherStats) bool {
		return stats.DirtySections == 1 && stats.QueuedJobs == 0 &&
			stats.InFlightJobs == 1 && stats.ReadyResults == 0
	})
	sendClientMessage(t, clientEndpoint, network.PlayerInput{
		Sequence: 2, Yaw: 0, Pitch: 1.0, Mining: true,
	})
	broken := awaitInteractionChange(
		t, running, clientEndpoint, mirror, interactionChunk, 2, 3, mesher,
	)
	if broken.Block != core.AirID || broken.Position != holeBlock {
		t.Fatalf("挖掘结果 = %+v，想要移除 %+v", broken, holeBlock)
	}
	releaseRevision2()
	waitForMesherStats(t, mesher, func(stats client.MesherStats) bool {
		return stats.QueuedJobs == 0 && stats.InFlightJobs == 0 && stats.ReadyResults == 1
	})
	if stale := mesher.Drain(mirror, 1); len(stale) != 0 {
		t.Fatalf("重开后接受了 revision 2 的旧网格: %+v", stale)
	}
	if stats := mesher.Stats(); stats.ReadyResults != 0 {
		t.Fatalf("重开后旧网格未被 Drain 消费: %+v", stats)
	}
	reopened := awaitMeshedSection(t, mesher, mirror, underHole, 3)
	if got := mirrorColumnTop(t, mirror, underHole); got != groundTop {
		t.Fatalf("移除后列顶 = %d，想要 %d", got, groundTop)
	}
	for _, check := range []struct {
		name     string
		position core.BlockPos
		want     int
	}{
		{"洞下", underHole, 15},
		{"一格传播", oneStep, 14},
		{"最后亮格", lastLit, 1},
		{"首个暗格", firstDark, 0},
	} {
		if got := topFaceSkyLight(t, reopened, check.position); got != check.want {
			t.Fatalf("重开后%s地面天空光 = %d，想要 %d", check.name, got, check.want)
		}
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
