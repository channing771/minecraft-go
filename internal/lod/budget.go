package lod

// frameBudget 是远环 LOD 的自包含帧预算计数器,语义与近环
// internal/render 包的 `UploadBudget` 对齐(每帧重置、`tryConsume` 首个超预算请求
// 放行一次以防永久饥饿);因依赖方向铁律(internal/lod 不得 import
// internal/render)在本包独立实现,绝不与近环 `SectionScheduler` 共享实例。
// 生成派发与上传冲刷共用同一预算:每次派发按步长静态上界字节计费,
// 该上界同时覆盖本次生成的 CPU 分配与结果上传的字节足迹。
//
// 并发约束:仅帧线程(渲染循环)触碰,`beginFrame`/`tryConsume` 均不加锁。
type frameBudget struct {
	perFrame  uint32
	spent     uint32
	exhausted bool
}

// newFrameBudget 创建每帧 bytesPerFrame 字节的 LOD 预算。
func newFrameBudget(bytesPerFrame uint32) *frameBudget {
	return &frameBudget{perFrame: bytesPerFrame}
}

// beginFrame 重置本帧已消耗与耗尽标记。
func (b *frameBudget) beginFrame() {
	b.spent = 0
	b.exhausted = false
}

// tryConsume 申请消耗 bytes 字节并返回是否放行:已耗尽直接拒绝;超预算
// 请求在本帧尚无任何消耗时放行一次并标记耗尽,避免预算小于单次请求时
// 永久饥饿。比较用减法,避免 spent+bytes 的 uint32 加法溢出。
func (b *frameBudget) tryConsume(bytes uint32) bool {
	if b.exhausted {
		return false
	}
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
