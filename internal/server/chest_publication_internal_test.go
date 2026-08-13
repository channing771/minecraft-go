package server

import (
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

// TestChestPublicationOutboxFullClosesOnlySlowSession 证明箱子状态与关闭通知
// 复用与其余发布路径相同的 outbox-满即关闭规则：慢 session 的 outbox 撑满时
// server 只关闭它自己，健康 session 仍然按顺序收到自己的箱子状态与关闭通知。
func TestChestPublicationOutboxFullClosesOnlySlowSession(t *testing.T) {
	config := registryTestConfig()
	config.OutboxCapacity = 1
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	slowEndpoint := newBlockingServerEndpoint()
	healthyEndpoint := newHeartbeatEndpoint()
	slowExit, err := running.AttachSession(registrySessionSpec(7, 1, slowEndpoint))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := running.AttachSession(registrySessionSpec(8, 1, healthyEndpoint)); err != nil {
		t.Fatal(err)
	}

	chestRef := core.ContainerRef{
		Dimension: core.Overworld, Kind: core.ContainerKindChest, Slot: 0, Generation: 1,
	}
	publishChest := func() {
		running.stepMu.Lock()
		defer running.stepMu.Unlock()
		running.publish(sim.TickResult{Chests: []sim.ChestUpdate{
			{Session: 7, Chest: chestRef},
			{Session: 8, Chest: chestRef},
		}})
	}
	publishChest()
	select {
	case <-slowEndpoint.sendStarted:
	case <-time.After(waitDeadline):
		t.Fatal("slow writer 没有阻塞")
	}
	if message, ok := healthyEndpoint.nextSent(t).(network.ChestState); !ok || message.Chest != chestRef {
		t.Fatalf("healthy 箱子状态 = %#v", message)
	}

	publishClose := func() {
		running.stepMu.Lock()
		defer running.stepMu.Unlock()
		running.publish(sim.TickResult{FurnaceEnds: []sim.FurnaceEnd{
			{Session: 7, Furnace: chestRef},
			{Session: 8, Furnace: chestRef},
		}})
	}
	// outbox 容量为 1：第一条消息已被慢 writer 取走并阻塞在 Send 上，
	// 第二条占满剩余缓冲区，第三条才会真正溢出并触发关闭。
	publishClose()
	if message, ok := healthyEndpoint.nextSent(t).(network.ContainerClosed); !ok || message.Container != chestRef {
		t.Fatalf("healthy 关闭通知 = %#v", message)
	}
	publishChest()
	if message, ok := healthyEndpoint.nextSent(t).(network.ChestState); !ok || message.Chest != chestRef {
		t.Fatalf("healthy 第三次箱子状态 = %#v", message)
	}

	if got := waitSessionExit(t, slowExit); got.ID != 7 || got.Err == nil {
		t.Fatalf("slow exit = %+v", got)
	}
	if _, ok := running.PlayerStateFor(8); !ok {
		t.Fatal("slow session 关闭了健康 session")
	}
}
