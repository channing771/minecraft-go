// Package world 提供世界数据模型：区块、区段、调色板存储与光照。
package world

import "minecraft-go/internal/core"

// BlockID 是核心域方块 ID 的兼容别名。
type BlockID = core.BlockID

// AirID 是空气的方块 ID。
const AirID = core.AirID

// storageKind 是调色板容器的三种形态（spec §4.1）。
type storageKind uint8

const (
	// kindSingle：整段同一种方块，不分配位数据。
	kindSingle storageKind = iota
	// kindIndexed：用调色板索引，每方块 4 或 8 位。
	kindIndexed
	// kindDirect：直接存全局 ID，每方块 15 位。
	kindDirect
)

// directBits 是直接态每方块的位数。15 位可表示 32768 种方块。
const directBits = 15

// PalettedContainer 存储一个 16³ 区段的方块。
//
// 三态自动升级，降级由 Compact 惰性触发——避免在方块反复变更时抖动。
// 本类型不是并发安全的；并发访问由 COW + 原子指针交换保证（spec §4.3）。
type PalettedContainer struct {
	kind    storageKind
	single  BlockID            // kindSingle 时有效
	palette []BlockID          // kindIndexed 时有效，索引 -> BlockID
	lookup  map[BlockID]uint32 // kindIndexed 时有效，BlockID -> 索引
	bits    uint8              // 每方块位数：kindSingle 为 0
	data    []uint64           // 位打包数据
}

// NewPalettedContainer 创建一个全部填充 fill 的容器，处于单值态。
func NewPalettedContainer(fill BlockID) *PalettedContainer {
	return &PalettedContainer{kind: kindSingle, single: fill}
}

// blockIndex 把局部坐标映射为 0..4095 的线性索引。
//
// 用 YZX 顺序：同一 Y 平面上的方块在内存中连续，
// 贪心网格化按平面切片扫描（Task 9），这个顺序对它的缓存局部性最好。
func blockIndex(x, y, z int) int { return (y << 8) | (z << 4) | x }

// Get 返回局部坐标处的方块。坐标须在 0..15。
func (c *PalettedContainer) Get(x, y, z int) BlockID {
	if c.kind == kindSingle {
		return c.single
	}
	v := c.readRaw(blockIndex(x, y, z))
	if c.kind == kindDirect {
		return BlockID(v)
	}
	return c.palette[v]
}

// readRaw 从位打包数据中读出第 i 个槽的原始值。
//
// 不跨 uint64 边界打包：每个 uint64 装 64/bits 个槽，余下的位空着。
// 直接态每字 4 个槽（60 位用、4 位废），换来的是无分支的读写路径。
func (c *PalettedContainer) readRaw(i int) uint32 {
	perWord := 64 / int(c.bits)
	word := c.data[i/perWord]
	shift := uint((i % perWord) * int(c.bits))
	mask := uint64(1)<<c.bits - 1
	return uint32((word >> shift) & mask)
}

// writeRaw 把原始值写入第 i 个槽。
func (c *PalettedContainer) writeRaw(i int, v uint32) {
	perWord := 64 / int(c.bits)
	shift := uint((i % perWord) * int(c.bits))
	mask := uint64(1)<<c.bits - 1
	w := &c.data[i/perWord]
	*w = (*w &^ (mask << shift)) | (uint64(v)&mask)<<shift
}

// wordsFor 返回容纳 4096 个 bits 位槽所需的 uint64 数量。
func wordsFor(bits uint8) int {
	perWord := 64 / int(bits)
	return (core.BlocksPerSection + perWord - 1) / perWord
}

// Set 写入局部坐标处的方块，必要时升级存储形态。
func (c *PalettedContainer) Set(x, y, z int, id BlockID) {
	if c.kind == kindSingle {
		if id == c.single {
			return // 值没变，保持单值态
		}
		c.upgradeToIndexed()
	}

	if c.kind == kindIndexed {
		slot, ok := c.lookup[id]
		if !ok {
			if len(c.palette) >= 1<<c.bits {
				if c.bits >= 8 {
					c.upgradeToDirect()
					c.writeRaw(blockIndex(x, y, z), uint32(id))
					return
				}
				c.growIndexed(8)
			}
			slot = uint32(len(c.palette))
			c.palette = append(c.palette, id)
			c.lookup[id] = slot
		}
		c.writeRaw(blockIndex(x, y, z), slot)
		return
	}

	c.writeRaw(blockIndex(x, y, z), uint32(id))
}

// upgradeToIndexed 把单值态升级为 4 位索引态。
func (c *PalettedContainer) upgradeToIndexed() {
	c.kind = kindIndexed
	c.bits = 4
	c.palette = []BlockID{c.single}
	c.lookup = map[BlockID]uint32{c.single: 0}
	c.data = make([]uint64, wordsFor(4))
	// 全 0 即全部指向调色板槽 0，也就是原来的 single，无需再填。
}

