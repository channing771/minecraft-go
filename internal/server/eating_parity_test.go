package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

// 进食 parity 脚本的夹具常量。
const (
	// eatingParityHunger 是玩家登录时的权威饥饿值。取 12 而不是初值 20：
	// 饥饿已满时进食状态机根本不推进，脚本会在什么都没发生的情况下全绿。
	// 12 + 面包的 5 = 17，仍在上限之内，因此"加了多少"是可读的精确读数。
	eatingParityHunger uint8 = 12
	// eatingParitySaturation 是登录时的饱和度（千分位），≤ 12×1000 满足存档
	// 编码的上界校验。
	eatingParitySaturation uint16 = 5000
	// eatingParityWheat 是夹具**直接放进快捷栏**的小麦数，恰好是面包配方的
	// 用量。这里刻意不走农业闭环（翻地 → 播种 → 等作物成熟 → 收获）：那条
	// 路径要跨上千个权威 tick，而本脚本要验的是"两种传输下进食结算一致"，
	// 不是农业本身——农业闭环由 farming_loop_e2e_test.go 覆盖。
	//
	// 也不能指望缺失玩家的一次性材料包：它给的是 core.ItemWheatSeeds
	// （见 starterMaterialInventory），不是小麦，更不是面包。
	eatingParityWheat uint8 = 3
	// eatingParityEatTicks 是脚本按住进食输入推进的 tick 数，必须等于权威
	// 默认的 EatingTicks。sim 的默认值不导出（archcheck 的禁导出清单），
	// 这里写字面量并由第 31 tick「饥饿精确不变」+ 第 32 tick「精确 +5」
	// 两条断言共同钉死：默认值一旦改动，这两条会同时变红。
	eatingParityEatTicks = 32
)

// eatingParityResult 是一次进食脚本在某种传输下的可比结果：逐条业务消息的
// 文本转写、末态饥饿值与末态快捷栏。两种传输的这个结构体必须逐字段相同。
type eatingParityResult struct {
	Transcript  []string
	FinalHunger uint8
	FinalHotbar core.Hotbar
}

// TestMemoryTCPEatingConvergence 覆盖 spec「相同输入重放逐位一致」在进食上的
// 那一半：同一串「合成面包 → 选中 → 长按 32 tick」的输入在 Memory 与 TCP 两种
// 传输下必须产生逐字段相同的业务转写。
//
// 断言的饥饿值读自 **wire 上的 `network.PlayerState.Hunger`**，不是窥探服务端
// 内部状态：单机与远程共用同一套模拟这条架构约束，只有在"客户端真的收到了
// 同样的字节"这个层面上才是可验证的。
func TestMemoryTCPEatingConvergence(t *testing.T) {
	memory := runEatingParityScript(t, "memory")
	tcp := runEatingParityScript(t, "tcp")
	if !reflect.DeepEqual(tcp, memory) {
		t.Fatalf("进食 Memory/TCP 未收敛\nmemory=%+v\ntcp=%+v", memory, tcp)
	}
	// 夹具自证：转写必须真的记下了整段进食，否则两个空切片也"相同"。
	if len(memory.Transcript) < eatingParityEatTicks {
		t.Fatalf("进食转写只有 %d 条，短于进食本身的 %d tick",
			len(memory.Transcript), eatingParityEatTicks)
	}
}

