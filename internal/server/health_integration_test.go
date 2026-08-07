package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

const (
	// healthScriptHurtFallHeight 是非致死落差：伤害 = floor(10) − 3 = 7。
	healthScriptHurtFallHeight = float32(10)
	// healthScriptLethalFallHeight 是从满血摔死的落差：伤害 = floor(23) − 3 = 20。
	healthScriptLethalFallHeight = float32(23)
	// healthScriptHurtHealth 是受伤后的权威生命值。
	healthScriptHurtHealth = core.MaxHealth - 7
	// healthScriptDeathColumn 是死亡与拾取发生的水平列，与出生列 (0.5, 0.5)
	// 相距远超拾取半径；掉落物落在方块 (10, 1, 10)，其中心距守候玩家不足一格，
	// 因此拾取延迟结束后必然被守候玩家拾取，而不会被重生回出生点的死者拿回。
	healthScriptDeathColumn = float32(10.5)
)

// healthScriptDrop 是脚本里唯一的一堆物品：摔死后掉进世界、再被另一名玩家拾取。
var healthScriptDrop = core.ItemStack{Item: core.ItemStone, Count: 9}

// runHealthFallDeathAndPickupScript 是 Memory 与 TCP 共用的生命值纵向脚本：
// 摔落受伤 -> 等待自动回复 -> 再次致死摔落 -> 背包掉在世界里 ->
// 玩家回到出生点满血 -> 另一名玩家拾取这些掉落物。
// 两种 transport 唯一的差异在调用方如何搭建 world/连接，脚本本身逐条复用，
// 因此最终区块、玩家状态与掉落物分布的断言对两条链路完全相同。
func runHealthFallDeathAndPickupScript(
	t *testing.T,
	running *Server,
	faller, other integrationClient,
	fallerSession, otherSession sim.SessionID,
) {
	t.Helper()
	key := core.ChunkKey{Dimension: core.Overworld}

	// 守候玩家先站到死亡列上：它全程不动，只负责证明掉落物可被另一名玩家拾取。
	running.SetPlayerPositionForTest(otherSession, mgl32.Vec3{
		healthScriptDeathColumn, 1.001, healthScriptDeathColumn,
	})

	// 非致死摔落：落差 10 扣 7 点，权威生命值必须随本人的玩家状态下发。
	// 摔落峰值只在"上一 tick 还站在地面"时取瞬移后的高度，因此必须先等摔落者
	// 在出生列站稳，落差才是确定的 10 而不是首个空中 tick 的高度。
	waitHealth(t, faller, healthScriptGrounded(mgl32.Vec3{0.5, 1, 0.5}))
	running.SetPlayerPositionForTest(fallerSession, mgl32.Vec3{
		healthScriptDeathColumn,
		1 + healthScriptHurtFallHeight,
		healthScriptDeathColumn,
	})
	waitHealth(t, faller, func(state network.PlayerState) bool {
		return state.Ready && state.Health == healthScriptHurtHealth
	})

	// 等待自动回复：最后一次受伤后 RegenDelayTicks + RegenIntervalTicks 个 tick
	// 恢复第一点生命值。
	waitHealth(t, faller, func(state network.PlayerState) bool {
		return state.Health == healthScriptHurtHealth+1
	})

	// 致死摔落：伤害在同一 tick 内结算死亡，因此外部只能观察到重生后的满血。
	waitHealth(t, faller, healthScriptGrounded(mgl32.Vec3{
		healthScriptDeathColumn, 1, healthScriptDeathColumn,
	}))
	running.SetPlayerPositionForTest(fallerSession, mgl32.Vec3{
		healthScriptDeathColumn,
		1 + healthScriptLethalFallHeight,
		healthScriptDeathColumn,
	})
	_, dying := waitHealth(t, faller, func(state network.PlayerState) bool {
		return !state.Ready
	})
	_, respawning := waitHealth(t, faller, func(state network.PlayerState) bool {
		return state.Ready && state.Position == (mgl32.Vec3{0.5, 1, 0.5})
	})

	// 背包必须掉在世界里：死亡结算发布的掉落物差分正好是摔落者身上的那一堆。
	dropped := append(dying, respawning...)
	if len(dropped) != 1 || dropped[0].Item != healthScriptDrop.Item ||
		dropped[0].Count != healthScriptDrop.Count {
		t.Fatalf("死亡掉落物差分 = %+v，想要 %+v", dropped, healthScriptDrop)
	}

	// 另一名玩家守在死亡列上，拾取延迟结束后必须收到这堆物品。
	waitIntegrationState(t, other, func(message network.ServerMessage) bool {
		state, ok := message.(network.InventoryState)
		return ok && state.Inventory.Hotbar.Slots[0] == healthScriptDrop
	})

	// 最终区块：掉落物已被拾走，死亡区块不再有活动掉落槽。
	finalChunk, _, ok := running.CloneReadyChunkForTest(key)
	if !ok {
		t.Fatal("死亡区块不可用")
	}
	for slot := range core.DropsPerChunk {
		if drop := finalChunk.Drop(slot); drop.Active {
			t.Fatalf("拾取后仍有活动掉落物槽 %d: %+v", slot, drop)
		}
	}

	// 最终玩家状态：死者回到出生锚点满血，守候者原地未受伤。
	assertScriptSnapshot(t, running, fallerSession, mgl32.Vec3{0.5, 1, 0.5}, core.Inventory{})
	var wantOther core.Inventory
	wantOther.Hotbar.Slots[0] = healthScriptDrop
	assertScriptSnapshot(t, running, otherSession, mgl32.Vec3{
		healthScriptDeathColumn, 1, healthScriptDeathColumn,
	}, wantOther)
}

