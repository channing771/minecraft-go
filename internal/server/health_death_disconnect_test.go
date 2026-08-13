package server

import (
	"context"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

// healthDeathFallY 是致死摔落的起跳高度。地面在 y = 1，落差因此约 29 格，
// 远超满血摔死所需的 23 格（伤害 = floor(落差) − 3 ≥ 20），死亡不依赖首 tick
// 重力积分的精确取值。
const healthDeathFallY = float32(30)

// deathDropStoneCount 是死亡玩家携带的石头数量，也是重连后应当恰好持有的数量。
const deathDropStoneCount = 10

// airSpawnGenerator 把出生锚点周围的出生候选区块全部生成成空气，因此死亡后的
// 重生永远找不到合法出生点，玩家稳定停在待重生状态。"死亡后、重生前"这段窗口
// 由此变成确定的测试条件，而不依赖 1 tick 的时序竞态。出生候选列的半径是 16 格，
// 锚点 (0,0) 的候选列因此落在 |X| ≤ 1、|Z| ≤ 1 的九个区块里。
type airSpawnGenerator struct{}

func (airSpawnGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	if position.X >= -1 && position.X <= 1 && position.Z >= -1 && position.Z <= 1 {
		return world.NewChunk(position)
	}
	return changedGenerator{}.GenerateChunk(position)
}

// TestDeathBeforeRespawnDisconnectDoesNotDuplicateInventory 覆盖
// "死亡后、重生前断线不得复制物品"：死亡当 tick 已经把 36 格掉进世界并清空了
// 权威背包，此时断线落盘的必须是清空后的背包。若这段待重生窗口取不到权威快照，
// 落盘的仍是死亡前的满背包，重连后一份物品就变成了两份——地上一份、背包一份。
func TestDeathBeforeRespawnDisconnectDoesNotDuplicateInventory(t *testing.T) {
	root := t.TempDir()
	identity := integrationIdentity(0x9d, "Doomed")
	// 玩家远离出生锚点登录：死亡掉落留在远处的死亡区块，重生只能走出生候选列，
	// 而出生候选列全是空气，因此重生永不完成。
	far := sim.PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2000.5, 1.001, 2000.5},
	}
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStone, Count: deathDropStoneCount,
	}
	seedIntegrationPlayer(t, root, identity, sim.PlayerSnapshot{
		Current: far, Safe: &far, Inventory: inventory,
	})

	first := startDiskHost(t, root, "127.0.0.1:0", airSpawnGenerator{})
	connected := dialIntegrationClient(t, first.Addr, identity)
	waitClientReadyFor(t, first, connected, identity.PlayerID)

	first.Host.world.SetPlayerPositionForTest(
		first.SessionFor(t, identity.PlayerID),
		mgl32.Vec3{2000.5, healthDeathFallY, 2000.5},
	)
	// 死亡当 tick 玩家转入待重生，而空气出生区让它再也无法重生，窗口就此稳定张开。
	waitHealth(t, connected, func(state network.PlayerState) bool {
		return !state.Ready
	})

	if err := connected.Close(); err != nil {
		t.Fatalf("关闭连接: %v", err)
	}
	first.WaitPlayerSaved(t, identity.PlayerID)
	first.Shutdown(t)

	stored := loadStoredPlayerForTest(t, root, identity.PlayerID)
	if got := integrationItemCount(stored.Inventory, core.ItemStone); got != 0 {
		t.Fatalf(
			"死亡后断线落盘的背包里仍有 %d 个石头，想要 0：死亡掉落被复制了一份",
			got,
		)
	}
	if stored.Health != core.MaxHealth {
		t.Fatalf("死亡后断线落盘的生命值 = %d，想要满血 %d",
			stored.Health, core.MaxHealth)
	}

	// 重连：玩家回到安全点（也就是死亡落地点）并把地上的死亡掉落拾回，
	// 整条时间线上持有的石头必须恰好是原来的 10 个。
	second := startDiskHost(t, root, "127.0.0.1:0", airSpawnGenerator{})
	reconnected := dialIntegrationClient(t, second.Addr, identity)
	waitClientReadyFor(t, second, reconnected, identity.PlayerID)
	session := second.SessionFor(t, identity.PlayerID)
	duplicated := 0
	waitIntegrationCondition(t, "重连后拾回死亡掉落", func() bool {
		snapshot, ok := second.Host.world.PlayerSnapshotFor(session)
		if !ok {
			return false
		}
		count := integrationItemCount(snapshot.Inventory, core.ItemStone)
		if count > deathDropStoneCount {
			duplicated = count
			return true
		}
		return count == deathDropStoneCount
	})
	if duplicated != 0 {
		t.Fatalf("重连后持有 %d 个石头，想要至多 %d：死亡掉落被复制",
			duplicated, deathDropStoneCount)
	}
	if err := reconnected.Close(); err != nil {
		t.Fatal(err)
	}
	second.Shutdown(t)
}

// loadStoredPlayerForTest 在世界关闭后重新打开磁盘存档读取一名玩家的落盘状态。
func loadStoredPlayerForTest(
	t *testing.T,
	root string,
	id core.PlayerID,
) storage.StoredPlayer {
	t.Helper()
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{})
	if err != nil {
		t.Fatalf("重新打开磁盘存档: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("关闭检视用磁盘存档: %v", err)
		}
	}()
	stored, err := store.LoadPlayer(context.Background(), id)
	if err != nil {
		t.Fatalf("读取玩家 %s 的落盘状态: %v", id, err)
	}
	return stored
}
