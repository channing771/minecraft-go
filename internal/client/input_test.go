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

func TestInputStateUsesRisingEdgesAndNumberSelection(t *testing.T) {
	var state client.InputState

	first := state.Update(true, true, 0)
	if !first.Break || !first.Place || first.SelectedBlock != core.StoneID {
		t.Fatalf("首次按下 = %+v", first)
	}
	held := state.Update(true, true, 2)
	if held.Break || held.Place || held.SelectedBlock != core.DirtID {
		t.Fatalf("持续按下/选择 2 = %+v", held)
	}
	released := state.Update(false, false, 0)
	if released.Break || released.Place || released.SelectedBlock != core.DirtID {
		t.Fatalf("释放 = %+v", released)
	}
	again := state.Update(true, false, 3)
	if !again.Break || again.Place || again.SelectedBlock != core.GrassID {
		t.Fatalf("再次按下/选择 3 = %+v", again)
	}
	invalid := state.Update(false, false, 9)
	if invalid.SelectedBlock != core.GrassID {
		t.Fatalf("无效数字改变选择: %+v", invalid)
	}
}
