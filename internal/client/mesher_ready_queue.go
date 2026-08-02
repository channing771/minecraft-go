package client

import (
	"container/heap"

	"minecraft-go/internal/core"
)

type readySectionHeap struct {
	keys    []core.SectionKey
	indexes map[core.SectionKey]int
}

func newReadySectionHeap() readySectionHeap {
	return readySectionHeap{indexes: make(map[core.SectionKey]int)}
}

func (ready *readySectionHeap) Len() int { return len(ready.keys) }
func (ready *readySectionHeap) Less(i, j int) bool {
	return sectionKeyLess(ready.keys[i], ready.keys[j])
}
func (ready *readySectionHeap) Swap(i, j int) {
	ready.keys[i], ready.keys[j] = ready.keys[j], ready.keys[i]
	ready.indexes[ready.keys[i]] = i
	ready.indexes[ready.keys[j]] = j
}
func (ready *readySectionHeap) Push(value any) {
	key := value.(core.SectionKey)
	ready.indexes[key] = len(ready.keys)
	ready.keys = append(ready.keys, key)
}
func (ready *readySectionHeap) Pop() any {
	last := len(ready.keys) - 1
	key := ready.keys[last]
	ready.keys[last] = core.SectionKey{}
	ready.keys = ready.keys[:last]
	delete(ready.indexes, key)
	return key
}
func (ready *readySectionHeap) Add(key core.SectionKey) bool {
	if ready.indexes == nil {
		ready.indexes = make(map[core.SectionKey]int)
	}
	if _, exists := ready.indexes[key]; exists {
		return false
	}
	heap.Push(ready, key)
	return true
}
func (ready *readySectionHeap) Remove(key core.SectionKey) bool {
	index, exists := ready.indexes[key]
	if !exists {
		return false
	}
	heap.Remove(ready, index)
	return true
}
func (ready *readySectionHeap) Take() (core.SectionKey, bool) {
	if ready.Len() == 0 {
		return core.SectionKey{}, false
	}
	return heap.Pop(ready).(core.SectionKey), true
}
func sectionKeyLess(left, right core.SectionKey) bool {
	if left.Dimension != right.Dimension {
		return left.Dimension < right.Dimension
	}
	if left.Pos.X != right.Pos.X {
		return left.Pos.X < right.Pos.X
	}
	if left.Pos.Z != right.Pos.Z {
		return left.Pos.Z < right.Pos.Z
	}
	return left.Pos.Y < right.Pos.Y
}
