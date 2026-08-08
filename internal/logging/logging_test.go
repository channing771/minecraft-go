package logging

import (
	"bytes"
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"
)

// callerRecord 构造一条 PC 指向本测试文件的记录，其模块名应为 logging。
func callerRecord(level slog.Level, message string) slog.Record {
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	return slog.NewRecord(time.Now(), level, message, pcs[0])
}

func newTestHandler(config Config) (slog.Handler, *bytes.Buffer) {
	var buffer bytes.Buffer
	inner := slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})
	return New(inner, config), &buffer
}

func TestGlobalLevelDropsLowerRecords(t *testing.T) {
	handler, buffer := newTestHandler(Config{Default: slog.LevelInfo})
	if handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("全局 info 下 debug 必须被 Enabled 快速门拒绝")
	}
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelDebug, "掉弃")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("低于全局等级的记录不该输出，实际 %q", buffer.String())
	}
}

func TestModuleOverrideLoosensSingleModule(t *testing.T) {
	handler, buffer := newTestHandler(Config{
		Default: slog.LevelInfo,
		Modules: map[string]slog.Level{"logging": slog.LevelDebug},
	})
	if !handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("存在放宽到 debug 的模块时，Enabled 快速门必须放行 debug")
	}
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelDebug, "放行")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(buffer.String(), "放行") {
		t.Fatalf("被放宽模块的 debug 必须输出，实际 %q", buffer.String())
	}
}

func TestModuleOverrideDoesNotLeakToOtherModules(t *testing.T) {
	handler, buffer := newTestHandler(Config{
		Default: slog.LevelInfo,
		Modules: map[string]slog.Level{"gfx": slog.LevelDebug},
	})
	// 记录来自 logging 包，不是被放宽的 gfx。
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelDebug, "不该出现")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("未被放宽的模块 debug 不该输出，实际 %q", buffer.String())
	}
}

func TestModuleOverrideTighttensSingleModule(t *testing.T) {
	handler, buffer := newTestHandler(Config{
		Default: slog.LevelInfo,
		Modules: map[string]slog.Level{"logging": slog.LevelError},
	})
	if err := handler.Handle(context.Background(), callerRecord(slog.LevelWarn, "不该出现")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("被收紧模块的 warn 不该输出，实际 %q", buffer.String())
	}
}

func TestModuleForPC(t *testing.T) {
	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])
	if module := moduleForPC(pcs[0]); module != "logging" {
		t.Fatalf("模块名 = %q，want logging", module)
	}
	if module := moduleForPC(0); module != "" {
		t.Fatalf("零 PC 的模块名 = %q，want 空", module)
	}
}

func TestParseLevel(t *testing.T) {
	for text, want := range map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug,
		"info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
	} {
		got, err := ParseLevel(text)
		if err != nil {
			t.Fatalf("ParseLevel(%q): %v", text, err)
		}
		if got != want {
			t.Fatalf("ParseLevel(%q) = %v，want %v", text, got, want)
		}
	}
	if _, err := ParseLevel("verbose"); err == nil {
		t.Fatal("未知等级必须报错")
	}
}

func TestWithAttrsPreservesFiltering(t *testing.T) {
	handler, buffer := newTestHandler(Config{Default: slog.LevelInfo})
	derived := handler.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if err := derived.Handle(context.Background(), callerRecord(slog.LevelDebug, "掉弃")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if buffer.Len() != 0 {
		t.Fatalf("WithAttrs 派生的 handler 必须保持过滤，实际 %q", buffer.String())
	}
}
