package server

import (
	"context"
	"sort"
	"testing"
	"time"

	"minecraft-go/internal/network"
)

// worldTimeByTick 从客户端 transcript 中提取每个权威 tick 的绝对世界时间。
func worldTimeByTick(connected *multiplayerTCPClient) map[uint64]uint64 {
	times := make(map[uint64]uint64)
	for _, event := range connected.transcript {
		if state, ok := event.message.(network.PlayerState); ok {
			times[state.ServerTick] = state.WorldTimeTicks
		}
	}
	return times
}

// TestMultiplayerTCPClientsObserveSameWorldTimePhase 证明同一权威 tick 下
// 所有客户端观察到相同的绝对世界时间，且时间随 tick 单调推进。
func TestMultiplayerTCPClientsObserveSameWorldTimePhase(t *testing.T) {
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	host := startMultiplayerTCPHost(t)
	var a, b *multiplayerTCPClient
	cleanupMultiplayerTCPTest(t, host, &a, &b)
	a = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, multiplayerIdentity(0xc1, "阿明"))
	b = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, multiplayerIdentity(0xc2, "Builder"))

	mustDrainMultiplayer(t, deadline, a, b, "两名客户端就绪", func() bool {
		return a.readyWithFootSnapshot() && b.readyWithFootSnapshot()
	})

	// 让两端各自累积若干个权威 tick 的玩家状态。
	for sequence := uint64(1); sequence <= 6; sequence++ {
		mustSendMultiplayer(t, deadline, a, network.PlayerInput{Sequence: sequence, Yaw: 0, Pitch: -0.2})
		mustSendMultiplayer(t, deadline, b, network.PlayerInput{Sequence: sequence, Yaw: 0, Pitch: -0.2})
	}
	mustDrainMultiplayer(t, deadline, a, b, "两端累积到共同的权威 tick", func() bool {
		shared := 0
		left, right := worldTimeByTick(a), worldTimeByTick(b)
		for tick := range left {
			if _, ok := right[tick]; ok {
				shared++
			}
		}
		return shared >= 3
	})

	left, right := worldTimeByTick(a), worldTimeByTick(b)
	shared := 0
	for tick, leftTime := range left {
		rightTime, ok := right[tick]
		if !ok {
			continue
		}
		shared++
		if leftTime != rightTime {
			t.Fatalf("权威 tick %d 上两端世界时间不同：%d 与 %d", tick, leftTime, rightTime)
		}
		if leftTime == 0 {
			t.Fatalf("权威 tick %d 的世界时间为 0，想要已推进的绝对时间", tick)
		}
	}
	if shared < 3 {
		t.Fatalf("共同 tick 数 = %d，想要至少 3", shared)
	}

	// 相邻 tick 之间世界时间必须严格递增且步长为 1。
	ticks := make([]uint64, 0, len(left))
	for tick := range left {
		ticks = append(ticks, tick)
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i] < ticks[j] })
	for index := 1; index < len(ticks); index++ {
		previous, current := ticks[index-1], ticks[index]
		if left[current]-left[previous] != current-previous {
			t.Fatalf(
				"tick %d→%d 世界时间从 %d 变为 %d，想要同步推进 %d",
				previous, current, left[previous], left[current], current-previous,
			)
		}
	}
}

// TestReconnectContinuesWorldTimeWithoutRollback 证明重连的客户端从当前权威
// 相位继续，而不是回到默认相位或回退已确认的时间。
func TestReconnectContinuesWorldTimeWithoutRollback(t *testing.T) {
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	host := startMultiplayerTCPHost(t)
	var first, second *multiplayerTCPClient
	cleanupMultiplayerTCPTest(t, host, &first, &second)
	identity := multiplayerIdentity(0xd1, "阿明")
	first = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, identity)
	mustDrainMultiplayer(t, deadline, first, nil, "首次连接就绪", func() bool {
		return first.readyWithFootSnapshot()
	})

	for sequence := uint64(1); sequence <= 6; sequence++ {
		mustSendMultiplayer(t, deadline, first, network.PlayerInput{
			Sequence: sequence, Yaw: 0, Pitch: -0.2,
		})
	}
	mustDrainMultiplayer(t, deadline, first, nil, "首次连接推进世界时间", func() bool {
		return first.local.WorldTimeTicks > 0 && first.local.LastInputSequence >= 6
	})
	beforeDisconnect := first.local.WorldTimeTicks

	mustCloseMultiplayerTCPClient(t, first)
	second = mustConnectMultiplayerTCPClient(t, deadline, host.Addr, identity)
	mustDrainMultiplayer(t, deadline, second, nil, "重连就绪", func() bool {
		return second.local.Ready && second.local.WorldTimeTicks > 0
	})

	if second.local.WorldTimeTicks < beforeDisconnect {
		t.Fatalf(
			"重连后世界时间 = %d，早于断线前的 %d",
			second.local.WorldTimeTicks, beforeDisconnect,
		)
	}
	// 重连不得把相位重置到起点。
	if second.local.WorldTimeTicks == 0 {
		t.Fatal("重连后世界时间被重置为 0")
	}
}
