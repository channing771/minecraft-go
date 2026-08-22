package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

// 端到端饥饿脚本的固定预算与夹具量。预算的**唯一职责**是把挂起变成一条读得懂
// 的失败而不是 go test 超时，因此它们不是性能断言，宁可宽到几乎不可能误伤。
const (
	// hungerLoopFallHeight 是脚本用来受伤的落差：摔落曲线是
	// `floor(峰值Y − 落地Y) − 3`，10 格恰好扣 7 点。选 10 而不是更大的落差，
	// 是为了让「扣 7 → 回 6 次血」这段刚好停在 19 点而**不满血**：回血一旦
	// 把生命值填满，`advanceHealthRegen` 会在入口短路，后面「回血停止」那一步
	// 就分不清是饥饿门控生效还是满血短路，整条脚本退化成一句空话。
	hungerLoopFallHeight = float32(10)
	// hungerLoopFallDamage 是上面那次落差的精确伤害点数。
	hungerLoopFallDamage uint8 = 7
	// hungerLoopWheat 是夹具直接放进快捷栏的小麦数，恰好是面包配方的用量。
	hungerLoopWheat uint8 = 3
	// hungerLoopLoginBudget 是等登录就绪（Ready + 背包发布 + 九个区块进镜像）
	// 的 tick 预算，取值理由同 `farmingLoginBudget`：实测卡点是异步区块生成，
	// 并发跑满包时会漂到 300 tick 以上，3000 给到一个数量级余量。
	hungerLoopLoginBudget = 3000
	// hungerLoopGroundBudget 是等玩家在出生列站稳的 tick 预算。出生点在地面
	// 上方 0.001 格，一步就该落地；60 已是三个数量级的余量。
	hungerLoopGroundBudget = 60
	// hungerLoopFallBudget 是等 10 格自由落体触地的 tick 预算。按权威物理
	// 积分约 25 tick，200 足够吸收任何重力 tunable 的合理取值。
	hungerLoopFallBudget = 200
	// hungerLoopSettleTicks 是一条命令之后让背包发布收敛的 tick 数。
	hungerLoopSettleTicks = 3
)

// hungerLoopReading 是某个权威 tick 在 wire 上读到的一对 (生命值, 饥饿值)。
// 两个字段都来自 `network.PlayerState`，不是服务端内部快照。
type hungerLoopReading struct {
	Health uint8
	Hunger uint8
}

// hungerLoopStage 是期望数列的一项：阶段说明加上该阶段**新出现**的读数。
type hungerLoopStage struct {
	Name    string
	Reading hungerLoopReading
}

// hungerLoopExpected 是整条脚本在 wire 上的 (生命值, 饥饿值) 精确数列。
//
// 脚本全程只有一个疲劳来源——自然回血（每回 1 点生命值累积 6000 千分位疲劳，
// 见 `internal/sim/hunger.go` 的固定表），因此每一行都能由两个常量算死：
// 阈值 4000、初始饱和度 5000（= 5 点）。第 k 次回血之后累计疲劳是 6000k，
// 跨过的阈值数是 `floor(6000k/4000)`；前 5 个阈值吃掉初始饱和度，第 6 个起
// 才开始扣饥饿值。
//
//	k:            1     2     3     4     5     6
//	累计疲劳:   6000 12000 18000 24000 30000 36000
//	跨阈值数:      1     3     4     6     7     9
//	扣饥饿点:      0     0     0     1     2     4
//
// **饥饿值不会停在 17**：第 6 次回血一次加 6000，在阈值 4000 上连跨两格，
// 饥饿值从 18 直接掉到 16。17 只是 `applyExhaustion` 循环里的中间态，
// 外部一个 tick 都观察不到。回血门控的判据是 `hunger >= 18`，16 与 17 在
// 门控上是同一侧，spec 的 Scenario「饥饿值 17 不回血」在 sim 侧由组 1 的
// 成对夹具直接覆盖；这里覆盖的是它在真实机制下的自然落点。
var hungerLoopExpected = []hungerLoopStage{
	{"登录：缺失玩家的一次性材料包 + 三层饥饿初值", hungerLoopReading{Health: core.MaxHealth, Hunger: core.MaxHunger}},
	{"摔落 10 格：floor(10) − 3 = 7 点伤害", hungerLoopReading{Health: core.MaxHealth - hungerLoopFallDamage, Hunger: core.MaxHunger}},
	{"回血 1：疲劳 6000，跨 1 阈值，饱和 5000 → 4000", hungerLoopReading{Health: 14, Hunger: 20}},
	{"回血 2：累计 12000，跨 3 阈值，饱和 4000 → 2000", hungerLoopReading{Health: 15, Hunger: 20}},
	{"回血 3：累计 18000，跨 4 阈值，饱和 2000 → 1000", hungerLoopReading{Health: 16, Hunger: 20}},
	{"回血 4：累计 24000，跨 6 阈值，饱和见底后开始扣饥饿", hungerLoopReading{Health: 17, Hunger: 19}},
	{"回血 5：累计 30000，跨 7 阈值", hungerLoopReading{Health: 18, Hunger: 18}},
	{"回血 6：累计 36000，跨 9 阈值，一次连扣两点，饥饿跌破门控", hungerLoopReading{Health: 19, Hunger: 16}},
	{"进食第 32 tick 结算：饥饿 +5 钳到上限", hungerLoopReading{Health: 19, Hunger: core.MaxHunger}},
	{"恢复后的第一次回血", hungerLoopReading{Health: core.MaxHealth, Hunger: core.MaxHunger}},
}

