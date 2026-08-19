package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

// fluidPublicationSource 是测试世界里唯一的水源位置：刻意远离出生点，避免出生
// 扫描把它当成落脚点，也让水有完整的 7 格铺开空间。
var fluidPublicationSource = core.BlockPos{X: 0, Y: 1, Z: -5}

// fluidGenerator 生成 y=0 一层草地、并在 fluidPublicationSource 处放一个水源的
// 平坦世界。水源随区块快照一起下发，之后的铺开则必须走区块变更通道。
type fluidGenerator struct{}

// GenerateChunk 实现 Generator。
func (fluidGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	for x := range core.SectionSize {
		for z := range core.SectionSize {
			chunk.SetBlock(x, 0, z, core.GrassID)
		}
	}
	if position == fluidPublicationSource.Chunk() {
		x, _, z := fluidPublicationSource.Local()
		chunk.SetBlock(x, fluidPublicationSource.Y, z, core.WaterSourceID)
	}
	chunk.Compact()
	return chunk
}

// TestFluidChangesBroadcastOverExistingChunkChannel 覆盖 spec Scenario
// 「流体方块同步到客户端」与 design.md D8「不新增协议消息」：
//
//   - 权威侧的流动写入必须经既有的 network.BlockChanges 通道广播；
//   - 客户端只读镜像收下之后逐格与权威世界一致（用整块哈希比对，比抽查几格更严）；
//   - 整个过程中出现的服务端消息类型必须全部落在本变更之前就存在的集合里。
func TestFluidChangesBroadcastOverExistingChunkChannel(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})
	config := hostTestConfig()
	config.ViewRadius = 1
	// 关掉自动存盘，测试只观察广播路径，不牵扯存档 schema（那是任务组 6）。
	config.AutosaveTicks = 1 << 30
	host := mustNewHost(t, config, fluidGenerator{}, store)
	identity := integrationIdentity(0x77, "FluidBroadcast")
	endpoint, _, closeTransport := openParityTransport(t, host, "memory", identity)
	t.Cleanup(func() {
		_ = endpoint.Close()
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		_ = host.Shutdown(ctx)
		closeTransport()
	})

	mirror := client.NewMirror()
	// 本变更之前就存在的服务端消息类型全集。流体不得引入任何新类型（D8）。
	preexisting := map[string]struct{}{}
	for _, message := range []network.ServerMessage{
		network.KeepAlive{}, network.Disconnect{},
		network.ChunkSnapshot{}, network.BlockChanges{}, network.ForgetChunks{},
		network.PlayerState{}, network.CommandRejected{},
		network.RemotePlayerSpawn{}, network.RemotePlayerDespawn{}, network.RemotePlayerStates{},
		network.InventoryState{},
		network.FurnaceState{}, network.ChestState{}, network.ContainerClosed{},
		network.ItemDropUpserts{}, network.ItemDropRemoves{},
		network.ChatEvent{}, network.CompanionSpawn{}, network.CompanionStates{},
		network.CompanionDespawn{},
	} {
		preexisting[fmt.Sprintf("%T", message)] = struct{}{}
	}

	// 水源最弱的一格：距源 7 格 ⇒ 等级 7；第 8 格必须保持空气。
	farthest := core.BlockPos{X: 0, Y: 1, Z: fluidPublicationSource.Z - 7}
	beyond := core.BlockPos{X: 0, Y: 1, Z: fluidPublicationSource.Z - 8}
	fluidViaBlockChanges := false
	converged := false
	for range 240 {
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			if _, known := preexisting[fmt.Sprintf("%T", message)]; !known {
				t.Fatalf("流体引入了新的服务端消息类型 %T", message)
			}
			changes, ok := message.(network.BlockChanges)
			if !ok {
				continue
			}
			for _, change := range changes.Changes {
				// 只认最远那一格：它必须是"从空气变成流体"之后才被广播出来的，
				// 而不是随初始区块快照一起下发的——否则这条断言会被快照顺带满足。
				if change.Position == farthest && core.IsFluid(change.Block) {
					fluidViaBlockChanges = true
				}
			}
		}
		if block, ok := mirror.BlockAt(core.Overworld, farthest); ok && block == core.WaterLevel7ID {
			converged = true
			break
		}
	}
	if !converged {
		block, _ := mirror.BlockAt(core.Overworld, farthest)
		t.Fatalf("镜像中最远的一格 %+v=%d，想要 %d", farthest, block, core.WaterLevel7ID)
	}
	if !fluidViaBlockChanges {
		t.Fatalf("最远的一格 %+v 不是经 network.BlockChanges 通道到达客户端的", farthest)
	}
	if block, ok := mirror.BlockAt(core.Overworld, beyond); !ok || block != core.AirID {
		t.Fatalf("镜像中第 8 格 %+v=%d，想要空气", beyond, block)
	}

	// 先确认水确实越过了 x=0 这条区块边界，否则下面的跨区块哈希比对会退化成
	// "比了两块一模一样的草地"。
	acrossChunkSeam := core.BlockPos{X: fluidPublicationSource.X - 7, Y: 1, Z: fluidPublicationSource.Z}
	if block, ok := mirror.BlockAt(core.Overworld, acrossChunkSeam); !ok || block != core.WaterLevel7ID {
		t.Fatalf("镜像中越过区块边界的一格 %+v=%d，想要 %d", acrossChunkSeam, block, core.WaterLevel7ID)
	}
	if acrossChunkSeam.Chunk() == fluidPublicationSource.Chunk() {
		t.Fatalf("%+v 与水源同属区块 %+v，没有跨区块", acrossChunkSeam, acrossChunkSeam.Chunk())
	}

	// 逐格一致：镜像区块与权威区块的整块哈希与 revision 必须完全相同。
	//
	// 这两个区块必须**跨区块边界**，比对才有意义：水源在 (0,1,-5)，向四个水平
	// 方向各铺 7 格，因此水同时落在区块 {0,-1}（水源自身与 -Z 方向）与区块
	// {-1,-1}（-X 方向越过 x=0 边界的那几格）。fluidPublicationSource.Chunk()
	// 与 farthest.Chunk() 都是 {0,-1}，写成那样等于把同一块比了两次。
	for _, position := range []core.ChunkPos{
		{X: 0, Z: -1}, {X: -1, Z: -1},
	} {
		authorityHash, authorityRevision, ok := host.world.ChunkHash(core.Overworld, position)
		if !ok {
			t.Fatalf("权威区块 %+v 不可用", position)
		}
		mirrorHash, mirrorRevision, ok := mirror.Hash(core.Overworld, position)
		if !ok {
			t.Fatalf("镜像区块 %+v 不可用", position)
		}
		if authorityHash != mirrorHash || authorityRevision != mirrorRevision {
			t.Fatalf(
				"区块 %+v 镜像与权威不一致: 权威 %x@%d，镜像 %x@%d",
				position, authorityHash, authorityRevision, mirrorHash, mirrorRevision,
			)
		}
	}
}
