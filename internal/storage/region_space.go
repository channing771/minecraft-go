package storage

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
)

type sectorExtent struct {
	First uint32
	Count uint32
}

type regionSpacePolicy struct {
	WasteRatio float64
	MinWaste   int64
}

var productionRegionSpacePolicy = regionSpacePolicy{
	WasteRatio: 0.25,
	MinWaste:   8 << 20,
}

type regionCompactionHooks struct {
	beforeTempSync func() error
	rename         func(string, string) error
	syncDirectory  func(string) error
}

func freeSectorExtents(bank regionBank, fileSize int64) ([]sectorExtent, error) {
	if fileSize < int64(dataStartSector*sectorSize) {
		return nil, fmt.Errorf("%w: region file is shorter than fixed headers", ErrCorrupt)
	}
	if err := validateRegionBank(bank, fileSize, true); err != nil {
		return nil, err
	}

	totalSectors := uint64(fileSize / sectorSize)
	if fileSize%sectorSize != 0 {
		totalSectors++
	}
	if totalSectors > math.MaxUint32 {
		return nil, fmt.Errorf("%w: region file exceeds uint32 sectors", ErrCorrupt)
	}
	occupied := make([]bool, int(totalSectors))
	for sector := 0; sector < dataStartSector; sector++ {
		occupied[sector] = true
	}
	for _, entry := range bank.Entries {
		if entry.OffsetSector == 0 {
			continue
		}
		end := uint64(entry.OffsetSector) + uint64(entry.SectorCount)
		for sector := uint64(entry.OffsetSector); sector < end; sector++ {
			occupied[int(sector)] = true
		}
	}

	free := make([]sectorExtent, 0)
	for sector := uint32(dataStartSector); uint64(sector) < totalSectors; {
		if occupied[int(sector)] {
			sector++
			continue
		}
		first := sector
		for uint64(sector) < totalSectors && !occupied[int(sector)] {
			sector++
		}
		free = append(free, sectorExtent{First: first, Count: sector - first})
	}
	return free, nil
}

func allocateExtent(
	free []sectorExtent,
	sectorCount uint32,
	appendSector uint32,
) (sectorExtent, []sectorExtent) {
	remaining := append([]sectorExtent(nil), free...)
	if sectorCount == 0 {
		return sectorExtent{}, remaining
	}
	for index, candidate := range remaining {
		if candidate.Count < sectorCount {
			continue
		}
		allocated := sectorExtent{First: candidate.First, Count: sectorCount}
		if candidate.Count == sectorCount {
			remaining = append(remaining[:index], remaining[index+1:]...)
		} else {
			remaining[index].First += sectorCount
			remaining[index].Count -= sectorCount
		}
		return allocated, remaining
	}
	if uint64(appendSector)+uint64(sectorCount) > math.MaxUint32 {
		return sectorExtent{}, remaining
	}
	return sectorExtent{First: appendSector, Count: sectorCount}, remaining
}

func (r *region) shouldCompact(policy regionSpacePolicy) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.file == nil {
		return false
	}
	info, err := r.file.Stat()
	if err != nil {
		return false
	}
	dataSize := info.Size() - int64(dataStartSector*sectorSize)
	if dataSize <= 0 {
		return false
	}
	var liveSize int64
	for _, entry := range r.bank.Entries {
		liveSize += int64(entry.SectorCount) * sectorSize
	}
	if liveSize > dataSize {
		return false
	}
	waste := dataSize - liveSize
	return waste >= policy.MinWaste && float64(waste)/float64(dataSize) >= policy.WasteRatio
}

