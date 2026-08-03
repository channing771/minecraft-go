package client_test

import (
	"testing"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
)

func TestMovementFromKeysCancelsOpposites(t *testing.T) {
	tests := []struct {
		name             string
		w, a, s, d, jump bool
		want             client.Movement
	}{
		{"forward right jump", true, false, false, true, true, client.Movement{MoveX: 1, MoveZ: 1, Jump: true}},
		{"opposites cancel", true, true, true, true, false, client.Movement{}},
		{"back left", false, true, true, false, false, client.Movement{MoveX: -1, MoveZ: -1}},
	}
	for _, tc := range tests {
		if got := client.MovementFromKeys(tc.w, tc.a, tc.s, tc.d, tc.jump); got != tc.want {
			t.Fatalf("%s=%+v，想要 %+v", tc.name, got, tc.want)
		}
	}
}

func TestInputStateUsesRisingEdgesAndHotbarSelection(t *testing.T) {
	var state client.InputState

	first := state.Update(true, true, 0)
	if !first.Break || !first.Place || first.Select {
		t.Fatalf("首次按下 = %+v", first)
	}
	held := state.Update(true, true, 2)
	if held.Break || held.Place || !held.Select || held.SelectSlot != 1 {
		t.Fatalf("持续按下并按下数字 2 = %+v", held)
	}
	repeat := state.Update(true, true, 2)
	if repeat.Select {
		t.Fatalf("按住同一数字重复发送选择: %+v", repeat)
	}
	released := state.Update(false, false, 0)
	if released.Break || released.Place || released.Select {
		t.Fatalf("释放 = %+v", released)
	}
	again := state.Update(true, false, 9)
	if !again.Break || again.Place || !again.Select || again.SelectSlot != core.HotbarSlots-1 {
		t.Fatalf("再次按下并按下数字 9 = %+v", again)
	}
}

func TestInputStateIgnoresNumbersOutsideHotbarRange(t *testing.T) {
	var state client.InputState
	for _, number := range []int{-1, 0, core.HotbarSlots + 1, 99} {
		if got := state.Update(false, false, number); got.Select {
			t.Fatalf("数字 %d 产生了选择请求: %+v", number, got)
		}
	}
}
