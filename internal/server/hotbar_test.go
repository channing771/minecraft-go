package server_test

import (
	"context"
	"math"
	"testing"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
)

func TestHotbarStateReachesOwningSessionBeforeReady(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	running := newMemoryAttachedWorldForExternalTest(
		hotbarTestConfig(1), serverEndpoint, server.FlatTestGenerator{},
	)
	shutdownHotbarServer(t, running, clientEndpoint)
	mirror := client.NewMirror()

	var kinds []string
	stepUntilCollect(t, running, clientEndpoint, mirror, func(message network.ServerMessage) {
		switch message := message.(type) {
		case network.HotbarState:
			kinds = append(kinds, "hotbar")
		case network.PlayerState:
			if message.Ready {
				kinds = append(kinds, "ready")
			}
		}
	}, func() bool {
		player, ok := playerStateForExternalTest(running)
		return ok && player.Ready
	})

	hotbars, readyIndex, hotbarIndex := 0, -1, -1
	for index, kind := range kinds {
		switch kind {
		case "hotbar":
			hotbars++
			if hotbarIndex < 0 {
				hotbarIndex = index
			}
		case "ready":
			if readyIndex < 0 {
				readyIndex = index
			}
		}
	}
	if hotbars != 1 {
		t.Fatalf("登录期间 HotbarState 数量 = %d，想要恰好一份", hotbars)
	}
	if hotbarIndex < 0 || readyIndex < 0 || hotbarIndex > readyIndex {
		t.Fatalf("消息顺序 = %v，快捷栏必须先于 Ready 玩家状态", kinds)
	}
}

func TestHotbarStateStaysWithOwningSession(t *testing.T) {
	firstClient, firstServer := network.NewMemoryPair(256)
	secondClient, secondServer := network.NewMemoryPair(256)
	running := newMemoryAttachedWorldForExternalTest(
		hotbarTestConfig(2), firstServer, server.FlatTestGenerator{},
	)
	if _, err := running.AttachSession(externalSessionSpec(2, 1, secondServer, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
	})); err != nil {
		t.Fatalf("附加第二个会话: %v", err)
	}

	shutdownHotbarServer(t, running, firstClient, secondClient)
	firstMirror := &client.HotbarMirror{}
	secondMirror := &client.HotbarMirror{}
	firstReady, secondReady := false, false
	deadline := time.Now().Add(5 * time.Second)
	broken := false
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待两名玩家快捷栏同步超时")
		}
		result := running.StepForTest()
		firstStates := hotbarDrainTick(t, firstClient, result.Tick, firstMirror, &firstReady)
		secondStates := hotbarDrainTick(t, secondClient, result.Tick, secondMirror, &secondReady)
		if broken {
			if len(secondStates) != 0 {
				t.Fatalf("玩家乙收到了不属于自己的快捷栏更新: %+v", secondStates)
			}
			if len(firstStates) == 0 {
				continue
			}
			last := firstStates[len(firstStates)-1]
			if last.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemGrass, Count: 1}) {
				t.Fatalf("玩家甲快捷栏 = %+v，想要 1 个草", last.Hotbar)
			}
			return
		}
		if firstReady && secondReady {
			sendClientMessage(t, firstClient, network.BreakBlock{
				Sequence: 1, Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
			})
			broken = true
		}
	}
}

// shutdownHotbarServer 在测试结束时关闭服务端并释放会话 goroutine。
func shutdownHotbarServer(
	t *testing.T,
	running *server.Server,
	endpoints ...network.ClientEndpoint,
) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := running.Shutdown(ctx); err != nil {
			t.Errorf("Server.Shutdown: %v", err)
		}
		for _, endpoint := range endpoints {
			_ = endpoint.Close()
		}
	})
}

func hotbarTestConfig(maxPlayers int) server.Config {
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.SnapshotChunks = 16
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 256
	config.MaxPlayers = maxPlayers
	return config
}

// hotbarDrainTick 读取一个会话在本 tick 的全部消息，并返回其中的快捷栏状态。
func hotbarDrainTick(
	t *testing.T,
	endpoint network.ClientEndpoint,
	throughTick uint64,
	mirror *client.HotbarMirror,
	ready *bool,
) []network.HotbarState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var states []network.HotbarState
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		switch message := message.(type) {
		case network.HotbarState:
			if err := mirror.Apply(message); err != nil {
				t.Fatalf("HotbarMirror.Apply: %v", err)
			}
			states = append(states, message)
		case network.CommandRejected:
			t.Fatalf("权威命令被拒绝: %+v", message)
		case network.PlayerState:
			*ready = message.Ready
			if message.ServerTick == throughTick {
				return states
			}
			if message.ServerTick > throughTick {
				t.Fatalf("PlayerState tick=%d，跳过目标 tick=%d", message.ServerTick, throughTick)
			}
		}
	}
}