// healthScriptGrounded 匹配"玩家已在指定位置站稳"的权威玩家状态。
func healthScriptGrounded(position mgl32.Vec3) func(network.PlayerState) bool {
	return func(state network.PlayerState) bool {
		return state.Ready && state.OnGround && state.Position == position
	}
}

// assertScriptSnapshot 断言某个会话的权威快照：位置、生命值满血、物品状态。
func assertScriptSnapshot(
	t *testing.T,
	running *Server,
	session sim.SessionID,
	position mgl32.Vec3,
	inventory core.Inventory,
) {
	t.Helper()
	snapshot, ok := running.PlayerSnapshotFor(session)
	if !ok {
		t.Fatalf("会话 %d 没有权威快照", session)
	}
	if snapshot.Health != core.MaxHealth {
		t.Fatalf("会话 %d 最终生命值 = %d，想要满血 %d",
			session, snapshot.Health, core.MaxHealth)
	}
	if snapshot.Current.Position != position {
		t.Fatalf("会话 %d 最终位置 = %+v，想要 %+v",
			session, snapshot.Current.Position, position)
	}
	if snapshot.Inventory != inventory {
		t.Fatalf("会话 %d 最终物品状态 = %+v，想要 %+v",
			session, snapshot.Inventory, inventory)
	}
}

// waitHealth 等待某个客户端收到满足条件的权威玩家状态，返回该状态与本次扫描
// 期间收到的全部掉落物差分，并在扫描过程中顺带钉住"生命值 0 永不外发"：
// 任何一条玩家状态携带 0 都直接判失败。
// 回复一次需要 RegenDelayTicks + RegenIntervalTicks 个 tick，因此超时留得比
// 其它纵向等待更宽。
func waitHealth(
	t *testing.T,
	connected integrationClient,
	accept func(network.PlayerState) bool,
) (network.PlayerState, []network.ItemDrop) {
	t.Helper()
	var drops []network.ItemDrop
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		message, err := connected.Endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("等待玩家状态: %v", err)
		}
		applyIntegrationMessage(t, connected.Mirror, message)
		if upserts, ok := message.(network.ItemDropUpserts); ok {
			drops = append(drops, upserts.Drops...)
		}
		state, ok := message.(network.PlayerState)
		if !ok {
			continue
		}
		if state.Health == 0 {
			t.Fatalf("服务端发布了生命值 0 的玩家状态: %+v", state)
		}
		if accept(state) {
			return state, drops
		}
	}
}

