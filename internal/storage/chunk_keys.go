package storage

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"minecraft-go/internal/core"
)

// ChunkKeys 返回磁盘上已有区块键的稳定只读快照。
func (store *DiskStore) ChunkKeys(ctx context.Context) ([]core.ChunkKey, error) {
	if store.closing.Load() {
		return nil, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closing.Load() || store.closed {
		return nil, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	dimensionsPath := filepath.Join(store.files.root, "dimensions")
	dimensions, err := os.ReadDir(dimensionsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dimensions %q: %w", dimensionsPath, err)
	}

	var keys []core.ChunkKey
	for _, dimensionEntry := range dimensions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !dimensionEntry.IsDir() {
			continue
		}
		dimension, err := strconv.ParseInt(dimensionEntry.Name(), 10, 32)
		if err != nil {
			continue
		}

		regionsPath := filepath.Join(dimensionsPath, dimensionEntry.Name(), "regions")
		entries, err := os.ReadDir(regionsPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read regions %q: %w", regionsPath, err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			name, ok := strings.CutPrefix(entry.Name(), "r.")
			if !ok || entry.IsDir() {
				continue
			}
			name, ok = strings.CutSuffix(name, ".region")
			if !ok {
				continue
			}
			coordinates := strings.Split(name, ".")
			if len(coordinates) != 2 {
				continue
			}
			regionX, errX := strconv.ParseInt(coordinates[0], 10, 32)
			regionZ, errZ := strconv.ParseInt(coordinates[1], 10, 32)
			if errX != nil || errZ != nil {
				continue
			}

			regionKey := RegionKey{
				Dimension: core.DimensionID(dimension),
				X:         int32(regionX),
				Z:         int32(regionZ),
			}
			opened, err := openRegion(ctx, filepath.Join(regionsPath, entry.Name()), regionKey)
			if err != nil {
				return nil, fmt.Errorf("open region %+v: %w", regionKey, err)
			}
			for slot, regionEntry := range opened.bank.Entries {
				if regionEntry.OffsetSector == 0 {
					continue
				}
				x := int64(regionKey.X)*32 + int64(slot%32)
				z := int64(regionKey.Z)*32 + int64(slot/32)
				if x < math.MinInt32 || x > math.MaxInt32 || z < math.MinInt32 || z > math.MaxInt32 {
					_ = opened.close()
					return nil, fmt.Errorf("%w: region %+v slot %d overflows chunk coordinates", ErrCorrupt, regionKey, slot)
				}
				keys = append(keys, core.ChunkKey{
					Dimension: regionKey.Dimension,
					Pos:       core.ChunkPos{X: int32(x), Z: int32(z)},
				})
			}
			if err := opened.close(); err != nil {
				return nil, fmt.Errorf("close region %+v: %w", regionKey, err)
			}
		}
	}

	slices.SortFunc(keys, func(left, right core.ChunkKey) int {
		if order := cmp.Compare(left.Dimension, right.Dimension); order != 0 {
			return order
		}
		if order := cmp.Compare(left.Pos.X, right.Pos.X); order != 0 {
			return order
		}
		return cmp.Compare(left.Pos.Z, right.Pos.Z)
	})
	return keys, nil
}
