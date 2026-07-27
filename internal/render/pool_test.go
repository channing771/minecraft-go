package render_test

import (
	"math"
	"math/rand"
	"testing"

	"minecraft-go/internal/render"
)

func TestPoolAllocAndFree(t *testing.T) {
	p := render.NewPool(1000)
	a, ok := p.Alloc(300)
	if !ok || a.Size != 300 {
		t.Fatalf("首次分配失败: %+v ok=%v", a, ok)
	}
	b, ok := p.Alloc(300)
	if !ok || b.Offset == a.Offset {
		t.Fatalf("第二次分配与第一次重叠: a=%+v b=%+v", a, b)
	}
	if p.Used() != 600 {
		t.Fatalf("Used = %d，想要 600", p.Used())
	}
	p.Free(a)
	if p.Used() != 300 {
		t.Fatalf("释放后 Used = %d，想要 300", p.Used())
	}
}

func TestPoolRejectsOversizedRequest(t *testing.T) {
	p := render.NewPool(100)
	if _, ok := p.Alloc(101); ok {
		t.Fatal("超出容量的请求应当失败")
	}
	if _, ok := p.Alloc(0); ok {
		t.Fatal("零大小请求应当失败")
	}
}

func TestPoolCoalescesAdjacentFreeBlocks(t *testing.T) {
	p := render.NewPool(300)
	a, _ := p.Alloc(100)
	b, _ := p.Alloc(100)
	c, _ := p.Alloc(100)
	p.Free(a)
	p.Free(c)
	p.Free(b)
	if got := p.LargestFree(); got != 300 {
		t.Fatalf("合并后最大空闲块 = %d，想要 300", got)
	}
	if _, ok := p.Alloc(300); !ok {
		t.Fatal("合并后应能一次分配出整个池")
	}
}

func TestPoolRandomChurnNeverOverlaps(t *testing.T) {
	const capacity = 4096
	p := render.NewPool(capacity)
	rng := rand.New(rand.NewSource(7))
	live := map[uint32]render.Alloc{}

	for step := 0; step < 50000; step++ {
		if len(live) > 0 && rng.Intn(2) == 0 {
			for k, v := range live {
				p.Free(v)
				delete(live, k)
				break
			}
			continue
		}
		size := uint32(rng.Intn(64) + 1)
		a, ok := p.Alloc(size)
		if !ok {
			continue
		}
		if a.Offset+a.Size > capacity {
			t.Fatalf("分配越界: %+v，容量 %d", a, capacity)
		}
		for _, other := range live {
			if a.Offset < other.Offset+other.Size && other.Offset < a.Offset+a.Size {
				t.Fatalf("分配重叠: %+v 与 %+v", a, other)
			}
		}
		live[a.Offset] = a
	}
}

func TestPoolFragmentation(t *testing.T) {
	p := render.NewPool(400)
	a, _ := p.Alloc(100)
	b, _ := p.Alloc(100)
	c, _ := p.Alloc(100)
	p.Free(a)
	// 空闲为 [0,100) 与 [300,400)，共 200，最大 100，碎片率 0.5。
	if got := p.Fragmentation(); math.Abs(float64(got-0.5)) > 1e-6 {
		t.Fatalf("Fragmentation = %f，想要 0.5", got)
	}
	p.Free(b)
	p.Free(c)
	if got := p.Fragmentation(); got != 0 {
		t.Fatalf("全部释放并合并后 Fragmentation = %f，想要 0", got)
	}
}

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
