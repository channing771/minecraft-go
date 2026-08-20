package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

// drownedColumn 是被水灌满的出生列。玩家恢复到 {0.5, 1.001, 0.5}，默认眼高 1.62
// 让眼睛落在 y=2 格，因此 y=1..2 两格水同时淹没身体与眼睛。
var drownedColumn = core.BlockPos{X: 0, Y: 1, Z: 0}

// drownedSpawnGenerator 在 integrationChunk 的平坦世界上，把出生列的 y=1..2
// 灌成水源。
//
// 注意 fluidEnabled 只门控 **worldgen 注水**，sim 的 advanceFluids 是无条件运行的：
// 这两格水会真的沿平地铺开。这对本用例无害（铺开只会让玩家更确定地泡在水里），
// 但它是下面那条「眼睛是否浸没」守卫仍然承重的原因——守卫读的是重连后玩家实际
// 所在格的镜像内容，而不是"生成器当初放过水"这个静态事实。
type drownedSpawnGenerator struct{}

// GenerateChunk 实现 Generator。
func (drownedSpawnGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := integrationChunk(position, core.DirtID)
	if position != drownedColumn.Chunk() {
		return chunk
	}
	x, _, z := drownedColumn.Local()
	for y := int32(1); y <= 2; y++ {
		chunk.SetBlock(x, y, z, core.WaterSourceID)
	}
	chunk.Compact()
	return chunk
}

// oxygenDrainedThreshold 是断线前必须观察到的耗损深度（60 tick = 3 秒）。
// 重连后的容忍窗口只有 20 tick，两个窗口之间刻意留出 40 tick 的空档，
// 「氧气被持久化了」与「重连读到满值」因此不可能同时成立。
const oxygenDrainedThreshold = core.MaxOxygenTicks - 60

// oxygenReconnectTolerance 是重连后允许的耗损量：玩家一登录就在水里，
// 从激活到测试读到第一条状态之间会真的掉几个 tick 的氧气，这个窗口容纳它。
const oxygenReconnectTolerance = core.MaxOxygenTicks - 20

// TestOxygenIsNotPersistedAcrossDiskRestart 覆盖 spec Scenario「氧气不跨重启保留」，
// 与 TestHealthSevenSurvivesDiskRestart 形成刻意的对照：生命值必须跨重启保真，
// 氧气必须不保真。
//
// 用例的关键设计在于**重生点本身就浸没**：如果玩家重连后落在干地上，第一个 tick 的
// 「出水立即回满」就会把氧气填满，那时即便加载路径完全没初始化氧气，断言照样绿。
// 让他一登录就在水里，「回满」那条分支根本不会执行，读到的值只能来自加载路径。
func TestOxygenIsNotPersistedAcrossDiskRestart(t *testing.T) {
	root := t.TempDir()
	identity := integrationIdentity(0x98, "Diver")
	loc := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1.001, 0.5}}
	seedIntegrationPlayer(t, root, identity, sim.PlayerSnapshot{Current: loc})

	first := startDiskHost(t, root, "127.0.0.1:0", drownedSpawnGenerator{})
	connected := dialIntegrationClient(t, first.Addr, identity)
	waitClientReadyFor(t, first, connected, identity.PlayerID)

	// 泡在水里直到氧气明显耗损。同一次等待顺带覆盖「氧气同步到客户端」：
	// 耗损值是从 wire 上读到的，不是从服务端内部状态窥探的。
	drained, _ := waitHealth(t, connected, func(state network.PlayerState) bool {
		return state.Ready && state.Oxygen <= oxygenDrainedThreshold
	})
	if drained.Oxygen > oxygenDrainedThreshold {
		t.Fatalf("断线前氧气 = %d，想要不高于 %d", drained.Oxygen, oxygenDrainedThreshold)
	}

	if err := connected.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}
	// 用 WaitPlayerReleased 而不是 WaitPlayerSaved：本用例在水里泡了三秒，
	// 期间自动存盘已经把这名玩家落过盘并把缓存条目清掉，断开时已无脏数据可写，
	// WaitPlayerSaved 等的那条缓存记录因此永远不会再出现。关服本身还会再刷一次。
	first.WaitPlayerReleased(t, identity.PlayerID)
	first.Shutdown(t)

	// 用同一磁盘世界重开并重连：玩家回到同一根水柱里，氧气必须是满值附近。
	second := startDiskHost(t, root, "127.0.0.1:0", drownedSpawnGenerator{})
	reconnected := dialIntegrationClient(t, second.Addr, identity)
	waitClientReadyFor(t, second, reconnected, identity.PlayerID)
	restored, _ := waitHealth(t, reconnected, func(state network.PlayerState) bool {
		return state.Ready
	})
	if restored.Oxygen < oxygenReconnectTolerance {
		t.Fatalf("重启后氧气 = %d，想要不低于 %d（氧气疑似跨重启保留了）",
			restored.Oxygen, oxygenReconnectTolerance)
	}

	// 夹具承重守卫排在真实断言之后：重连后的玩家必须真的还在水里，
	// 否则上面那条断言只是在陈述「出水的人氧气满」，加载路径零覆盖。
	// 判定走的是权威侧同一个 physics.SubmersionFlags，只是喂给它客户端镜像。
	_, eyeInFluid := physics.SubmersionFlags(restored.Position, client.MirrorCollisionSource{
		Mirror: reconnected.Mirror, Dimension: core.Overworld,
	})
	if !eyeInFluid {
		t.Fatal("夹具无效：重连后的玩家眼睛不在水里，「出水立即回满」会替加载路径把氧气填满")
	}

	if err := reconnected.Close(); err != nil {
		t.Fatal(err)
	}
	second.Shutdown(t)
}

