// Package logging 提供按模块分级过滤的 slog handler。
//
// 模块名从记录的调用点 PC 反查包路径末段得到（minecraft-go/internal/sim → sim），
// 因此日志调用点不需要显式声明自己属于哪个模块，新写的日志也自动归入正确模块。
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
)

// Config 是日志等级配置。Modules 的键是模块名，即包路径末段。
//
// json tag 让 Save 写出的键名与设计文档、README 一致的小写驼峰；读取侧由
// internal/config 自行大小写不敏感地解析，因此本次加 tag 之前写出的文件仍
// 可正常读入。
type Config struct {
	Default slog.Level            `json:"default"`
	Modules map[string]slog.Level `json:"modules"`
}

// ParseLevel 把配置文件中的等级文本解析为 slog.Level，大小写不敏感。
func ParseLevel(text string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("logging: 未知日志等级 %q", text)
	}
}

type handler struct {
	inner    slog.Handler
	config   Config
	minLevel slog.Level
}

// New 用 config 包装 inner。
//
// minLevel 取全局等级与所有模块等级中的最小值，供 Enabled 做快速门：没有任何模块
// 被放宽时，低于全局等级的记录在这里就被拒绝，不产生记录也不做模块反查。只有当某个
// 模块被显式放宽时，Handle 才会为通过快速门的记录做一次 runtime.CallersFrames。
func New(inner slog.Handler, config Config) slog.Handler {
	minimum := config.Default
	for _, level := range config.Modules {
		if level < minimum {
			minimum = level
		}
	}
	return &handler{inner: inner, config: config, minLevel: minimum}
}

// Install 把包装后的 handler 装为进程默认 logger。只应由 cmd 启动装配调用。
func Install(inner slog.Handler, config Config) {
	slog.SetDefault(slog.New(New(inner, config)))
}

func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.minLevel
}

func (h *handler) Handle(ctx context.Context, record slog.Record) error {
	threshold := h.config.Default
	if len(h.config.Modules) != 0 {
		if level, ok := h.config.Modules[moduleForPC(record.PC)]; ok {
			threshold = level
		}
	}
	if record.Level < threshold {
		return nil
	}
	return h.inner.Handle(ctx, record)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{inner: h.inner.WithAttrs(attrs), config: h.config, minLevel: h.minLevel}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name), config: h.config, minLevel: h.minLevel}
}

// moduleForPC 从调用点 PC 反查模块名，取包路径末段。
// frame.Function 形如 minecraft-go/internal/sim.(*Engine).Step，返回 sim。
// 反查不出时返回空串，此时按全局等级处理。
func moduleForPC(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{pc}).Next()
	name := frame.Function
	if name == "" {
		return ""
	}
	if slash := strings.LastIndex(name, "/"); slash >= 0 {
		name = name[slash+1:]
	}
	if dot := strings.Index(name, "."); dot >= 0 {
		name = name[:dot]
	}
	return name
}