// runEatingParityScript 在一种传输上跑完整段进食脚本并返回可比结果。
//
// 脚本形状照 runMiningParityScript：先把登录、视野与背包确认跑完，再逐 tick
// 发命令并把每一条业务消息经 parityBusinessMessage 转写成文本。
func runEatingParityScript(t *testing.T, transport string) eatingParityResult {
	t.Helper()
	identity := integrationIdentity(0x74, "EatingParity")
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemWheat, Count: eatingParityWheat}
	location := storage.PlayerLocation{Dimension: core.Overworld, Position: [3]float32{0.5, 1.001, 0.5}}
	if _, err := store.SavePlayer(context.Background(), storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: location, Safe: &location, Inventory: inventory,
		// 生命值取满是承重条件：非满血玩家会自然回血，而一次回血累积 6000
		// 疲劳（超过 4000 的阈值）会当场把饥饿值扣下去，"进食前后精确 ±5"
		// 的断言就不再归因于进食。
		Health: core.MaxHealth,
		Hunger: eatingParityHunger, SaturationMilli: eatingParitySaturation,
	}); err != nil {
		t.Fatal(err)
	}
	config := hostTestConfig()
	config.ViewRadius = 1
	config.AutosaveTicks = 1000
	host := mustNewHost(t, config, flatGenerator{}, store)
	endpoint, acceptDone, closeTransport := openParityTransport(t, host, transport, identity)
	defer closeTransport()
	mirror := client.NewMirror()

	ready := false
	inventoryConfirmed := false
	for !ready || !inventoryConfirmed || !parityViewLoaded(mirror) {
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			switch message := message.(type) {
			case network.PlayerState:
				assertValidIntegrationPlayerState(t, message)
				ready = ready || message.Ready
			case network.InventoryState:
				inventoryConfirmed = message.Inventory == inventory
			}
		}
	}

	result := eatingParityResult{Transcript: make([]string, 0, 64)}
	hotbar := inventory.Hotbar
	// step 发一条命令（可为 nil）、推进一个权威 tick，并把本 tick 的全部业务
	// 消息转写进 result.Transcript，同时把 wire 上的饥饿值与快捷栏抄出来。
	step := func(command network.ClientMessage) network.PlayerState {
		if command != nil {
			sendIntegration(t, endpoint, command)
			waitIntegrationCondition(
				t, fmt.Sprintf("%s eating %T queued", transport, command),
				func() bool { return len(host.world.incoming) > 0 },
			)
		}
		_, messages := parityStep(t, host, endpoint, mirror)
		var state network.PlayerState
		for _, message := range messages {
			result.Transcript = append(result.Transcript, parityBusinessMessage(t, mirror, message)...)
			switch message := message.(type) {
			case network.PlayerState:
				assertValidIntegrationPlayerState(t, message)
				state = message
			case network.InventoryState:
				hotbar = message.Inventory.Hotbar
			}
		}
		return state
	}

	// 第一段：3 小麦经既有合成命令换成 1 个面包。产物落在最低的空快捷栏位
	// （原料就是从 0 号格扣走的），因此紧接着的选中命令指向 0 号格。
	state := step(network.CraftRecipe{Sequence: 1, Recipe: core.RecipeBread})
	want := core.ItemStack{Item: core.ItemBread, Count: 1}
	if hotbar.Slots[0] != want {
		t.Fatalf("%s 合成后 0 号格=%+v，想要 %+v", transport, hotbar.Slots[0], want)
	}
	if state.Hunger != eatingParityHunger {
		t.Fatalf("%s 合成后 wire 饥饿值=%d，想要 %d", transport, state.Hunger, eatingParityHunger)
	}
	state = step(network.SelectHotbar{Sequence: 2, Slot: 0})
	if state.Hunger != eatingParityHunger {
		t.Fatalf("%s 选中后 wire 饥饿值=%d，想要 %d", transport, state.Hunger, eatingParityHunger)
	}

	// 第二段：按住进食输入整整 EatingTicks 个 tick。输入只发一次——权威侧的
	// 进食意图在下一条 PlayerInput 到达之前保持不变，与采掘同形。
	for tick := 1; tick <= eatingParityEatTicks; tick++ {
		var command network.ClientMessage
		if tick == 1 {
			command = network.PlayerInput{Sequence: 3, Eating: true}
		}
		state = step(command)
		if tick < eatingParityEatTicks {
			// 第 1..31 tick：wire 上的饥饿值必须**精确不变**。少了这条，
			// 结算提前一 tick 的实现在末态断言下同样全绿。
			if state.Hunger != eatingParityHunger {
				t.Fatalf("%s 进食第 %d tick wire 饥饿值=%d，想要精确保持 %d",
					transport, tick, state.Hunger, eatingParityHunger)
			}
			if hotbar.Slots[0] != want {
				t.Fatalf("%s 进食第 %d tick 0 号格=%+v，想要精确保持 %+v",
					transport, tick, hotbar.Slots[0], want)
			}
		}
	}
	if state.Hunger != eatingParityHunger+5 {
		t.Fatalf("%s 进食第 %d tick wire 饥饿值=%d，想要 %d",
			transport, eatingParityEatTicks, state.Hunger, eatingParityHunger+5)
	}
	if hotbar.Slots[0] != (core.ItemStack{}) {
		t.Fatalf("%s 结算后 0 号格=%+v，想要清空", transport, hotbar.Slots[0])
	}
	result.FinalHunger = state.Hunger
	result.FinalHotbar = hotbar

	if err := endpoint.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	select {
	case err := <-acceptDone:
		if err != nil && !errors.Is(err, network.ErrClosed) {
			t.Fatalf("%s eating accept worker: %v", transport, err)
		}
	case <-ctx.Done():
		t.Fatalf("%s eating accept worker did not exit: %v", transport, ctx.Err())
	}
	if err := host.Shutdown(ctx); err != nil {
		t.Fatalf("%s eating Host.Shutdown: %v", transport, err)
	}
	return result
}
