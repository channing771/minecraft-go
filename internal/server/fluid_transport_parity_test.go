package server

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

// damSource 是溃坝夹具里唯一的水源：架在一根两格高的石柱顶上，四周与柱下皆空，
// 因此水一开始就是「已放开的」——第 0 tick 就是溃坝瞬间，不需要任何客户端指令。
var damSource = core.BlockPos{X: 0, Y: 3, Z: -5}

// damParityTicks 是溃坝之后录制的 tick 数：足够让水从 y=3 落到地面并向四周
// 铺满 7 格（含跨区块边界），并在末尾留出一段静默期。
const damParityTicks = 320

// damGenerator 生成 y=0 一层草地地面、在 damSource 正下方立一根两格石柱、
// 柱顶放一个水源的世界。水先水平溢出柱顶，再沿柱侧落到地面，最后在地面铺开，
// 因此单次录制同时覆盖垂直下落与水平铺开两类流体变更。
type damGenerator struct{}

// GenerateChunk 实现 Generator。
func (damGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	for x := range core.SectionSize {
		for z := range core.SectionSize {
			chunk.SetBlock(x, 0, z, core.GrassID)
		}
	}
	if position == damSource.Chunk() {
		x, _, z := damSource.Local()
		chunk.SetBlock(x, damSource.Y-2, z, core.StoneID)
		chunk.SetBlock(x, damSource.Y-1, z, core.StoneID)
		chunk.SetBlock(x, damSource.Y, z, core.WaterSourceID)
	}
	chunk.Compact()
	return chunk
}

// damParityRecord 是一次溃坝在某种传输下的逐 tick 广播录像。
//
// 录像从「玩家就绪且视野载入」这一刻开始对齐，而不是从连接建立开始：握手与
// 首批区块快照所占的 tick 数本就随传输而异（TCP 走真实 socket，Memory 走内存
// 管道），把它计入比对只会引入与流体无关的噪声。溃坝本身在视野载入前不会推进
// （流体只在推进范围内的区块上跑），所以两侧录像的第 0 帧是同一个溃坝瞬间。
type damParityRecord struct {
	// Ticks 按 tick 顺序保存每个 tick 广播出的方块变更（逐格、保序）。
	Ticks [][]string
}

// TestMemoryTCPFluidDamBreakBroadcastParity 覆盖任务 7.3：同一次溃坝在 Memory
// 与 TCP 两种传输下，每个 tick 广播的方块变更逐格一致。
//
// 逐格一致意味着：同一 tick、同一顺序、同一区块、同一 revision 区间、同一坐标、
// 同一方块编号。任何一侧漏掉、重排或改写一个变更都会让 DeepEqual 变红。
func TestMemoryTCPFluidDamBreakBroadcastParity(t *testing.T) {
	memory := recordDamParity(t, "memory")
	tcp := recordDamParity(t, "tcp")
	if !reflect.DeepEqual(tcp, memory) {
		// 只定位并打印第一个不等的 tick：两份完整录像有数百 KB，全量打印会把
		// 真正的差异埋掉，读者还得自己 diff。
		index, memoryTick, tcpTick := firstDamParityMismatch(memory, tcp)
		t.Fatalf("溃坝广播 Memory/TCP 在第 %d 个 tick 起不一致\nmemory=%v\ntcp=%v",
			index, memoryTick, tcpTick)
	}

	// 夹具前提守卫排在真实断言之后：真实的传输差异必须先报出自己的诊断，
	// 而不是被「夹具没水」这个误导性结论抢先。
	changes, fluids := 0, 0
	for _, tick := range memory.Ticks {
		for _, entry := range tick {
			changes++
			if isFluidParityEntry(t, entry) {
				fluids++
			}
		}
	}
	if changes < 32 || fluids < 32 {
		t.Fatalf("溃坝共广播 %d 条变更（其中流体 %d 条），想要各至少 32（夹具失效）", changes, fluids)
	}
}

// recordDamParity 在指定传输上跑一次溃坝并录下逐 tick 的方块变更。
func recordDamParity(t *testing.T, transport string) damParityRecord {
	t.Helper()
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})
	config := hostTestConfig()
	config.ViewRadius = 1
	// 关掉自动存盘：本测试只观察广播路径，存档由任务组 6 覆盖。
	config.AutosaveTicks = 1 << 30
	host := mustNewHost(t, config, damGenerator{}, store)
	identity := integrationIdentity(0x7b, "FluidDam")
	endpoint, _, closeTransport := openParityTransport(t, host, transport, identity)
	defer func() {
		_ = endpoint.Close()
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		_ = host.Shutdown(ctx)
		closeTransport()
	}()

	mirror := client.NewMirror()
	record := damParityRecord{Ticks: make([][]string, 0, damParityTicks)}
	ready := false
	for !ready || !parityViewLoaded(mirror) {
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			if state, ok := message.(network.PlayerState); ok && state.Ready {
				ready = true
			}
		}
	}

	for range damParityTicks {
		_, messages := parityStep(t, host, endpoint, mirror)
		tick := make([]string, 0, 8)
		for _, message := range messages {
			changes, ok := message.(network.BlockChanges)
			if !ok {
				continue
			}
			for _, change := range changes.Changes {
				tick = append(tick, damParityEntry(changes, change))
			}
		}
		record.Ticks = append(record.Ticks, tick)
	}
	return record
}

// damParityEntry 把一条方块变更压成可比对的规范串：区块、revision 区间、
// 世界坐标与方块编号全部进入比对，任何一项不同都会让两侧的录像不相等。
func damParityEntry(changes network.BlockChanges, change network.BlockChange) string {
	return fmt.Sprintf(
		"chunk(%d,%d)@%d->%d %d,%d,%d=%d",
		changes.Chunk.X, changes.Chunk.Z, changes.BaseRevision, changes.NewRevision,
		change.Position.X, change.Position.Y, change.Position.Z, change.Block,
	)
}

// isFluidParityEntry 报告一条录像条目写入的是否是流体方块编号。
//
// 解析失败即当场 t.Fatalf：这里的格式串必须与 damParityEntry 保持一致，静默
// 返回 false 会让格式串的任何改动伪装成「夹具没水」，把真实故障错报成夹具失效。
func isFluidParityEntry(t *testing.T, entry string) bool {
	t.Helper()
	var chunkX, chunkZ int32
	var base, next uint64
	var x, y, z int32
	var block uint16
	if _, err := fmt.Sscanf(
		entry, "chunk(%d,%d)@%d->%d %d,%d,%d=%d",
		&chunkX, &chunkZ, &base, &next, &x, &y, &z, &block,
	); err != nil {
		t.Fatalf("录像条目 %q 无法按 damParityEntry 的格式解析：%v", entry, err)
	}
	return core.IsFluid(core.BlockID(block))
}

// firstDamParityMismatch 返回两份录像第一个不等的 tick 下标及该 tick 的两个
// 切片；tick 数不同时，短的一侧返回 nil。两份完全相等时返回 (-1, nil, nil)，
// 调用方只在 DeepEqual 已判不等时使用，因此这种情况不可达。
func firstDamParityMismatch(memory, tcp damParityRecord) (int, []string, []string) {
	for i := range max(len(memory.Ticks), len(tcp.Ticks)) {
		var memoryTick, tcpTick []string
		if i < len(memory.Ticks) {
			memoryTick = memory.Ticks[i]
		}
		if i < len(tcp.Ticks) {
			tcpTick = tcp.Ticks[i]
		}
		if !reflect.DeepEqual(memoryTick, tcpTick) {
			return i, memoryTick, tcpTick
		}
	}
	return -1, nil, nil
}
