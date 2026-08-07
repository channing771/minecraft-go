package server_test

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
)

// healthLethalFallHeight 是从满血摔死所需的落差：伤害 = floor(落差) − 3 = 20。
const healthLethalFallHeight = float32(23)

// healthTick 是一名玩家在某一 tick 收到的权威结果：本人的玩家状态与该 tick
// 发布给它的掉落物差分。
type healthTick struct {
	tick  uint64
	state network.PlayerState
	drops []network.ItemDrop
}

// TestDeathTickPublishesFullHealthAndDropDeltas 覆盖"生命值为零的状态不对外发布"：
// 致死摔落当 tick 发布的生命值必须是重生后的 20，掉落物差分必须在同一 tick 发布
// 给订阅了死亡区块的会话；整条时间线上不得出现生命值为 0 的玩家状态。
//
// 这条测试同时是"死亡结算必须早于状态发布"的护栏：把 settleDeaths 移到
// publishPlayers 之后，死亡当 tick 就会发布出生命值 0。
func TestDeathTickPublishesFullHealthAndDropDeltas(t *testing.T) {
	running, clientEndpoint := newDropPublicationWorld(t)
	ready := stepUntilHealthReady(t, running, clientEndpoint)
	if ready.state.Health != core.MaxHealth {
		t.Fatalf("出生生命值 = %d，想要满血 %d", ready.state.Health, core.MaxHealth)
	}

	running.SetPlayerInventoryForTest(1, func(inventory core.Inventory) core.Inventory {
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 10}
		return inventory
	})
	running.SetPlayerPositionForTest(1, mgl32.Vec3{0.5, 1 + healthLethalFallHeight, 0.5})

	var death healthTick
	wasReady := true
	for range 400 {
		got := stepHealthTick(t, running, clientEndpoint)
		if got.state.Health == 0 {
			t.Fatalf("tick %d 发布了生命值 0 的玩家状态：%+v", got.tick, got.state)
		}
		if wasReady && !got.state.Ready {
			death = got
			break
		}
		wasReady = got.state.Ready
	}
	if death.tick == 0 {
		t.Fatal("玩家未在 400 tick 内因致死摔落而死亡")
	}
	if death.state.Health != core.MaxHealth {
		t.Fatalf("死亡当 tick 发布生命值 = %d，想要重生后的 %d",
			death.state.Health, core.MaxHealth)
	}

	// 死亡掉落的差分必须发布给订阅了死亡区块的会话。死者本人的兴趣集合在重生
	// 当 tick 才重建，因此差分最迟落在死亡后的下一个 tick，而不是同一 tick。
	drops := death.drops
	respawned := false
	for range 100 {
		got := stepHealthTick(t, running, clientEndpoint)
		if got.state.Health == 0 {
			t.Fatalf("tick %d 发布了生命值 0 的玩家状态：%+v", got.tick, got.state)
		}
		drops = append(drops, got.drops...)
		if !got.state.Ready {
			continue
		}
		if got.state.Health != core.MaxHealth {
			t.Fatalf("重生生命值 = %d，想要满血 %d", got.state.Health, core.MaxHealth)
		}
		respawned = true
		break
	}
	if !respawned {
		t.Fatal("玩家未在 100 tick 内重生")
	}
	if len(drops) != 1 ||
		drops[0].Item != core.ItemStone || drops[0].Count != 10 {
		t.Fatalf("死亡掉落物差分 = %+v，想要一堆 10 个石头", drops)
	}
}

// TestDeathNeverPersistsZeroHealth 覆盖"生命值 0 不可能被持久化"：
// 死亡在同一 tick 内结算并回满，快照又只在玩家 Active 时产出，
// 因此整条致死时间线上任何一份权威玩家快照的生命值都不得为 0。
func TestDeathNeverPersistsZeroHealth(t *testing.T) {
	running, clientEndpoint := newDropPublicationWorld(t)
	stepUntilHealthReady(t, running, clientEndpoint)
	running.SetPlayerPositionForTest(1, mgl32.Vec3{0.5, 1 + healthLethalFallHeight, 0.5})

	died := false
	for range 500 {
		got := stepHealthTick(t, running, clientEndpoint)
		snapshot, ok := running.PlayerSnapshotFor(1)
		if ok && snapshot.Health == 0 {
			t.Fatalf("tick %d 的权威玩家快照生命值为 0：%+v", got.tick, snapshot)
		}
		if !got.state.Ready {
			died = true
			continue
		}
		if died {
			return
		}
	}
	t.Fatal("玩家未在 500 tick 内完成死亡与重生")
}

// stepUntilHealthReady 推进到玩家 Ready，返回 Ready 当 tick 的结果。
func stepUntilHealthReady(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
) healthTick {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got := stepHealthTick(t, running, endpoint)
		if got.state.Ready {
			return got
		}
	}
	t.Fatal("等待玩家 Ready 超时")
	return healthTick{}
}

// stepHealthTick 推进一个权威 tick 并读完该会话在本 tick 收到的全部消息。
func stepHealthTick(
	t *testing.T,
	running *server.Server,
	endpoint network.ClientEndpoint,
) healthTick {
	t.Helper()
	result := running.StepForTest()
	return drainHealthTick(t, endpoint, result)
}

func drainHealthTick(
	t *testing.T,
	endpoint network.ClientEndpoint,
	result sim.TickResult,
) healthTick {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := healthTick{tick: result.Tick}
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		switch message := message.(type) {
		case network.ItemDropUpserts:
			got.drops = append(got.drops, message.Drops...)
		case network.PlayerState:
			if message.ServerTick > result.Tick {
				t.Fatalf("PlayerState tick=%d，跳过目标 tick=%d",
					message.ServerTick, result.Tick)
			}
			if message.ServerTick == result.Tick {
				got.state = message
				return got
			}
		}
	}
}
