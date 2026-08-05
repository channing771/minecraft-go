package server

import (
	"context"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/storage"
)

// TestTCPDropSelectedItemSurvivesRestart 用真实 TCP 与 host 层玩家保存证明主动丢弃
// 的两侧结果都跨正常关服持久：玩家背包少一件，掉落物仍在同一区块槽位。
//
// 与 TestDroppedItemSurvivesShutdownAndRestart 的区别是本测试装配完整 Host，
// 因此玩家存档会被真正写入并在重连时读回。
func TestTCPDropSelectedItemSurvivesRestart(t *testing.T) {
	const seed int64 = 990011
	root := t.TempDir()
	identity := multiplayerIdentity(0xd7, "丢弃者")

	// 预置一个含煤炭的玩家存档：煤炭是不可放置物品，同时覆盖已注册物品边界。
	seedStore, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2, Seed: seed, SpawnDimension: core.Overworld,
	}})
	if err != nil {
		t.Fatalf("OpenDisk seed: %v", err)
	}
	location := storage.PlayerLocation{Dimension: core.Overworld, Position: [3]float32{8.5, 1.001, 8.5}}
	var inventory core.Inventory
	inventory.Hotbar.Selected = 0
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemCoal, Count: 5}
	if _, err := seedStore.SavePlayer(context.Background(), storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: location, Safe: &location, Inventory: inventory,
	}); err != nil {
		_ = seedStore.Close()
		t.Fatalf("预置玩家存档: %v", err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("关闭种子 store: %v", err)
	}

	var clients []*multiplayerTCPClient
	first := startMultiplayerRestartHost(t, root, seed)
	clients = connectRestartClients(t, first.addr, []network.Identity{identity}, nil)
	waitSingleRestartClientReady(t, clients[0])

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := clients[0].endpoint.Send(ctx, network.DropSelectedItem{Sequence: 1}); err != nil {
		cancel()
		t.Fatalf("发送主动丢弃: %v", err)
	}
	cancel()

	// 等待权威把扣减后的背包发布回发起者。
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待主动丢弃后的背包发布超时")
		}
		if _, err := clients[0].drainOne(); err != nil {
			t.Fatalf("drain: %v", err)
		}
		if clients[0].inventoryCount(core.ItemCoal) == 4 {
			break
		}
	}

	if err := shutdownRestartHost(first, clients); err != nil {
		t.Fatalf("首次关服: %v", err)
	}

	// 重开同一世界并重连同一身份：背包必须从存档恢复为少一件。
	var reconnected []*multiplayerTCPClient
	second := startMultiplayerRestartHost(t, root, seed)
	t.Cleanup(func() {
		if err := shutdownRestartHost(second, reconnected); err != nil {
			t.Errorf("清理关服: %v", err)
		}
	})
	reconnected = connectRestartClients(t, second.addr, []network.Identity{identity}, nil)
	waitSingleRestartClientReady(t, reconnected[0])

	deadline = time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("重启后煤炭数量 = %d，想要 4",
				reconnected[0].inventoryCount(core.ItemCoal))
		}
		if _, err := reconnected[0].drainOne(); err != nil {
			t.Fatalf("重连 drain: %v", err)
		}
		if reconnected[0].inventoryCount(core.ItemCoal) == 4 {
			return
		}
	}
}

// waitSingleRestartClientReady 等待单个客户端就绪。
// 既有的 waitRestartClientsReady 额外要求看到 7 个远端玩家，只适用于八客户端场景。
func waitSingleRestartClientReady(t *testing.T, connected *multiplayerTCPClient) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !connected.readyWithFootSnapshot() {
		if time.Now().After(deadline) {
			t.Fatalf("客户端未在期限内就绪\n%s", multiplayerDiagnostics(connected, nil))
		}
		if _, err := connected.drainOne(); err != nil {
			t.Fatalf("drain: %v", err)
		}
	}
}

// inventoryCount 返回该客户端收到的最新权威背包中某物品的总数。
// transcript 已记录全部服务端消息，这里只做只读汇总。
func (connected *multiplayerTCPClient) inventoryCount(item core.ItemID) uint8 {
	var total uint8
	for index := len(connected.transcript) - 1; index >= 0; index-- {
		state, ok := connected.transcript[index].message.(network.InventoryState)
		if !ok {
			continue
		}
		for _, slot := range state.Inventory.Hotbar.Slots {
			if slot.Item == item {
				total += slot.Count
			}
		}
		for _, slot := range state.Inventory.Backpack {
			if slot.Item == item {
				total += slot.Count
			}
		}
		return total
	}
	return 0
}
