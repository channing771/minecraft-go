package storage

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"minecraft-go/internal/core"
)

type region struct {
	mu         sync.RWMutex
	key        RegionKey
	path       string
	file       regionFile
	activeBank int
	bank       regionBank
	banks      [2]regionBank
	bankValid  [2]bool
	fileHooks  regionFileHooks

	compactionHooks regionCompactionHooks
}

type preparedRegionSave struct {
	save    ChunkSave
	slot    int
	hash    [32]byte
	payload []byte
}

var errRegionPayloadInvalid = errors.New("region payload integrity failure")

func createRegion(ctx context.Context, path string, key RegionKey) (*region, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	bankA, err := encodeRegionBank(key, regionBank{Generation: 1})
	if err != nil {
		return nil, err
	}
	bankB, err := encodeRegionBank(key, regionBank{})
	if err != nil {
		return nil, err
	}
	header := make([]byte, dataStartSector*sectorSize)
	superblock := encodeSuperblock(key)
	copy(header, superblock[:])
	copy(header[bankOffset(0):], bankA[:])
	copy(header[bankOffset(1):], bankB[:])

	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, "."+filepath.Base(path)+".create-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary region %q: %w", path, err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := writeFullAt(temporary, header, 0); err != nil {
		return nil, fmt.Errorf("write temporary region %q: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := temporary.Sync(); err != nil {
		return nil, fmt.Errorf("sync temporary region %q: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close temporary region %q: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return nil, fmt.Errorf("rename temporary region %q: %w", path, err)
	}
	removeTemporary = false
	if err := syncDirectory(parent); err != nil {
		return nil, fmt.Errorf("sync region directory %q: %w", parent, err)
	}

	return openRegion(ctx, path, key)
}

func openRegion(ctx context.Context, path string, key RegionKey) (*region, error) {
	return openRegionWithHooks(ctx, path, key, regionFileHooks{Open: openRegionFile})
}

func openRegionFile(name string, flag int, mode os.FileMode) (regionFile, error) {
	return os.OpenFile(name, flag, mode)
}

func openRegionWithHooks(
	ctx context.Context,
	path string,
	key RegionKey,
	hooks regionFileHooks,
) (*region, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if hooks.Open == nil {
		return nil, fmt.Errorf("open region %q: nil file hook", path)
	}
	file, err := hooks.Open(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open region %q: %w", path, err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat region %q: %w", path, err)
	}
	header := make([]byte, dataStartSector*sectorSize)
	if err := readFullAt(file, header, 0); err != nil {
		return nil, fmt.Errorf("%w: read region header %q: %v", ErrCorrupt, path, err)
	}
	if err := decodeSuperblock(key, header[:sectorSize]); err != nil {
		return nil, fmt.Errorf("decode region superblock %q: %w", path, err)
	}
	bankA, errA := decodeRegionBank(
		key,
		header[bankOffset(0):bankOffset(0)+regionBankSize],
		info.Size(),
	)
	bankB, errB := decodeRegionBank(
		key,
		header[bankOffset(1):bankOffset(1)+regionBankSize],
		info.Size(),
	)
	bank, activeBank, err := selectRegionBank(bankA, errA, bankB, errB)
	if err != nil {
		return nil, fmt.Errorf("select region bank %q: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	closeOnError = false
	return &region{
		key:        key,
		path:       path,
		file:       file,
		activeBank: activeBank,
		bank:       bank,
		banks:      [2]regionBank{bankA, bankB},
		bankValid:  [2]bool{errA == nil, errB == nil},
		fileHooks:  hooks,
	}, nil
}

func (r *region) load(ctx context.Context, key core.ChunkKey) (StoredChunk, error) {
	if err := ctx.Err(); err != nil {
		return StoredChunk{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return StoredChunk{}, err
	}
	if r.file == nil {
		return StoredChunk{}, os.ErrClosed
	}

	stored, err := r.loadLocked(ctx, key)
	if err != nil {
		return StoredChunk{}, err
	}
	stored.Chunk = stored.Chunk.Clone()
	return stored, nil
}

func (r *region) save(ctx context.Context, saves []ChunkSave) (SaveResult, error) {
	result := SaveResult{Committed: make(map[core.ChunkKey]uint64, len(saves))}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if r.file == nil {
		return result, os.ErrClosed
	}

	prepared := make([]preparedRegionSave, 0, len(saves))
	for _, save := range saves {
		if err := validateChunkSave(save); err != nil {
			return result, err
		}
		regionKey, slot := RegionFor(save.Key)
		if regionKey != r.key {
			return result, fmt.Errorf(
				"%w: chunk %v belongs to region %+v, not %+v",
				ErrCorrupt, save.Key, regionKey, r.key,
			)
		}
		payload, err := encodeChunkPayload(save)
		if err != nil {
			return result, err
		}
		prepared = append(prepared, preparedRegionSave{
			save: save, slot: slot, hash: save.Chunk.Hash(), payload: payload,
		})
		if err := ctx.Err(); err != nil {
			return result, err
		}
	}
	sort.SliceStable(prepared, func(i, j int) bool {
		return prepared[i].slot < prepared[j].slot
	})

	pending := make(map[core.ChunkKey]preparedRegionSave, len(prepared))
	for _, candidate := range prepared {
		if current, ok := pending[candidate.save.Key]; ok {
			if candidate.save.Revision == current.save.Revision && candidate.hash != current.hash {
				return result, fmt.Errorf(
					"%w: %v revision %d",
					ErrRevisionConflict, candidate.save.Key, candidate.save.Revision,
				)
			}
			if candidate.save.Revision > current.save.Revision {
				pending[candidate.save.Key] = candidate
			}
			continue
		}

		entry := r.bank.Entries[candidate.slot]
		if entry.OffsetSector != 0 {
			if candidate.save.Revision < entry.Revision {
				result.Committed[candidate.save.Key] = entry.Revision
				continue
			}
			if candidate.save.Revision == entry.Revision {
				stored, err := r.loadLocked(ctx, candidate.save.Key)
				if err != nil {
					return result, err
				}
				if candidate.hash != stored.Chunk.Hash() {
					return result, fmt.Errorf(
						"%w: %v revision %d",
						ErrRevisionConflict, candidate.save.Key, candidate.save.Revision,
					)
				}
				result.Committed[candidate.save.Key] = entry.Revision
				continue
			}
		}
		pending[candidate.save.Key] = candidate
	}
	if len(pending) == 0 {
		return result, nil
	}
	if r.bank.Generation == math.MaxUint64 {
		return result, fmt.Errorf("%w: region generation overflow", ErrCorrupt)
	}

	ordered := make([]preparedRegionSave, 0, len(pending))
	for _, candidate := range pending {
		ordered = append(ordered, candidate)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].slot < ordered[j].slot })

	info, err := r.file.Stat()
	if err != nil {
		return result, fmt.Errorf("stat region %q: %w", r.path, err)
	}
	fileSize := info.Size()
	free, err := freeSectorExtents(r.bank, fileSize)
	if err != nil {
		return result, err
	}
	appendSector := uint64(fileSize / sectorSize)
	if fileSize%sectorSize != 0 {
		appendSector++
	}
	if appendSector > math.MaxUint32 {
		return result, fmt.Errorf("%w: region file exceeds uint32 sectors", ErrCorrupt)
	}
	next := r.bank
	for _, candidate := range ordered {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		sectorCount := (len(candidate.payload) + sectorSize - 1) / sectorSize
		allocation, remaining := allocateExtent(free, uint32(sectorCount), uint32(appendSector))
		if allocation.Count == 0 {
			return result, fmt.Errorf("%w: region extent exceeds uint32 sectors", ErrCorrupt)
		}
		free = remaining
		endSector := uint64(allocation.First) + uint64(allocation.Count)
		if endSector > math.MaxUint32 {
			return result, fmt.Errorf("%w: region extent exceeds uint32 sectors", ErrCorrupt)
		}
		if endSector > appendSector {
			appendSector = endSector
		}
		offset := int64(allocation.First) * sectorSize
		extent := make([]byte, sectorCount*sectorSize)
		copy(extent, candidate.payload)
		if err := writeFullAt(r.file, extent, offset); err != nil {
			return result, fmt.Errorf("write chunk payload %v: %w", candidate.save.Key, err)
		}
		next.Entries[candidate.slot] = regionEntry{
			OffsetSector:  uint32(offset / sectorSize),
			SectorCount:   uint32(sectorCount),
			PayloadLength: uint32(len(candidate.payload)),
			Revision:      candidate.save.Revision,
			PayloadCRC32C: crc32.Checksum(candidate.payload, regionCRCTable),
		}
	}
	if err := r.file.Sync(); err != nil {
		return result, fmt.Errorf("sync payloads: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	next.Generation = r.bank.Generation + 1
	encoded, err := encodeRegionBank(r.key, next)
	if err != nil {
		return result, err
	}
	inactiveBank := 1 - r.activeBank
	if err := writeFullAt(r.file, encoded[:], bankOffset(inactiveBank)); err != nil {
		return result, err
	}
	if err := r.file.Sync(); err != nil {
		return result, fmt.Errorf("sync index bank: %w", err)
	}
	r.bank, r.activeBank = next, inactiveBank
	r.banks[inactiveBank] = next
	r.bankValid[inactiveBank] = true
	for _, candidate := range ordered {
		result.Committed[candidate.save.Key] = candidate.save.Revision
	}
	return result, nil
}

func (r *region) sync(ctx context.Context) error {
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
	return r.file.Sync()
}

func (r *region) close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

func (r *region) loadLocked(ctx context.Context, key core.ChunkKey) (StoredChunk, error) {
	regionKey, slot := RegionFor(key)
	if regionKey != r.key {
		return StoredChunk{}, fmt.Errorf(
			"%w: chunk %v belongs to region %+v, not %+v",
			ErrCorrupt, key, regionKey, r.key,
		)
	}
	activeEntry := r.bank.Entries[slot]
	if activeEntry.OffsetSector == 0 {
		return StoredChunk{}, fmt.Errorf("%w: %v", ErrChunkNotFound, key)
	}
	active, activeErr := r.loadEntry(ctx, key, slot, activeEntry)
	if activeErr == nil {
		return StoredChunk{
			Key:               key,
			Revision:          active.Revision,
			PersistedRevision: activeEntry.Revision,
			Chunk:             active.Chunk,
		}, nil
	}
	if !errors.Is(activeErr, errRegionPayloadInvalid) {
		return StoredChunk{}, activeErr
	}

	inactiveBank := 1 - r.activeBank
	inactiveEntry := r.banks[inactiveBank].Entries[slot]
	var old decodedPayload
	oldErr := fmt.Errorf("%w: inactive bank entry is ineligible", ErrCorrupt)
	if r.bankValid[inactiveBank] &&
		inactiveEntry.OffsetSector != 0 &&
		inactiveEntry.Revision < activeEntry.Revision &&
		!regionExtentsOverlap(activeEntry, inactiveEntry) {
		old, oldErr = r.loadEntry(ctx, key, slot, inactiveEntry)
	}
	if err := ctx.Err(); err != nil {
		return StoredChunk{}, err
	}
	if oldErr != nil {
		return StoredChunk{}, fmt.Errorf(
			"%w: active=%w fallback=%w", ErrCorrupt, activeErr, oldErr,
		)
	}
	if activeEntry.Revision == math.MaxUint64 {
		return StoredChunk{}, fmt.Errorf(
			"%w: active=%w fallback revision would overflow", ErrCorrupt, activeErr,
		)
	}
	return StoredChunk{
		Key:               key,
		Revision:          activeEntry.Revision + 1,
		PersistedRevision: old.Revision,
		NeedsRewrite:      true,
		Recovered:         true,
		Chunk:             old.Chunk,
	}, nil
}

func (r *region) loadEntry(
	ctx context.Context,
	key core.ChunkKey,
	slot int,
	entry regionEntry,
) (decodedPayload, error) {
	if err := ctx.Err(); err != nil {
		return decodedPayload{}, err
	}
	if entry.OffsetSector == 0 {
		return decodedPayload{}, fmt.Errorf("%w: absent region entry %d", ErrCorrupt, slot)
	}
	payload := make([]byte, int(entry.PayloadLength))
	if err := readFullAt(r.file, payload, int64(entry.OffsetSector)*sectorSize); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return decodedPayload{}, fmt.Errorf(
				"%w: read chunk payload %v: %w", ErrCorrupt, key, err,
			)
		}
		return decodedPayload{}, fmt.Errorf("read chunk payload %v: %w", key, err)
	}
	if err := ctx.Err(); err != nil {
		return decodedPayload{}, err
	}
	if crc32.Checksum(payload, regionCRCTable) != entry.PayloadCRC32C {
		return decodedPayload{}, fmt.Errorf(
			"%w: %w: chunk payload CRC32C for %v",
			ErrCorrupt, errRegionPayloadInvalid, key,
		)
	}
	decoded, err := decodeChunkPayload(key, entry.Revision, payload)
	if err != nil {
		if errors.Is(err, ErrFutureVersion) {
			return decodedPayload{}, err
		}
		return decodedPayload{}, fmt.Errorf("%w: %w", errRegionPayloadInvalid, err)
	}
	if err := ctx.Err(); err != nil {
		return decodedPayload{}, err
	}
	return decoded, nil
}

func regionExtentsOverlap(a, b regionEntry) bool {
	aStart := uint64(a.OffsetSector)
	aEnd := aStart + uint64(a.SectorCount)
	bStart := uint64(b.OffsetSector)
	bEnd := bStart + uint64(b.SectorCount)
	return aStart < bEnd && bStart < aEnd
}

func bankOffset(index int) int64 {
	if index == 0 {
		return int64(bankAStartSector * sectorSize)
	}
	return int64(bankBStartSector * sectorSize)
}

func alignSector(offset int64) int64 {
	return (offset + sectorSize - 1) / sectorSize * sectorSize
}

func writeFullAt(file regionFile, data []byte, offset int64) error {
	for len(data) > 0 {
		written, err := file.WriteAt(data, offset)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
		offset += int64(written)
	}
	return nil
}

func readFullAt(file regionFile, data []byte, offset int64) error {
	for len(data) > 0 {
		read, err := file.ReadAt(data, offset)
		if read > 0 {
			data = data[read:]
			offset += int64(read)
		}
		if err != nil {
			return err
		}
		if read == 0 {
			return io.ErrNoProgress
		}
	}
	return nil
}
