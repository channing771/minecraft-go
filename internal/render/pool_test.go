package render_test

import (
	"math"
	"testing"

	"github.com/channing771/mornlea/internal/render"
)

func TestUploadBudgetLimitsPerFrame(t *testing.T) {
	b := render.NewUploadBudget(1000)
	b.BeginFrame()
	if !b.TryConsume(600) || !b.TryConsume(400) {
		t.Fatal("累计 1000 应在预算内")
	}
	if b.TryConsume(1) {
		t.Fatal("超出预算后应拒绝")
	}
	b.BeginFrame()
	if !b.TryConsume(1000) {
		t.Fatal("新一帧预算应重置")
	}
}

func TestUploadBudgetAllowsOversizedSingleItem(t *testing.T) {
	b := render.NewUploadBudget(100)
	b.BeginFrame()
	if !b.TryConsume(5000) {
		t.Fatal("一帧内第一个请求即使超预算也应放行")
	}
	if b.TryConsume(1) {
		t.Fatal("放行超预算请求后，本帧不应再接受任何上传")
	}
}

func TestUploadBudgetHandlesUint32Overflow(t *testing.T) {
	b := render.NewUploadBudget(math.MaxUint32)
	b.BeginFrame()
	if !b.TryConsume(math.MaxUint32 - 5) {
		t.Fatal("接近 MaxUint32 的首个请求应成功")
	}
	if b.TryConsume(10) {
		t.Fatal("加法溢出不能绕过预算")
	}
}