func TestHealthFallDeathAndPickupOverMemory(t *testing.T) {
	config := hostTestConfig()
	config.MaxPlayers = 2
	running := NewWorld(config, flatTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = running.RunTicks(ctx) }()

	loc := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1.001, 0.5}}
	var fallerHotbar core.Hotbar
	fallerHotbar.Slots[0] = healthScriptDrop

	fallerClientEndpoint, fallerServerEndpoint := network.NewMemoryPair(4096)
	otherClientEndpoint, otherServerEndpoint := network.NewMemoryPair(4096)
	t.Cleanup(func() {
		_ = fallerClientEndpoint.Close()
		_ = otherClientEndpoint.Close()
	})
	if _, err := running.AttachSession(
		healthScriptSessionSpec(1, fallerServerEndpoint, loc, fallerHotbar),
	); err != nil {
		t.Fatalf("附加摔落者: %v", err)
	}
	if _, err := running.AttachSession(
		healthScriptSessionSpec(2, otherServerEndpoint, loc, core.Hotbar{}),
	); err != nil {
		t.Fatalf("附加守候者: %v", err)
	}

	faller := integrationClient{Endpoint: fallerClientEndpoint, Mirror: client.NewMirror()}
	other := integrationClient{Endpoint: otherClientEndpoint, Mirror: client.NewMirror()}
	waitScriptClientReady(t, faller)
	waitScriptClientReady(t, other)

	runHealthFallDeathAndPickupScript(t, running, faller, other, 1, 2)
}

func TestHealthFallDeathAndPickupOverTCP(t *testing.T) {
	root := t.TempDir()
	fallerIdentity := integrationIdentity(0x95, "Faller")
	otherIdentity := integrationIdentity(0x96, "Keeper")
	loc := sim.PlayerLocation{Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 1.001, 0.5}}
	var fallerHotbar core.Hotbar
	fallerHotbar.Slots[0] = healthScriptDrop

	seedIntegrationPlayer(t, root, fallerIdentity, sim.PlayerSnapshot{
		Current: loc, Inventory: core.Inventory{Hotbar: fallerHotbar},
	})
	seedIntegrationPlayer(t, root, otherIdentity, sim.PlayerSnapshot{Current: loc})

	host := startDiskHost(t, root, "127.0.0.1:0", changedGenerator{})
	faller := dialIntegrationClient(t, host.Addr, fallerIdentity)
	other := dialIntegrationClient(t, host.Addr, otherIdentity)
	waitClientReadyFor(t, host, faller, fallerIdentity.PlayerID)
	waitClientReadyFor(t, host, other, otherIdentity.PlayerID)

	runHealthFallDeathAndPickupScript(
		t, host.Host.world, faller, other,
		host.SessionFor(t, fallerIdentity.PlayerID),
		host.SessionFor(t, otherIdentity.PlayerID),
	)

	if err := faller.Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	host.Shutdown(t)
}

func healthScriptSessionSpec(
	id sim.SessionID,
	endpoint network.ServerEndpoint,
	location sim.PlayerLocation,
	hotbar core.Hotbar,
) SessionSpec {
	return SessionSpec{
		ID: id, Generation: 1,
		PlayerID:    core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, byte(id)},
		DisplayName: fmt.Sprintf("HealthScript-%d", id),
		Endpoint:    endpoint,
		Restore: sim.PlayerRestore{
			Current: &location, Safe: &location, SpawnDimension: location.Dimension,
			Inventory: core.Inventory{Hotbar: hotbar},
		},
	}
}
