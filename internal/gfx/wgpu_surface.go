//go:build darwin

package gfx

import (
	"fmt"
	"log/slog"

	"github.com/oliverbestmann/webgpu/wgpu"
)

// ---------------------------------------------------------------------------
// Surface
// ---------------------------------------------------------------------------

type wgpuSurface struct {
	device       *wgpuDevice
	surface      *wgpu.Surface
	config       *wgpu.SurfaceConfiguration
	presentModes []wgpu.PresentMode
	// format 是 config.Format 翻译成 gfx 中立枚举后的结果，NewDevice 里解析好。
	format TextureFormat

	// 当前帧已取得但尚未 Present 的纹理与视图，Present 时一并释放。
	frameTexture *wgpu.Texture
	frameView    *wgpu.TextureView
}

// Acquire 取当前帧的颜色附件视图。返回的视图归 Surface 所有，
// Present 时会连同底层 surface 纹理一起释放，调用方不必也不应释放它。
func (s *wgpuSurface) Acquire() TextureView {
	if s.frameTexture != nil {
		panic("gfx: 上一帧的 surface 纹理还没 Present，不能再次 Acquire")
	}

	st, err := s.surface.TryGetCurrentTexture()
	if err != nil {
		// 取纹理失败是瞬时状况（超时、surface 过期），跳过这一帧即可。
		slog.Warn("获取 surface 纹理失败，跳过本帧", "error", err)
		return nil
	}
	texture, ok := st.Get()
	if !ok {
		// 窗口被遮挡、最小化或 surface 已过期。
		return nil
	}

	view, err := texture.TryCreateView(nil)
	if err != nil {
		texture.Release()
		slog.Warn("创建 surface 纹理视图失败，跳过本帧", "error", err)
		return nil
	}

	s.frameTexture = texture
	s.frameView = view
	return &wgpuTextureView{view: view}
}

// Present 呈现当前帧。必须在 Submit 之后调用——wgpu-native 要求
// 先 Present 再释放 surface 纹理，提前释放会触发验证层报错。
func (s *wgpuSurface) Present() {
	if s.frameTexture == nil {
		return
	}
	s.surface.Present()
	s.frameView.Release()
	s.frameTexture.Release()
	s.frameView = nil
	s.frameTexture = nil
}

func (s *wgpuSurface) SetPresentMode(m PresentMode) error {
	// 绑定不提供"自动 VSync / 自动非 VSync"的封装，只能自己从 caps 里挑。
	// Metal 后端实测只上报 [fifo immediate]，没有 mailbox。
	var want wgpu.PresentMode
	switch m {
	case PresentModeAutoVSync:
		want = wgpu.PresentModeFifo
	case PresentModeAutoNoVSync:
		want = wgpu.PresentModeImmediate
	default:
		return fmt.Errorf("gfx: 未知的 present 模式 %d", m)
	}

	supported := false
	for _, pm := range s.presentModes {
		if pm == want {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("gfx: surface 不支持 present 模式 %v（可用：%v）", want, s.presentModes)
	}

	s.config.PresentMode = want
	s.surface.Configure(s.device.device, s.config)
	return nil
}

func (s *wgpuSurface) Resize(width, height uint32) {
	if width == 0 || height == 0 {
		// 窗口最小化时帧缓冲尺寸会变成 0，此时重新配置 surface 是非法的。
		return
	}
	if s.config.Width == width && s.config.Height == height {
		return
	}
	s.config.Width = width
	s.config.Height = height
	s.surface.Configure(s.device.device, s.config)
}

// Format 返回 surface 的颜色格式，用于建管线时填 ColorFormat。
func (s *wgpuSurface) Format() TextureFormat { return s.format }

func (s *wgpuSurface) Release() {
	if s.frameView != nil {
		s.frameView.Release()
		s.frameView = nil
	}
	if s.frameTexture != nil {
		s.frameTexture.Release()
		s.frameTexture = nil
	}
	if s.surface != nil {
		s.surface.Release()
		s.surface = nil
	}
	s.config = nil
}
