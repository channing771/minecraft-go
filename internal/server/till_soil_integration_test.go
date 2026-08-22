package server

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

// tillSoilParityResult 是翻地脚本跑完后的全部可观察结果：权威背包、客户端
// 镜像里的目标方块，以及第二次（重复）翻地被拒的完整拒绝消息。
type tillSoilParityResult struct {
	Inventory core.Inventory
	Block     core.BlockID
	Rejection network.CommandRejected
}

// tillSoilLookDown 是近乎垂直向下的俯角，正好落在 validPlayerLook 允许的
// ±(π/2 − 0.01) 之内；玩家脚下那一格因此就是权威射线的目标。
const tillSoilLookDown = -float32(math.Pi)/2 + 0.01

// TestTillSoilMemoryTCPParity 覆盖任务 4.5 的传输一致性要求：同一条翻地脚本
// 在 Memory 与 TCP 两种传输下必须得到逐字段相同的结果。
//
// 脚本刻意同时走成功与拒绝两条路：第一次翻地把脚下的草变成耕地并扣一点耐久，
// 第二次对已经是耕地的同一格再翻一次必须被拒且**一点耐久都不掉**。只跑成功
// 那一半的话，一个在拒绝路径上也扣耐久的实现照样能让两种传输一致。
func TestTillSoilMemoryTCPParity(t *testing.T) {
	memory := runTillSoilParityScript(t, "memory")
	tcp := runTillSoilParityScript(t, "tcp")
	if memory != tcp {
		t.Fatalf("翻地 Memory/TCP 未收敛\nmemory=%+v\ntcp=%+v", memory, tcp)
	}

	full, _ := core.ItemMaxDurability(core.ItemStoneHoe)
	wantHoe := core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: full - 1}
	if got := memory.Inventory.Hotbar.Slots[0]; got != wantHoe {
		t.Fatalf("两次翻地后的锄头 = %+v，想要恰好扣一点的 %+v（第二次被拒不得磨损）",
			got, wantHoe)
	}
	if memory.Block != core.FarmlandDryID {
		t.Fatalf("客户端镜像里的目标格 = %d，想要干耕地 %d", memory.Block, core.FarmlandDryID)
	}
	want := network.CommandRejected{Sequence: 2, Reason: network.RejectInvalidBlock}
	if memory.Rejection != want {
		t.Fatalf("重复翻地的拒绝 = %+v，想要 %+v", memory.Rejection, want)
	}
}

func runTillSoilParityScript(t *testing.T, transport string) tillSoilParityResult {
	t.Helper()
	identity := integrationIdentity(0x9c, "Tiller")
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})
	full, _ := core.ItemMaxDurability(core.ItemStoneHoe)
	var initial core.Inventory
	initial.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: full}
	location := storage.PlayerLocation{
		Dimension: core.Overworld,
		Position:  [3]float32{0.5, 1.001, 0.5},
	}
	if _, err := store.SavePlayer(context.Background(), wellFedPlayerSave(storage.PlayerSave{
		PlayerID: identity.PlayerID, Revision: 1, DisplayName: identity.DisplayName,
		Current: location, Safe: &location, Inventory: initial,
	})); err != nil {
		t.Fatal(err)
	}

	config := hostTestConfig()
	config.ViewRadius = 1
	config.AutosaveTicks = 1000
	host := mustNewHost(t, config, flatGenerator{}, store)
	endpoint, _, closeTransport := openParityTransport(t, host, transport, identity)
	t.Cleanup(func() {
		_ = endpoint.Close()
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		_ = host.Shutdown(ctx)
		closeTransport()
	})

	mirror := client.NewMirror()
	var rejection network.CommandRejected
	step := func() {
		t.Helper()
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			if rejected, ok := message.(network.CommandRejected); ok {
				rejection = rejected
			}
		}
	}
	send := func(command network.ClientMessage) {
		t.Helper()
		sendIntegration(t, endpoint, command)
		waitIntegrationCondition(t, fmt.Sprintf("%s till %T queued", transport, command), func() bool {
			return len(host.world.incoming) > 0
		})
		step()
	}

	ready, inventoryReady := false, false
	for !ready || !inventoryReady || !parityViewLoaded(mirror) {
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			switch message := message.(type) {
			case network.PlayerState:
				ready = ready || message.Ready
			case network.InventoryState:
				inventoryReady = inventoryReady || message.Inventory == initial
			}
		}
	}

	// 第一次翻地：脚下那格草必须变成耕地并扣一点耐久。
	send(network.TillSoil{Sequence: 1, Pitch: tillSoilLookDown})
	if rejection != (network.CommandRejected{}) {
		t.Fatalf("%s 首次翻地被拒绝: %+v", transport, rejection)
	}
	// 变更与背包发布可能落在紧随其后的 tick；多推几个 tick 让镜像收敛。
	for range 3 {
		step()
	}
	// 第二次翻地：目标已是耕地，必须被拒且不磨损。
	send(network.TillSoil{Sequence: 2, Pitch: tillSoilLookDown})
	for range 3 {
		step()
	}

	block, loaded := mirror.BlockAt(core.Overworld, core.BlockPos{})
	if !loaded {
		t.Fatalf("%s 目标格没有进入客户端镜像", transport)
	}
	host.mu.Lock()
	active := *host.activeByPlayer[identity.PlayerID]
	host.mu.Unlock()
	snapshot, ok := host.world.PlayerSnapshotFor(active.Session)
	if !ok {
		t.Fatalf("%s 没有权威玩家快照", transport)
	}
	return tillSoilParityResult{
		Inventory: snapshot.Inventory,
		Block:     block,
		Rejection: rejection,
	}
}
