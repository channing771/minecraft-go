// Package render 保留渲染的 CPU 半部:布局、编码与上传调度。
// GPU pass 与资源自 R2c 起由 Rust `mornlea_client` 渲染器独占。
package render

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
