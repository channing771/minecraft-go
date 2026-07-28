package storage

import (
	"context"
	"testing"
)

func TestAllocatorNeverUsesActiveExtentsAndUsesFirstFit(t *testing.T) {
	bank := regionBank{Generation: 4}
	bank.Entries[0] = regionEntry{
		OffsetSector:  15,
		SectorCount:   2,
		PayloadLength: 5000,
		Revision:      1,
	}
	bank.Entries[1] = regionEntry{
		OffsetSector:  20,
		SectorCount:   1,
		PayloadLength: 100,
		Revision:      1,
	}

	free, err := freeSectorExtents(bank, 24*sectorSize)
	if err != nil {
		t.Fatal(err)
	}
	wantFree := []sectorExtent{{First: 17, Count: 3}, {First: 21, Count: 3}}
	if len(free) != len(wantFree) {
		t.Fatalf("free extents = %+v, want %+v", free, wantFree)
	}
	for index := range wantFree {
		if free[index] != wantFree[index] {
			t.Fatalf("free extents = %+v, want %+v", free, wantFree)
		}
	}

	extent, remaining := allocateExtent(free, 3, 24)
	if extent != (sectorExtent{First: 17, Count: 3}) {
		t.Fatalf("extent = %+v, want first free fit", extent)
	}
	if len(remaining) != 1 || remaining[0] != (sectorExtent{First: 21, Count: 3}) {
		t.Fatalf("remaining extents = %+v, want second free run", remaining)
	}
}

func TestAllocatorAppendsOnlyWhenNoFreeExtentFits(t *testing.T) {
	free := []sectorExtent{{First: 16, Count: 1}, {First: 20, Count: 2}}

	extent, remaining := allocateExtent(free, 3, 24)
	if extent != (sectorExtent{First: 24, Count: 3}) {
		t.Fatalf("extent = %+v, want append extent", extent)
	}
	if len(remaining) != len(free) || remaining[0] != free[0] || remaining[1] != free[1] {
		t.Fatalf("remaining extents = %+v, want unchanged %+v", remaining, free)
	}
}

func TestRegionSaveReusesInactiveOnlyExtentWithoutGrowing(t *testing.T) {
	path, key, chunkKey, _, _ := seededRegion(t)
	r, err := openRegion(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()

	_, slot := RegionFor(chunkKey)
	inactiveOnly := r.bank.Entries[slot]
	if _, err := r.save(context.Background(), []ChunkSave{changedSave(chunkKey, 2)}); err != nil {
		t.Fatal(err)
	}
	activeBefore := r.bank.Entries[slot]
	if regionExtentsOverlap(activeBefore, inactiveOnly) {
		t.Fatalf("fixture extents overlap: active=%+v inactive-only=%+v", activeBefore, inactiveOnly)
	}
	infoBefore, err := r.file.Stat()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := r.save(context.Background(), []ChunkSave{changedSave(chunkKey, 3)}); err != nil {
		t.Fatal(err)
	}
	got := r.bank.Entries[slot]
	if got.OffsetSector != inactiveOnly.OffsetSector || got.SectorCount != inactiveOnly.SectorCount {
		t.Fatalf("revision 3 extent = %+v, want inactive-only extent %+v", got, inactiveOnly)
	}
	if regionExtentsOverlap(got, activeBefore) {
		t.Fatalf("revision 3 overwrote active extent: new=%+v active=%+v", got, activeBefore)
	}
	infoAfter, err := r.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if infoAfter.Size() != infoBefore.Size() {
		t.Fatalf("region grew from %d to %d while reusable extent existed", infoBefore.Size(), infoAfter.Size())
	}
}