func (r *region) writeCompactedFile(ctx context.Context, temporary *os.File) (regionBank, error) {
	if err := ctx.Err(); err != nil {
		return regionBank{}, err
	}
	if temporary == nil {
		return regionBank{}, fmt.Errorf("write compacted region %q: nil temporary file", r.path)
	}

	next := regionBank{Generation: r.bank.Generation}
	nextSector := uint64(dataStartSector)
	for slot, entry := range r.bank.Entries {
		if entry.OffsetSector == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return regionBank{}, err
		}
		if entry.PayloadLength == 0 {
			return regionBank{}, fmt.Errorf("%w: region entry %d has empty payload", ErrCorrupt, slot)
		}
		payload := make([]byte, int(entry.PayloadLength))
		if err := readFullAt(r.file, payload, int64(entry.OffsetSector)*sectorSize); err != nil {
			return regionBank{}, fmt.Errorf("read compacted region slot %d: %w", slot, err)
		}
		if crc32.Checksum(payload, regionCRCTable) != entry.PayloadCRC32C {
			return regionBank{}, fmt.Errorf("%w: region entry %d payload CRC32C", ErrCorrupt, slot)
		}

		sectorCount := uint64((entry.PayloadLength + sectorSize - 1) / sectorSize)
		endSector := nextSector + sectorCount
		if sectorCount == 0 || endSector > math.MaxUint32 {
			return regionBank{}, fmt.Errorf("%w: compacted region extent overflows", ErrCorrupt)
		}
		extent := make([]byte, int(sectorCount)*sectorSize)
		copy(extent, payload)
		if err := writeFullAt(temporary, extent, int64(nextSector)*sectorSize); err != nil {
			return regionBank{}, fmt.Errorf("write compacted region slot %d: %w", slot, err)
		}
		entry.OffsetSector = uint32(nextSector)
		entry.SectorCount = uint32(sectorCount)
		next.Entries[slot] = entry
		nextSector = endSector
	}

	bankA, err := encodeRegionBank(r.key, next)
	if err != nil {
		return regionBank{}, err
	}
	bankB, err := encodeRegionBank(r.key, regionBank{})
	if err != nil {
		return regionBank{}, err
	}
	header := make([]byte, dataStartSector*sectorSize)
	superblock := encodeSuperblock(r.key)
	copy(header, superblock[:])
	copy(header[bankOffset(0):], bankA[:])
	copy(header[bankOffset(1):], bankB[:])
	if err := writeFullAt(temporary, header, 0); err != nil {
		return regionBank{}, fmt.Errorf("write compacted region header: %w", err)
	}
	return next, nil
}

func (r *region) reopenCanonical() error {
	hooks := r.fileHooks
	if hooks.Open == nil {
		hooks.Open = openRegionFile
	}
	reopened, err := openRegionWithHooks(context.Background(), r.path, r.key, hooks)
	if err != nil {
		return err
	}

	old := r.file
	r.file = reopened.file
	r.activeBank = reopened.activeBank
	r.bank = reopened.bank
	r.banks = reopened.banks
	r.bankValid = reopened.bankValid
	r.fileHooks = hooks
	reopened.file = nil
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (r *region) compact(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.file == nil {
		return os.ErrClosed
	}

	parent := filepath.Dir(r.path)
	temporary, err := os.CreateTemp(parent, "."+filepath.Base(r.path)+".compact-*")
	if err != nil {
		return fmt.Errorf("create compacted region %q: %w", r.path, err)
	}
	temporaryPath := temporary.Name()
	temporaryOpen := true
	removeTemporary := true
	defer func() {
		if temporaryOpen {
			_ = temporary.Close()
		}
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	next, err := r.writeCompactedFile(ctx, temporary)
	if err != nil {
		return err
	}
	hooks := r.compactionHooks
	if hooks.beforeTempSync != nil {
		if err := hooks.beforeTempSync(); err != nil {
			return err
		}
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync compacted region %q: %w", r.path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close compacted region %q: %w", r.path, err)
	}
	temporaryOpen = false
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := r.file.Sync(); err != nil {
		return fmt.Errorf("sync canonical region %q: %w", r.path, err)
	}
	if err := r.file.Close(); err != nil {
		return errors.Join(
			fmt.Errorf("close canonical region %q: %w", r.path, err),
			r.reopenCanonical(),
		)
	}
	r.file = nil

	rename := hooks.rename
	if rename == nil {
		rename = os.Rename
	}
	if err := rename(temporaryPath, r.path); err != nil {
		return errors.Join(
			fmt.Errorf("replace canonical region %q: %w", r.path, err),
			r.reopenCanonical(),
		)
	}
	removeTemporary = false

	syncDir := hooks.syncDirectory
	if syncDir == nil {
		syncDir = syncDirectory
	}
	if err := syncDir(parent); err != nil {
		return errors.Join(
			fmt.Errorf("sync compacted region directory %q: %w", parent, err),
			r.reopenCanonical(),
		)
	}
	if err := r.reopenCanonical(); err != nil {
		return err
	}
	if r.activeBank != 0 || r.bank != next {
		return fmt.Errorf("%w: reopened compacted region does not match replacement", ErrCorrupt)
	}
	r.bank, r.activeBank = next, 0
	return nil
}