// TestPlayerSaveHasNoOxygenField 覆盖同一 Scenario 的另一半：
// 「玩家存档 MUST NOT 包含氧气字段」。
//
// 上一条用例证明的是"读回来是满值"，这条证明的是"根本没地方存"——只有两条都
// 成立，玩家 schema 保持 v6 才是真的。反射扫字段名，而不是比对某个具体长度：
// 后者会在无关字段增删时误报，也挡不住"换个名字塞进去"。
//
// 覆盖面如实说明：**只扫顶层字段**，把氧气藏进 Current / Safe / Inventory 这类
// 嵌套结构体里不会被它发现。没做成递归是因为真实的失效模式是"顺手在 PlayerSave
// 上加一个字段"，而嵌套结构体各自另有存档契约与用例；等真出现"氧气被塞进嵌套
// 类型"这种情况再改成递归也不迟。
func TestPlayerSaveHasNoOxygenField(t *testing.T) {
	saveType := reflect.TypeOf(storage.PlayerSave{})
	if saveType.NumField() == 0 {
		t.Fatal("夹具无效：storage.PlayerSave 没有任何字段，逐字段扫描等于没扫")
	}
	for index := range saveType.NumField() {
		name := saveType.Field(index).Name
		if strings.Contains(strings.ToLower(name), "oxygen") ||
			strings.Contains(strings.ToLower(name), "drown") ||
			strings.Contains(strings.ToLower(name), "breath") {
			t.Fatalf("storage.PlayerSave 出现了氧气字段 %s：氧气是瞬态权威值，不得进存档", name)
		}
	}
	// 守卫排在真实断言之后：扫描必须真的能认出它要拒绝的名字，否则上面那个循环
	// 只是在遍历一堆无关字段、永远不会命中。
	type oxygenBearingSave struct{ Oxygen uint16 }
	rejected := false
	witness := reflect.TypeOf(oxygenBearingSave{})
	for index := range witness.NumField() {
		if strings.Contains(strings.ToLower(witness.Field(index).Name), "oxygen") {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("守卫失效：连一个字面叫 Oxygen 的字段都认不出来")
	}
}
