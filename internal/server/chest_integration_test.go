package server

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

// chestScriptLookDown 是俯视脚下箱子的固定视角，与熔炉纵向测试共用同一常量值。
const chestScriptLookDown = -float32(math.Pi)/2 + 0.01

// runChestSharedByTwoPlayersScript 是 Memory 与 TCP 共用的箱子纵向脚本：
// 放置箱子 -> 两名玩家同时打开 -> 交错存取 -> 两端同见最终内容 ->
// 破坏者破坏箱子 -> 另一名玩家收到关闭通知 -> 旧引用命令被拒绝。
// 两种 transport 唯一的差异在调用方如何搭建 world/连接，脚本本身逐条复用。
func runChestSharedByTwoPlayersScript(
	t *testing.T,
	running *Server,
	breaker, other integrationClient,
) {
	t.Helper()
	key := core.ChunkKey{Dimension: core.Overworld}
	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("箱子位置没有区块索引")
	}
	running.SetBlockForTest(core.BlockPos{}, core.ChestID)
	running.SetChunkChestForTest(key, 0, world.ChestSlot{
		Generation: 1, Active: true, BlockIndex: index,
	})

	// 两名玩家同时打开同一个箱子。
	sendIntegration(t, breaker.Endpoint, network.OpenContainer{
		Sequence: 10, Pitch: chestScriptLookDown,
	})
	sendIntegration(t, other.Endpoint, network.OpenContainer{
		Sequence: 10, Pitch: chestScriptLookDown,
	})
	breakerRef := waitChestState(t, breaker, func(network.ChestState) bool { return true }).Chest
	otherRef := waitChestState(t, other, func(network.ChestState) bool { return true }).Chest
	if breakerRef != otherRef {
		t.Fatalf("两名查看者的引用不同: %+v vs %+v", breakerRef, otherRef)
	}

	// 交错存取：两名玩家几乎同时把各自持有的物品移入箱子的不同格。
	sendIntegration(t, breaker.Endpoint, network.MoveContainerStack{
		Sequence: 11, Container: breakerRef, From: 1, To: core.ChestFirstSlot + 0,
	})
	sendIntegration(t, other.Endpoint, network.MoveContainerStack{
		Sequence: 11, Container: otherRef, From: 0, To: core.ChestFirstSlot + 1,
	})

	// 两端最终必须同见完全一致的箱子内容；物品状态必须反映各自的来源格已清空。
	// 两个信号可能落在同一 tick，分两次独立 Wait 会把先到的那条消息在另一次里丢掉，
	// 因此在同一遍扫描里一起等。
	final := func(state network.ChestState) bool {
		return state.Items[0].Item == core.ItemStone && state.Items[0].Count == 5 &&
			state.Items[1].Item == core.ItemDirt && state.Items[1].Count == 3
	}
	waitChestStateWithClearedSlot(t, breaker, final, 1)
	waitChestStateWithClearedSlot(t, other, final, 0)

	// 破坏者先关闭查看关系（查看容器时无法采掘），再切到石镐挖掉箱子。
	// 三条命令来自同一条连接，传输层保证到达顺序，因此不需要在此处等待关闭生效的回执：
	// CloseContainer 一定先于随后的采掘命令被服务端处理。
	sendIntegration(t, breaker.Endpoint, network.CloseContainer{Sequence: 12})
	sendIntegration(t, breaker.Endpoint, network.SelectHotbar{Sequence: 13, Slot: 0})
	sendIntegration(t, breaker.Endpoint, network.PlayerInput{
		Sequence: 14, Pitch: chestScriptLookDown, Mining: true,
	})

	// 两名查看者都必须精确收到一次关闭通知：破坏者是主动关闭后再破坏，
	// 另一名玩家是箱子在其查看期间失效触发的被动关闭。
	waitContainerClosed(t, other, otherRef)

	// 物品状态：破坏成功后石镐耐久必须恰好 -1（consumeToolDurability 每次破坏扣一点）。
	fullDurability, ok := core.ItemMaxDurability(core.ItemStonePickaxe)
	if !ok {
		t.Fatal("石镐没有最大耐久")
	}
	waitIntegrationState(t, breaker, func(message network.ServerMessage) bool {
		state, ok := message.(network.InventoryState)
		return ok && state.Inventory.Hotbar.Slots[0].Item == core.ItemStonePickaxe &&
			state.Inventory.Hotbar.Slots[0].Durability == fullDurability-1
	})

	// 最终区块：方块变回空气，箱子槽停用、内容清零，但 Generation 保留。
	finalChunk, _, ok := running.CloneReadyChunkForTest(key)
	if !ok {
		t.Fatal("破坏后区块不可用")
	}
	x, _, z := core.BlockPos{}.Local()
	if got := finalChunk.BlockAt(x, 0, z); got != core.AirID {
		t.Fatalf("破坏后方块 = %d，想要空气", got)
	}
	if got, want := finalChunk.Chest(0), (world.ChestSlot{Generation: 1}); got != want {
		t.Fatalf("破坏后箱子槽 = %+v，想要 %+v", got, want)
	}

	// 旧引用命令必须被拒绝：箱子已经不存在，两名玩家都已经不再查看它。
	sendIntegration(t, other.Endpoint, network.MoveContainerStack{
		Sequence: 99, Container: otherRef, From: 0, To: core.ChestFirstSlot,
	})
	waitIntegrationRejection(t, other, 99, network.RejectInvalidInput)
	sendIntegration(t, breaker.Endpoint, network.MoveContainerStack{
		Sequence: 99, Container: breakerRef, From: 0, To: core.ChestFirstSlot,
	})
	waitIntegrationRejection(t, breaker, 99, network.RejectInvalidInput)
}