// TestHungerLoopEndToEndMemory 是 authoritative-hunger 整条生存循环的集成回归：
// 一名**从未存在过**的玩家登录之后走完
// 「受伤 → 自然回血把饱和度与饥饿值烧掉 → 饥饿跌破门控、回血停摆 →
// 合成面包 → 长按吃下 → 饥饿回满 → 回血恢复」。
// 它是「饥饿让回血有代价、小麦因此变得有用」这句话的唯一可执行证据。
//
// 三条刻意的设计约束，每条都对应一类假绿：
//
//  1. **饥饿必须由机制跑下去**。脚本一个字节都不播种三层饥饿状态：玩家是
//     `ErrPlayerNotFound` 路径构造的新玩家，三层状态是固定初值 20/5000/0，
//     此后每一次下降都由「回血 → `applyExhaustion(6000)` → 跨阈值」跑出来。
//     直接播种 17 的话，疲劳表与回血消耗一行都没被覆盖，整条脚本退化成组 4
//     的进食 parity。
//  2. **每一步都是精确数列而不是「饥饿降了」**。`hungerLoopExpected` 列出
//     wire 上 (生命值, 饥饿值) 的全部变化点，脚本逐点断言并在末尾比对整条
//     数列——中间任何一步错（少跨一个阈值、多扣一点饥饿、回血早一拍）都会
//     被抓住，而只断言终态的写法对这些改动全部免疫。
//  3. **「回血停止」必须在非满血上验**。第 6 次回血把生命值停在 19 而不是
//     20，脚本随后推进 `RegenDelayTicks + 3×RegenIntervalTicks` 个 tick 并
//     逐 tick 要求生命值**精确不变**。满血夹具下门控开不开都不回血，那种
//     用例对「删掉门控」这个变异是全绿的。
//
// 受伤来源选**真实摔落**而不是任何直接改血的测试入口：摔落走的是既有
// `applyDamage` 入口，因此「受伤重置回血计时」这条前提也是跑出来的而不是假设的。
//
// 3 小麦由夹具 `SetPlayerInventoryForTest` 直给并写在这里：小麦可得已由
// `TestFarmingLoopEndToEndMemory`（种 → 长 → 收，精确数列 63 → 65 → 64）证明，
// 本脚本不重跑农业闭环——那条路径要跨上千个权威 tick，而且它验的是农业本身。
// 一次性材料包只发种子（`starterMaterialInventory`），不发小麦更不发面包，
// 脚本在第 1 步显式钉住这一点。
//
// 全部断言都从 wire 读：生命值与饥饿值读 `network.PlayerState`，背包读已确认的
// `network.InventoryState`。单机与远程共用同一套模拟这条架构约束，只有在
// 「客户端真的收到了这些字节」这个层面上才是可验证的。
func TestHungerLoopEndToEndMemory(t *testing.T) {
	// 三个节律常量全部读默认 tunable 而不是写字面量：它们在 sim 里是不导出的
	// 常量（archcheck 的禁导出清单），字面量在这里只会变成第二份数值来源。
	// 默认值本身由组 1、组 4 的 sim 用例钉住。
	tunables := sim.DefaultTunables()
	regenDelay := uint64(tunables.RegenDelayTicks)
	regenInterval := uint64(tunables.RegenIntervalTicks)
	eatingTicks := int(tunables.EatingTicks)

	identity := integrationIdentity(0x8f, "Hungry")
	// 刻意**不**预存玩家：只有 LoadPlayer 返回 ErrPlayerNotFound 的路径才会
	// 构造一次性材料包与三层饥饿初值，而这两者正是整条脚本的起点。
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})

	config := hostTestConfig()
	config.ViewRadius = 1
	// 脚本要跑数百个 tick，自动存盘调远一点，免得混进无关的存盘噪声。
	config.AutosaveTicks = 1 << 20
	host := mustNewHost(t, config, flatGenerator{}, store)
	endpoint, _, closeTransport := openParityTransport(t, host, "memory", identity)
	t.Cleanup(func() {
		_ = endpoint.Close()
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		_ = host.Shutdown(ctx)
		closeTransport()
	})

	mirror := client.NewMirror()
	// wireInventory 是 wire 上最后一份已确认的完整物品状态；readings 是
	// (生命值, 饥饿值) 的**变化点**序列（相邻重复读数折叠）。
	var wireInventory core.Inventory
	var readings []hungerLoopReading

	// —— 第 1 步：登录即拿到材料包，且身上没有任何食物 ——
	//
	// 等待条件刻意只看「发布过一份非空背包」，**不**看具体物品：条件里写物品的话，
	// 材料包被改动时这个循环会一直空转到 go test 超时，而超时是一种读不出原因的红。
	ready, inventoryReady := false, false
	for ticks := 0; !ready || !inventoryReady || !parityViewLoaded(mirror); ticks++ {
		if ticks > hungerLoopLoginBudget {
			t.Fatalf("登录 %d 个 tick 后仍未就绪: ready=%v 背包已发布=%v 视野已加载=%v",
				ticks, ready, inventoryReady, parityViewLoaded(mirror))
		}
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			switch message := message.(type) {
			case network.PlayerState:
				ready = ready || message.Ready
			case network.InventoryState:
				wireInventory = message.Inventory
				inventoryReady = inventoryReady || message.Inventory != core.Inventory{}
			}
		}
	}
	wantSeeds := core.ItemStack{Item: core.ItemWheatSeeds, Count: core.MaxStackCount}
	if got := wireInventory.Backpack[starterSeedSlot]; got != wantSeeds {
		t.Fatalf("登录后材料包第 %d 格 = %+v，想要 %+v",
			starterSeedSlot+1, got, wantSeeds)
	}
	// 「没有食物」必须扫全部 36 格：材料包若顺手发一块面包，后面的合成与
	// 「饿到门控之下」两步都会失去意义，而只看快捷栏的断言抓不住那种材料包。
	if got := countItem(wireInventory, core.ItemBread); got != 0 {
		t.Fatalf("登录时已持有面包 %d 个，材料包不该发食物", got)
	}
	if got := countItem(wireInventory, core.ItemWheat); got != 0 {
		t.Fatalf("登录时已持有小麦 %d 个，材料包只发种子", got)
	}

	host.mu.Lock()
	active := host.activeByPlayer[identity.PlayerID]
	host.mu.Unlock()
	if active == nil {
		t.Fatal("玩家还没有 active 会话")
	}
	session := active.Session

	// step 推进一个权威 tick，把 wire 上的物品状态与 (生命值, 饥饿值) 抄出来，
	// 并把任何命令拒绝直接判失败。
	step := func() network.PlayerState {
		t.Helper()
		_, messages := parityStep(t, host, endpoint, mirror)
		var state network.PlayerState
		for _, message := range messages {
			switch message := message.(type) {
			case network.CommandRejected:
				t.Fatalf("端到端脚本的命令被拒绝: %+v", message)
			case network.PlayerState:
				assertValidIntegrationPlayerState(t, message)
				state = message
			case network.InventoryState:
				wireInventory = message.Inventory
			}
		}
		reading := hungerLoopReading{Health: state.Health, Hunger: state.Hunger}
		if len(readings) == 0 || readings[len(readings)-1] != reading {
			readings = append(readings, reading)
		}
		return state
	}
	send := func(command network.ClientMessage) network.PlayerState {
		t.Helper()
		sendIntegration(t, endpoint, command)
		waitIntegrationCondition(t, fmt.Sprintf("hunger loop %T queued", command), func() bool {
			return len(host.world.incoming) > 0
		})
		return step()
	}
	settle := func() {
		t.Helper()
		for range hungerLoopSettleTicks {
			step()
		}
	}

	// —— 第 2 步：先站稳，再摔一跤 ——
	//
	// 摔落峰值只在「上一 tick 还站在地面」时取瞬移后的高度，因此必须先等玩家
	// 在出生列站稳，落差才是确定的 10 格而不是首个空中 tick 的高度。
	var grounded network.PlayerState
	for ticks := 0; !grounded.OnGround; ticks++ {
		if ticks > hungerLoopGroundBudget {
			t.Fatalf("等了 %d 个 tick 玩家仍未站稳: %+v", ticks, grounded)
		}
		grounded = step()
	}
	if got := (hungerLoopReading{Health: grounded.Health, Hunger: grounded.Hunger}); got != hungerLoopExpected[0].Reading {
		t.Fatalf("站稳时 (生命值, 饥饿值) = %+v，想要 %+v", got, hungerLoopExpected[0].Reading)
	}
	host.world.SetPlayerPositionForTest(session, grounded.Position.Add(
		mgl32.Vec3{0, hungerLoopFallHeight, 0},
	))
	damaged := grounded
	for ticks := 0; damaged.Health == core.MaxHealth; ticks++ {
		if ticks > hungerLoopFallBudget {
			t.Fatalf("抬高 %v 格后等了 %d 个 tick 仍未摔到地面: %+v",
				hungerLoopFallHeight, ticks, damaged)
		}
		damaged = step()
	}
	if got := (hungerLoopReading{Health: damaged.Health, Hunger: damaged.Hunger}); got != hungerLoopExpected[1].Reading {
		t.Fatalf("落地时 (生命值, 饥饿值) = %+v，想要 %+v（%s）",
			got, hungerLoopExpected[1].Reading, hungerLoopExpected[1].Name)
	}
	// 受伤这一 tick 是全部回血节律的原点：`applyDamage` 在这里把回血计时清零，
	// 第 k 次回血因此精确落在 damageTick + RegenDelayTicks + k×RegenIntervalTicks。
	damageTick := damaged.ServerTick

	// —— 第 3 步：六次自然回血，把饱和度烧完再开始扣饥饿 ——
	//
	// 每一次回血都断言三件事：生命值恰好 +1、饥饿值精确等于疲劳表算出的值、
	// 且落在算死的那个 tick 上。少了 tick 断言，「回血间隔被改」这类变异在
	// 「等到生命值变了为止」的循环下照样全绿。
	state := damaged
	for regen := uint64(1); regen <= 6; regen++ {
		wantTick := damageTick + regenDelay + regen*regenInterval
		wantStage := hungerLoopExpected[1+regen]
		for state.ServerTick < wantTick {
			state = step()
			got := hungerLoopReading{Health: state.Health, Hunger: state.Hunger}
			if state.ServerTick < wantTick && got != hungerLoopExpected[regen].Reading {
				t.Fatalf("第 %d 次回血之前的 tick %d 读到 %+v，想要精确保持 %+v",
					regen, state.ServerTick, got, hungerLoopExpected[regen].Reading)
			}
		}
		got := hungerLoopReading{Health: state.Health, Hunger: state.Hunger}
		if got != wantStage.Reading {
			t.Fatalf("第 %d 次回血（tick %d）读到 %+v，想要 %+v（%s）",
				regen, state.ServerTick, got, wantStage.Reading, wantStage.Name)
		}
	}

	// —— 第 4 步：饥饿跌破门控，回血确实停了 ——
	//
	// 生命值停在 19 而不是满血，因此这一步测的是门控本身而不是满血短路。
	// 推进的窗口取 RegenDelayTicks + 3×RegenIntervalTicks：既覆盖「计时重来
	// 一遍」也覆盖「按原节律再来三拍」，两种改法都跑不掉。
	stalled := hungerLoopExpected[7].Reading
	for tick := uint64(0); tick < regenDelay+3*regenInterval; tick++ {
		state = step()
		got := hungerLoopReading{Health: state.Health, Hunger: state.Hunger}
		if got != stalled {
			t.Fatalf("饥饿 %d < 门控时推进第 %d 个 tick 读到 %+v，想要精确保持 %+v",
				stalled.Hunger, tick+1, got, stalled)
		}
	}

	// —— 第 5 步：夹具直给 3 小麦，合成面包 ——
	host.world.SetPlayerInventoryForTest(session, func(inventory core.Inventory) core.Inventory {
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemWheat, Count: hungerLoopWheat}
		return inventory
	})
	wantWheat := core.ItemStack{Item: core.ItemWheat, Count: hungerLoopWheat}
	for ticks := 0; wireInventory.Hotbar.Slots[0] != wantWheat; ticks++ {
		if ticks > hungerLoopSettleTicks {
			t.Fatalf("夹具写入后等了 %d 个 tick，wire 上 0 号格仍是 %+v，想要 %+v",
				ticks, wireInventory.Hotbar.Slots[0], wantWheat)
		}
		step()
	}
	send(network.CraftRecipe{Sequence: 1, Recipe: core.RecipeBread})
	settle()
	wantBread := core.ItemStack{Item: core.ItemBread, Count: 1}
	if got := wireInventory.Hotbar.Slots[0]; got != wantBread {
		t.Fatalf("合成后 wire 上 0 号格 = %+v，想要 %+v", got, wantBread)
	}
	if got := countItem(wireInventory, core.ItemWheat); got != 0 {
		t.Fatalf("合成后剩余小麦 = %d，想要 0（一个面包恰好吃掉 3 个）", got)
	}
	// 进食只认权威选中格，把面包那一格显式选上。
	send(network.SelectHotbar{Sequence: 2, Slot: 0})
	settle()

	// —— 第 6 步：长按整整 EatingTicks 个 tick，饥饿回满 ——
	//
	// 输入只发一次：权威侧的进食意图在下一条 `PlayerInput` 到达之前保持不变，
	// 与采掘同形。第 1..31 tick 逐 tick 要求饥饿值与生命值**精确不变**，
	// 结算提前一拍的实现只看末态是抓不住的。
	for tick := 1; tick <= eatingTicks; tick++ {
		if tick == 1 {
			state = send(network.PlayerInput{Sequence: 3, Eating: true})
		} else {
			state = step()
		}
		got := hungerLoopReading{Health: state.Health, Hunger: state.Hunger}
		if tick < eatingTicks {
			if got != stalled {
				t.Fatalf("进食第 %d tick 读到 %+v，想要精确保持 %+v", tick, got, stalled)
			}
			if slot := wireInventory.Hotbar.Slots[0]; slot != wantBread {
				t.Fatalf("进食第 %d tick 0 号格 = %+v，想要精确保持 %+v", tick, slot, wantBread)
			}
		}
	}
	fed := hungerLoopExpected[8]
	if got := (hungerLoopReading{Health: state.Health, Hunger: state.Hunger}); got != fed.Reading {
		t.Fatalf("进食第 %d tick 读到 %+v，想要 %+v（%s）",
			eatingTicks, got, fed.Reading, fed.Name)
	}
	if got := wireInventory.Hotbar.Slots[0]; got != (core.ItemStack{}) {
		t.Fatalf("结算后 wire 上 0 号格 = %+v，想要清空", got)
	}

	// —— 第 7 步：回血恢复 ——
	//
	// `advanceHealthRegen` 排在 `advanceEating` **之前**，所以结算那一 tick 的
	// 门控读到的还是吃之前的饥饿值，回血最早也只能从下一 tick 起。恢复后的
	// 第一次回血仍落在原节律上（受伤计时在门控关着的时候照常累积），因此它的
	// tick 是算得死的：damageTick + 延迟 + 间隔的整数倍里第一个大于结算 tick 的。
	settleTick := state.ServerTick
	resumeTick := damageTick + regenDelay + regenInterval
	for resumeTick <= settleTick {
		resumeTick += regenInterval
	}
	resumed := hungerLoopExpected[9].Reading
	for tick := uint64(0); tick < regenInterval; tick++ {
		state = step()
		got := hungerLoopReading{Health: state.Health, Hunger: state.Hunger}
		want := fed.Reading
		if state.ServerTick >= resumeTick {
			want = resumed
		}
		if got != want {
			t.Fatalf("进食后第 %d 个 tick（tick %d，期望回血 tick %d）读到 %+v，想要 %+v",
				tick+1, state.ServerTick, resumeTick, got, want)
		}
	}
	if got := (hungerLoopReading{Health: state.Health, Hunger: state.Hunger}); got != resumed {
		t.Fatalf("推进一个回血间隔后读到 %+v，想要 %+v（%s）",
			got, resumed, hungerLoopExpected[9].Name)
	}

	// —— 收尾：整条数列逐点比对 ——
	//
	// 上面每一步都已就地断言；这一条把「整条脚本的变化点恰好是这 10 个、
	// 顺序也恰好如此」这个结论本身也写下来：任何多出来的中间态（例如门控
	// 失效后多回的那一点血）都会先在这里现形。
	if len(readings) != len(hungerLoopExpected) {
		t.Fatalf("整条脚本的 (生命值, 饥饿值) 变化点有 %d 个，想要 %d 个\n实际 = %+v",
			len(readings), len(hungerLoopExpected), readings)
	}
	for index, stage := range hungerLoopExpected {
		if readings[index] != stage.Reading {
			t.Fatalf("第 %d 个变化点 = %+v，想要 %+v（%s）",
				index+1, readings[index], stage.Reading, stage.Name)
		}
	}
}
