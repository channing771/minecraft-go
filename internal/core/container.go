package core

// ContainerKind 标识一个容器引用指向区块的哪个固定容器数组。
// 零值必须表示熔炉：既有代码里大量 core.FurnaceRef{...} 字面量不设置 Kind 字段，
// 它们必须继续被解释为熔炉，而不是被新增的箱子种类误判。
type ContainerKind uint8

const (
	// ContainerKindFurnace 是 ContainerKind 的零值，代表区块的固定熔炉数组。
	ContainerKindFurnace ContainerKind = iota
	// ContainerKindChest 代表区块的固定箱子数组。
	ContainerKindChest
)

// ContainerRef 在容器的生命周期内唯一且稳定地标识它，熔炉与箱子共用同一结构，
// 因此线上与内存中只保留一份引用编解码与比较逻辑。
// 槽位复用时 Generation 递增，因此旧引用不会与新容器冲突。
type ContainerRef struct {
	Dimension  DimensionID
	Chunk      ChunkPos
	Kind       ContainerKind
	Slot       uint8
	Generation uint32
}
