package core_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func TestSmeltingOutput(t *testing.T) {
	tests := []struct {
		input, output core.ItemID
		ok            bool
	}{
		{core.ItemRawIron, core.ItemIronIngot, true},
		{core.ItemSand, core.ItemGlass, true},
		{core.ItemClay, core.ItemBrick, true},
		{core.ItemNone, core.ItemNone, false},
		{core.ItemStone, core.ItemNone, false},
	}
	for _, test := range tests {
		output, ok := core.SmeltingOutput(test.input)
		if output != test.output || ok != test.ok {
			t.Errorf("SmeltingOutput(%d) = (%d, %v)，想要 (%d, %v)",
				test.input, output, ok, test.output, test.ok)
		}
	}
}
