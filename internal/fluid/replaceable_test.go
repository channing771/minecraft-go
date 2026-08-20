package fluid

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestReplaceable 覆盖 task-2-brief.md 2.1 的可替换判定表：
//
//	目标格                          可替换？
//	空气                            是
//	流体等级更大的流动水（更弱）      是
//	源方块                          否
//	流体等级更小或相等的流动水        否
//	任意其他非空气方块（实心）        否
func TestReplaceable(t *testing.T) {
	cases := []struct {
		name     string
		target   core.BlockID
		newLevel uint8
		wantTrue bool
	}{
		{"空气可替换", core.AirID, 3, true},
		{"更大等级(更弱)的流动水可替换", core.WaterLevel5ID, 3, true},
		{"源方块不可替换", core.WaterSourceID, 3, false},
		{"更小等级(更强)的流动水不可替换", core.WaterLevel2ID, 3, false},
		{"相等等级的流动水不可替换", core.WaterLevel3ID, 3, false},
		{"石头等实心方块不可替换", core.StoneID, 3, false},
		{"空气对最强等级1同样可替换", core.AirID, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Replaceable(c.target, c.newLevel)
			if got != c.wantTrue {
				t.Errorf("Replaceable(%v, %d) = %v, want %v", c.target, c.newLevel, got, c.wantTrue)
			}
		})
	}
}
