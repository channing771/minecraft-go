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
	"minecraft-go/internal/storage"
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
		case network.InventoryState:
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
	firstMirror := &client.InventoryMirror{}
	secondMirror := &client.InventoryMirror{}
	firstReady, secondReady := false, false
	wantCollected := core.ItemStack{Item: core.ItemGrass, Count: 1}
	// 挖掘产生地面掉落物，玩家需在拾取延迟后原地拾取才会更新快捷栏。
	deadline := time.Now().Add(5 * time.Second)
	// 阶段 0 等待两人 Ready，阶段 1 验证玩家甲采集且玩家乙不受影响。
	stage := 0
	stopped := false
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待两名玩家快捷栏同步超时")
		}
		result := running.StepForTest()
		firstStates := hotbarDrainTick(t, firstClient, result.Tick, firstMirror, &firstReady)
		secondStates := hotbarDrainTick(t, secondClient, result.Tick, secondMirror, &secondReady)
		switch stage {
		case 0:
			if !firstReady || !secondReady {
				continue
			}
			sendClientMessage(t, firstClient, network.PlayerInput{
				Sequence: 1, Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01, Mining: true,
			})
			stage = 1
		case 1:
			// 玩家甲挖掘后在原地拾取；同一 tick 内玩家乙不得收到任何快捷栏更新。
			if !stopped && len(result.Changes) != 0 {
				sendClientMessage(t, firstClient, network.PlayerInput{
					Sequence: 2, Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01,
				})
				stopped = true
			}
			if len(secondStates) != 0 {
				t.Fatalf("玩家乙收到了不属于自己的快捷栏更新: %+v", secondStates)
			}
			if len(firstStates) == 0 {
				continue
			}
			if got := firstStates[len(firstStates)-1].Inventory.Hotbar.Slots[0]; got != wantCollected {
				continue
			}
			if got, ok := secondMirror.State(); !ok || got != (core.Inventory{}) {
				t.Fatalf("玩家乙镜像 = %+v, %v，想要保持为空", got, ok)
			}
			return
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
	mirror *client.InventoryMirror,
	ready *bool,
) []network.InventoryState {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var states []network.InventoryState
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		switch message := message.(type) {
		case network.InventoryState:
			if err := mirror.Apply(message); err != nil {
				t.Fatalf("InventoryMirror.Apply: %v", err)
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

func TestFullHotbarStillBreaksBlockIntoGroundDrop(t *testing.T) {
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	config := hotbarTestConfig(1)
	running := server.NewWorld(config, server.FlatTestGenerator{}, hotbarTestStore(config))
	var full core.Hotbar
	for slot := range full.Slots {
		full.Slots[slot] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	}
	if _, err := running.AttachSession(externalSessionSpec(1, 1, serverEndpoint, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		Inventory:      core.Inventory{Hotbar: full},
	})); err != nil {
		t.Fatalf("附加会话: %v", err)
	}
	shutdownHotbarServer(t, running, clientEndpoint)

	mirror := &client.InventoryMirror{}
	ready := false
	deadline := time.Now().Add(5 * time.Second)
	broken := false
	for {
		if time.Now().After(deadline) {
			t.Fatal("等待满快捷栏挖掘结果超时")
		}
		result := running.StepForTest()
		states, rejections := hotbarDrainTickAllowingRejections(
			t, clientEndpoint, result.Tick, mirror, &ready,
		)
		if broken {
			if len(rejections) != 0 {
				t.Fatalf("满快捷栏挖掘被拒绝: %+v", rejections)
			}
			if len(states) != 0 {
				t.Fatalf("满快捷栏挖掘仍发布了快捷栏更新: %+v", states)
			}
			if len(result.Changes) == 0 {
				continue
			}
			if got, _ := mirror.State(); got.Hotbar != full {
				t.Fatalf("满快捷栏被修改: %+v", got)
			}
			return
		}
		if ready {
			sendClientMessage(t, clientEndpoint, network.PlayerInput{
				Sequence: 1, Yaw: 0, Pitch: -float32(math.Pi)/2 + 0.01, Mining: true,
			})
			broken = true
		}
	}
}

func hotbarTestStore(config server.Config) storage.WorldStore {
	return storage.NewMemory(storage.Metadata{
		FormatVersion:  1,
		Seed:           config.Seed,
		SpawnDimension: config.SpawnDimension,
		SpawnAnchor:    config.SpawnAnchor,
	})
}

// hotbarDrainTickAllowingRejections 与 hotbarDrainTick 相同，但把拒绝作为结果返回。
func hotbarDrainTickAllowingRejections(
	t *testing.T,
	endpoint network.ClientEndpoint,
	throughTick uint64,
	mirror *client.InventoryMirror,
	ready *bool,
) ([]network.InventoryState, []network.CommandRejected) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var states []network.InventoryState
	var rejections []network.CommandRejected
	for {
		message, err := endpoint.Recv(ctx)
		if err != nil {
			t.Fatalf("接收服务端消息: %v", err)
		}
		switch message := message.(type) {
		case network.InventoryState:
			if err := mirror.Apply(message); err != nil {
				t.Fatalf("InventoryMirror.Apply: %v", err)
			}
			states = append(states, message)
		case network.CommandRejected:
			rejections = append(rejections, message)
		case network.PlayerState:
			*ready = message.Ready
			if message.ServerTick == throughTick {
				return states, rejections
			}
			if message.ServerTick > throughTick {
				t.Fatalf("PlayerState tick=%d，跳过目标 tick=%d", message.ServerTick, throughTick)
			}
		}
	}
}
