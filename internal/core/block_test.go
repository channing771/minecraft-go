package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestCanonicalBlockIDsStayStable(t *testing.T) {
	got := []core.BlockID{
		core.AirID,
		core.BarrierID,
		core.StoneID,
		core.DirtID,
		core.GrassID,
		core.BedrockID,
	}
	for i, id := range got {
		if id != core.BlockID(i) {
			t.Fatalf("ID[%d] = %d，协议要求固定为 %d", i, id, i)
		}
	}
}

func TestBlockFaceOpposite(t *testing.T) {
	cases := []struct {
		in, want core.BlockFace
	}{
		{core.BlockFaceNegX, core.BlockFacePosX},
		{core.BlockFacePosX, core.BlockFaceNegX},
		{core.BlockFaceNegY, core.BlockFacePosY},
		{core.BlockFacePosY, core.BlockFaceNegY},
		{core.BlockFaceNegZ, core.BlockFacePosZ},
		{core.BlockFacePosZ, core.BlockFaceNegZ},
		{core.BlockFaceNone, core.BlockFaceNone},
	}
	for _, tc := range cases {
		if got := tc.in.Opposite(); got != tc.want {
			t.Fatalf("%v.Opposite() = %v，想要 %v", tc.in, got, tc.want)
		}
	}
}