// growIndexed 把索引态的位宽扩大到 newBits，重新打包已有数据。
func (c *PalettedContainer) growIndexed(newBits uint8) {
	old := c.data
	oldBits := c.bits
	c.bits = newBits
	c.data = make([]uint64, wordsFor(newBits))

	oldPerWord := 64 / int(oldBits)
	mask := uint64(1)<<oldBits - 1
	for i := 0; i < core.BlocksPerSection; i++ {
		shift := uint((i % oldPerWord) * int(oldBits))
		c.writeRaw(i, uint32((old[i/oldPerWord]>>shift)&mask))
	}
}

// upgradeToDirect 把索引态升级为直接态，槽内改存全局 ID。
func (c *PalettedContainer) upgradeToDirect() {
	pal := c.palette
	oldBits := c.bits
	old := c.data

	c.kind = kindDirect
	c.bits = directBits
	c.data = make([]uint64, wordsFor(directBits))
	c.palette = nil
	c.lookup = nil

	oldPerWord := 64 / int(oldBits)
	mask := uint64(1)<<oldBits - 1
	for i := 0; i < core.BlocksPerSection; i++ {
		shift := uint((i % oldPerWord) * int(oldBits))
		slot := (old[i/oldPerWord] >> shift) & mask
		c.writeRaw(i, uint32(pal[slot]))
	}
}

// Compact 惰性降级：内容重新统一时退回单值态，
// 调色板实际用量缩小时退回更窄的位宽。
//
// 应在 tick 末对本 tick 被修改过的区段调用，而非每次 Set 都调用——
// 否则方块反复变更会在形态之间抖动。
func (c *PalettedContainer) Compact() {
	if c.kind == kindSingle {
		return
	}

	first := c.Get(0, 0, 0)
	uniform := true
	used := make(map[BlockID]struct{}, 16)
	for i := 0; i < core.BlocksPerSection; i++ {
		id := c.idAt(i)
		used[id] = struct{}{}
		if id != first {
			uniform = false
		}
	}

	if uniform {
		*c = PalettedContainer{kind: kindSingle, single: first}
		return
	}
	if len(used) > 256 {
		return // 仍需直接态
	}

	// 重建为最紧凑的索引态。遍历顺序取自 data 而非 map，保证确定性。
	rebuilt := &PalettedContainer{kind: kindIndexed, lookup: map[BlockID]uint32{}}
	if len(used) <= 16 {
		rebuilt.bits = 4
	} else {
		rebuilt.bits = 8
	}
	rebuilt.data = make([]uint64, wordsFor(rebuilt.bits))
	for i := 0; i < core.BlocksPerSection; i++ {
		id := c.idAt(i)
		slot, ok := rebuilt.lookup[id]
		if !ok {
			slot = uint32(len(rebuilt.palette))
			rebuilt.palette = append(rebuilt.palette, id)
			rebuilt.lookup[id] = slot
		}
		rebuilt.writeRaw(i, slot)
	}
	*c = *rebuilt
}

// idAt 返回线性索引处的全局方块 ID。
func (c *PalettedContainer) idAt(i int) BlockID {
	if c.kind == kindDirect {
		return BlockID(c.readRaw(i))
	}
	return c.palette[c.readRaw(i)]
}

// IsUniform 在容器处于单值态时返回该值与 true。
func (c *PalettedContainer) IsUniform() (BlockID, bool) {
	if c.kind == kindSingle {
		return c.single, true
	}
	return 0, false
}

// PayloadBytes 返回压缩 payload 的逻辑大小。
//
// 它不包含 Go 对象头、map bucket、allocator size class 等运行时开销，
// 只能比较压缩率，不能当作进程驻留内存。真实内存由 Task 17 采样 RSS。
func (c *PalettedContainer) PayloadBytes() int {
	n := len(c.data) * 8
	n += len(c.palette) * 2
	n += len(c.lookup) * 8 // map 开销的粗略估计
	return n
}

// Clone 返回一份深拷贝，供 COW 使用（spec §4.3）。
func (c *PalettedContainer) Clone() *PalettedContainer {
	cp := &PalettedContainer{
		kind:   c.kind,
		single: c.single,
		bits:   c.bits,
	}
	if c.data != nil {
		cp.data = make([]uint64, len(c.data))
		copy(cp.data, c.data)
	}
	if c.palette != nil {
		cp.palette = make([]BlockID, len(c.palette))
		copy(cp.palette, c.palette)
		cp.lookup = make(map[BlockID]uint32, len(c.lookup))
		for k, v := range c.lookup {
			cp.lookup[k] = v
		}
	}
	return cp
}
