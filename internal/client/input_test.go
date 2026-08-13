package client_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
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

func TestInputStateKeepsMiningHeldAndUsesRisingEdgesForOtherActions(t *testing.T) {
	var state client.InputState

	first := state.Update(true, true, 0, false, false, false)
	if !first.Mining || !first.Place || first.Select {
		t.Fatalf("首次按下 = %+v", first)
	}
	held := state.Update(true, true, 2, false, false, false)
	if !held.Mining || held.Place || !held.Select || held.SelectSlot != 1 {
		t.Fatalf("持续按下并按下数字 2 = %+v", held)
	}
	repeat := state.Update(true, true, 2, false, false, false)
	if !repeat.Mining || repeat.Select {
		t.Fatalf("连续按住主键或数字键状态错误: %+v", repeat)
	}
	released := state.Update(false, false, 0, false, false, false)
	if released.Mining || released.Place || released.Select {
		t.Fatalf("释放 = %+v", released)
	}
	again := state.Update(true, false, 9, false, false, false)
	if !again.Mining || again.Place || !again.Select || again.SelectSlot != core.HotbarSlots-1 {
		t.Fatalf("再次按下并按下数字 9 = %+v", again)
	}
}

func TestInputStateIgnoresNumbersOutsideHotbarRange(t *testing.T) {
	var state client.InputState
	for _, number := range []int{-1, 0, core.HotbarSlots + 1, 99} {
		if got := state.Update(false, false, number, false, false, false); got.Select {
			t.Fatalf("数字 %d 产生了选择请求: %+v", number, got)
		}
	}
}

func TestInputStateTogglesInventoryOnRisingEdge(t *testing.T) {
	var state client.InputState
	if got := state.Update(false, false, 0, true, false, false); !got.ToggleInventory {
		t.Fatalf("E 上升沿未产生开关: %+v", got)
	}
	if got := state.Update(false, false, 0, true, false, true); got.ToggleInventory {
		t.Fatalf("按住 E 重复开关: %+v", got)
	}
	if got := state.Update(false, false, 0, false, false, true); got.ToggleInventory {
		t.Fatalf("释放 E 产生开关: %+v", got)
	}
	if got := state.Update(false, false, 0, true, false, true); !got.ToggleInventory {
		t.Fatalf("再次按下 E 未产生开关: %+v", got)
	}
}

func TestInputStateSuppressesGameActionsWhileInventoryOpen(t *testing.T) {
	var state client.InputState
	got := state.Update(true, true, 3, false, false, true)
	if got.Mining || got.Place || got.Select {
		t.Fatalf("界面打开时未抑制游戏操作: %+v", got)
	}
	if !got.Click {
		t.Fatalf("界面打开时左键未产生点击: %+v", got)
	}
	if held := state.Update(true, true, 3, false, false, true); held.Mining || held.Click {
		t.Fatalf("界面打开时按住左键产生采掘或重复点击: %+v", held)
	}
}

func TestInputStateDropsOnlyOnValidQRisingEdge(t *testing.T) {
	var state client.InputState

	// 上升沿触发一次。
	if got := state.Update(false, false, 0, false, true, false); !got.Drop {
		t.Fatalf("Q 上升沿 = %+v，想要触发丢弃", got)
	}
	// 按住不重复。
	if got := state.Update(false, false, 0, false, true, false); got.Drop {
		t.Fatalf("按住 Q 重复丢弃 = %+v", got)
	}

	// 松开后在背包打开时按下：抑制，但物理状态必须被记录。
	state.Update(false, false, 0, false, false, false)
	if got := state.Update(false, false, 0, false, true, true); got.Drop {
		t.Fatalf("背包打开时 Q 产生丢弃 = %+v", got)
	}
	// 关闭背包但仍按住：不得因为抑制期未记录状态而误触发。
	if got := state.Update(false, false, 0, false, true, false); got.Drop {
		t.Fatalf("关闭背包但仍按住 Q 误触发 = %+v", got)
	}
	// 完整松开后再次按下才允许触发。
	state.Update(false, false, 0, false, false, false)
	if got := state.Update(false, false, 0, false, true, false); !got.Drop {
		t.Fatalf("重新按下 Q = %+v，想要触发丢弃", got)
	}
}
