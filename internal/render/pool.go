// Package render 负责 GPU 渲染编排。
//
// 本包只通过 internal/gfx 接触 GPU，不得 import 任何 WebGPU 绑定。
package render

import "sort"

// Alloc 是显存池中的一段，单位是面数而非字节。
type Alloc struct{ Offset, Size uint32 }

type freeBlock struct{ offset, size uint32 }

// Pool 是采用 best-fit、可合并相邻空闲块的显存区间分配器。
type Pool struct {
	capacity uint32
	free     []freeBlock
	used     uint32
}

func NewPool(capacity uint32) *Pool {
	return &Pool{
		capacity: capacity,
		free:     []freeBlock{{offset: 0, size: capacity}},
	}
}

// Alloc 分配 faces 个面的空间。空间不足时返回 ok=false。
func (p *Pool) Alloc(faces uint32) (Alloc, bool) {
	if faces == 0 || faces > p.capacity {
		return Alloc{}, false
	}

	best := -1
	for i, b := range p.free {
		if b.size < faces {
			continue
		}
		if best < 0 || b.size < p.free[best].size {
			best = i
		}
	}
	if best < 0 {
		return Alloc{}, false
	}

	b := p.free[best]
	a := Alloc{Offset: b.offset, Size: faces}
	if b.size == faces {
		p.free = append(p.free[:best], p.free[best+1:]...)
	} else {
		p.free[best] = freeBlock{offset: b.offset + faces, size: b.size - faces}
	}
	p.used += faces
	return a, true
}

// Free 归还一段空间，并与相邻空闲块合并。
func (p *Pool) Free(a Alloc) {
	if a.Size == 0 {
		return
	}
	p.used -= a.Size

	i := sort.Search(len(p.free), func(i int) bool {
		return p.free[i].offset >= a.Offset
	})
	p.free = append(p.free, freeBlock{})
	copy(p.free[i+1:], p.free[i:])
	p.free[i] = freeBlock{offset: a.Offset, size: a.Size}

	if i+1 < len(p.free) && p.free[i].offset+p.free[i].size == p.free[i+1].offset {
		p.free[i].size += p.free[i+1].size
		p.free = append(p.free[:i+1], p.free[i+2:]...)
	}
	if i > 0 && p.free[i-1].offset+p.free[i-1].size == p.free[i].offset {
		p.free[i-1].size += p.free[i].size
		p.free = append(p.free[:i], p.free[i+1:]...)
	}
}

func (p *Pool) Used() uint32 { return p.used }

func (p *Pool) LargestFree() uint32 {
	var largest uint32
	for _, b := range p.free {
		if b.size > largest {
			largest = b.size
		}
	}
	return largest
}

// Fragmentation 返回 1 - 最大空闲块/总空闲量。
func (p *Pool) Fragmentation() float32 {
	total := p.capacity - p.used
	if total == 0 {
		return 0
	}
	return 1 - float32(p.LargestFree())/float32(total)
}

// UploadBudget 限制每帧写入 GPU 的字节数。
type UploadBudget struct {
	perFrame  uint32
	spent     uint32
	exhausted bool
}

func NewUploadBudget(bytesPerFrame uint32) *UploadBudget {
	return &UploadBudget{perFrame: bytesPerFrame}
}

func (b *UploadBudget) BeginFrame() {
	b.spent = 0
	b.exhausted = false
}

// TryConsume 申请上传 bytes 字节。首个超预算请求会放行一次，避免永久饥饿。
func (b *UploadBudget) TryConsume(bytes uint32) bool {
	if b.exhausted {
		return false
	}
	// 用减法比较，避免 spent+bytes 的 uint32 加法溢出。
	if b.spent > b.perFrame || bytes > b.perFrame-b.spent {
		if b.spent > 0 {
			return false
		}
		b.exhausted = true
		b.spent = bytes
		return true
	}
	b.spent += bytes
	return true
}
