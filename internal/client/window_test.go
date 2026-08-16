//go:build darwin

package client

import (
	"encoding/binary"
	"math"
	"testing"
)

// buildSnapshot 构造一份合法快照字节,便于无头验证解码与缓存语义。
func buildSnapshot(mutate func([]byte)) []byte {
	buf := make([]byte, snapshotBytes)
	binary.LittleEndian.PutUint32(buf[0:4], snapshotLayoutVersion)
	if mutate != nil {
		mutate(buf)
	}
	return buf
}

func TestClientABIVersionMatchesHeader(t *testing.T) {
	// v7:雾参数化 render_set_lod_fog 出口加入;新增导出面即 bump。变基重编:
	// v5 归 main 的 water pass,远环 tile 出口 v6、雾 setter v7。
	if got := ClientABIVersion(); got != 7 {
		t.Fatalf("client ABI version=%d,想要 7", got)
	}
}

func TestWindowMapsBackspaceAndReportsTextOverflow(t *testing.T) {
	// 键位 iota 稳定性:位序即快照 bitmask 契约,Backspace 必须保持在末尾。
	if KeyBackspace != KeyLeftAlt+1 || int(KeyBackspace) != 28 {
		t.Fatalf("Backspace iota=%d", KeyBackspace)
	}
	var window Window
	for range 1024 {
		window.enqueueTextInput('a')
	}
	window.enqueueTextInput('b')
	got, overflow := window.DrainTextInput(make([]rune, 0, 1024))
	if len(got) != 1024 || !overflow || got[1023] != 'a' {
		t.Fatalf("DrainTextInput len=%d overflow=%v tail=%q", len(got), overflow, got[1023])
	}
	if got, overflow := window.DrainTextInput(got[:0]); len(got) != 0 || overflow {
		t.Fatalf("second drain len=%d overflow=%v", len(got), overflow)
	}
}

func TestDecodeSnapshotCachesAllFields(t *testing.T) {
	buf := buildSnapshot(func(buf []byte) {
		binary.LittleEndian.PutUint32(buf[4:8], 1) // should_close
		binary.LittleEndian.PutUint64(buf[8:16], 1<<KeyW|1<<KeyBackspace)
		binary.LittleEndian.PutUint32(buf[16:20], 0b11)
		binary.LittleEndian.PutUint32(buf[20:24], 2)
		binary.LittleEndian.PutUint64(buf[24:32], math.Float64bits(12.5))
		binary.LittleEndian.PutUint64(buf[32:40], math.Float64bits(-3.0))
		binary.LittleEndian.PutUint32(buf[40:44], 2560)
		binary.LittleEndian.PutUint32(buf[44:48], 1440)
		binary.LittleEndian.PutUint32(buf[48:52], 1280)
		binary.LittleEndian.PutUint32(buf[52:56], 720)
		binary.LittleEndian.PutUint32(buf[64:68], uint32('你'))
		binary.LittleEndian.PutUint32(buf[68:72], uint32('A'))
	})

	var window Window
	window.state = decodeSnapshot(buf, window.enqueueTextInput, func() { window.textInputOverflow = true })

	if !window.ShouldClose() {
		t.Fatal("should_close 标志未解码")
	}
	if !window.KeyDown(KeyW) || !window.KeyDown(KeyBackspace) || window.KeyDown(KeyA) {
		t.Fatal("键位 bitmask 解码错误")
	}
	if !window.PrimaryButtonDown() || !window.SecondaryButtonDown() {
		t.Fatal("鼠标键位解码错误")
	}
	if x, y := window.CursorPos(); x != 12.5 || y != -3.0 {
		t.Fatalf("光标位置=(%f,%f)", x, y)
	}
	if w, h := window.FramebufferSize(); w != 2560 || h != 1440 {
		t.Fatalf("framebuffer=(%d,%d)", w, h)
	}
	if w, h := window.ContentSize(); w != 1280 || h != 720 {
		t.Fatalf("content=(%d,%d)", w, h)
	}
	text, overflow := window.DrainTextInput(nil)
	if string(text) != "你A" || overflow {
		t.Fatalf("text=%q overflow=%v", string(text), overflow)
	}
}

func TestDecodeSnapshotOverflowFlagSetsQueueOverflow(t *testing.T) {
	buf := buildSnapshot(func(buf []byte) {
		binary.LittleEndian.PutUint32(buf[4:8], 2) // text_overflow
	})
	var window Window
	window.state = decodeSnapshot(buf, window.enqueueTextInput, func() { window.textInputOverflow = true })
	if _, overflow := window.DrainTextInput(nil); !overflow {
		t.Fatal("快照 overflow 标志必须传导到队列")
	}
}

func TestDecodeSnapshotRejectsMalformedInput(t *testing.T) {
	for name, buf := range map[string][]byte{
		"短缓冲":  make([]byte, snapshotBytes-1),
		"布局版本": buildSnapshot(func(buf []byte) { binary.LittleEndian.PutUint32(buf[0:4], 2) }),
		"文本计数": buildSnapshot(func(buf []byte) { binary.LittleEndian.PutUint32(buf[20:24], textCapacity+1) }),
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("非法快照必须 panic")
				}
			}()
			decodeSnapshot(buf, func(rune) {}, func() {})
		})
	}
}