// waitChestState 等待某个客户端收到满足条件的箱子状态。
func waitChestState(
	t *testing.T,
	connected integrationClient,
	accept func(network.ChestState) bool,
) network.ChestState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待箱子状态: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if state, ok := message.(network.ChestState); ok && accept(state) {
			return state
		}
	}
}

// waitChestStateWithClearedSlot 在同一遍消息扫描里等待满足 accept 的箱子状态，
// 并确认该客户端自己的完整物品状态里 hotbar slot 已清空；两个信号可能落在同一
// tick，分成两次独立的 Recv 循环会把先到的那条消息在另一次里丢掉。
func waitChestStateWithClearedSlot(
	t *testing.T,
	connected integrationClient,
	accept func(network.ChestState) bool,
	slot int,
) network.ChestState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	var state network.ChestState
	var chestSeen, slotCleared bool
	for !chestSeen || !slotCleared {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待箱子状态与物品状态: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		switch typed := message.(type) {
		case network.ChestState:
			if accept(typed) {
				state, chestSeen = typed, true
			}
		case network.InventoryState:
			if typed.Inventory.Hotbar.Slots[slot] == (core.ItemStack{}) {
				slotCleared = true
			}
		}
	}
	return state
}

// waitContainerClosed 等待某个客户端收到指定容器的关闭通知。
func waitContainerClosed(
	t *testing.T,
	connected integrationClient,
	want core.ContainerRef,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待关闭通知: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if closed, ok := message.(network.ContainerClosed); ok {
			if closed.Container != want {
				t.Fatalf("关闭通知引用 = %+v，想要 %+v", closed.Container, want)
			}
			return
		}
	}
}

// chestScriptHotbars 构造脚本所需的两份快捷栏：破坏者持有石镐与待存入的石头，
// 另一名玩家持有待存入的泥土。
func chestScriptHotbars() (breaker, other core.Hotbar) {
	fullDurability, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	breaker.Slots[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: fullDurability}
	breaker.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 5}
	other.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 3}
	return breaker, other
}

func TestChestSharedByTwoPlayersOverMemory(t *testing.T) {
	config := hostTestConfig()
	config.MaxPlayers = 2
	running := NewWorld(config, flatTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = running.RunTicks(ctx) }()

	breakerHotbar, otherHotbar := chestScriptHotbars()
	loc := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1.001, 0.5}}

	breakerClientEndpoint, breakerServerEndpoint := network.NewMemoryPair(4096)
	otherClientEndpoint, otherServerEndpoint := network.NewMemoryPair(4096)
	t.Cleanup(func() {
		_ = breakerClientEndpoint.Close()
		_ = otherClientEndpoint.Close()
	})
	if _, err := running.AttachSession(chestScriptSessionSpec(1, breakerServerEndpoint, loc, breakerHotbar)); err != nil {
		t.Fatalf("附加破坏者: %v", err)
	}
	if _, err := running.AttachSession(chestScriptSessionSpec(2, otherServerEndpoint, loc, otherHotbar)); err != nil {
		t.Fatalf("附加另一名玩家: %v", err)
	}

	breaker := integrationClient{Endpoint: breakerClientEndpoint, Mirror: client.NewMirror()}
	other := integrationClient{Endpoint: otherClientEndpoint, Mirror: client.NewMirror()}
	waitScriptClientReady(t, breaker)
	waitScriptClientReady(t, other)

	runChestSharedByTwoPlayersScript(t, running, breaker, other)
}

func TestChestSharedByTwoPlayersOverTCP(t *testing.T) {
	root := t.TempDir()
	breakerIdentity := integrationIdentity(0x93, "Breaker")
	otherIdentity := integrationIdentity(0x94, "Watcher")
	breakerHotbar, otherHotbar := chestScriptHotbars()
	loc := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1.001, 0.5}}

	seedIntegrationPlayer(t, root, breakerIdentity, sim.PlayerSnapshot{
		Current: loc, Inventory: core.Inventory{Hotbar: breakerHotbar},
	})
	seedIntegrationPlayer(t, root, otherIdentity, sim.PlayerSnapshot{
		Current: loc, Inventory: core.Inventory{Hotbar: otherHotbar},
	})

	host := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	breaker := dialIntegrationClient(t, host.Addr, breakerIdentity)
	other := dialIntegrationClient(t, host.Addr, otherIdentity)
	waitClientReadyFor(t, host, breaker, breakerIdentity.PlayerID)
	waitClientReadyFor(t, host, other, otherIdentity.PlayerID)

	runChestSharedByTwoPlayersScript(t, host.Host.world, breaker, other)

	if err := breaker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	host.Shutdown(t)
}

// waitScriptClientReady 等待 Memory 直连客户端收到 Ready，不依赖 TCP host 的
// 存档快照校验（Memory 变体没有磁盘身份可核对）。
func waitScriptClientReady(t *testing.T, connected integrationClient) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待 Ready: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if state, ok := message.(network.PlayerState); ok && state.Ready {
			return
		}
	}
}

func chestScriptSessionSpec(
	id sim.SessionID,
	endpoint network.ServerEndpoint,
	location sim.PlayerLocation,
	hotbar core.Hotbar,
) SessionSpec {
	return SessionSpec{
		ID: id, Generation: 1,
		PlayerID:    core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, byte(id)},
		DisplayName: fmt.Sprintf("ChestScript-%d", id),
		Endpoint:    endpoint,
		Restore: sim.PlayerRestore{
			Current: &location, Safe: &location, SpawnDimension: location.Dimension,
			Inventory: core.Inventory{Hotbar: hotbar},
		},
	}
}
