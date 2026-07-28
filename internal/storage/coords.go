package storage

import "minecraft-go/internal/core"

func floorDiv32(value int32) int32 {
	wide := int64(value)
	if wide >= 0 {
		return int32(wide / 32)
	}
	return int32(-((-wide + 31) / 32))
}

func RegionFor(key core.ChunkKey) (RegionKey, int) {
	rx, rz := floorDiv32(key.Pos.X), floorDiv32(key.Pos.Z)
	lx, lz := key.Pos.X-rx*32, key.Pos.Z-rz*32
	return RegionKey{Dimension: key.Dimension, X: rx, Z: rz}, int(lz*32 + lx)
}
