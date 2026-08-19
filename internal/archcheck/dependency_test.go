package archcheck_test

import (
	"os/exec"
	"strings"
	"testing"
)

// allowed 列出每个内部包允许直接依赖的内部包。
//
// internal/server → internal/physics：M5B 任务编排（m5b-companion-planning-fifo）
// 经 sim.CompanionAction 提交伙伴移动输入，该 API 的字段类型就是 physics.Input；
// server 只消费物理的值类型与 ActiveTunables 快照（发令者射线的眼高），方向是
// 编排层依赖领域值类型，与 sim→physics 同向，不引入反向耦合。
//
// internal/sim → internal/fluid（变更 authoritative-fluid，design D2）：流动的调度
// 算法（待更新队列、全序排序、封闭盆地上的不动点收敛）被拆成独立包，使其能脱离权威
// 引擎被穷举性质测试；流动的数据所有权仍在权威模拟的 sim，fluid 只暴露
// Advance(now, FluidWorld, budget) 这样的无状态纯函数入口，不持有世界。
// fluid 只允许依赖 internal/core（当前实现未依赖 internal/world，即便设计意图允许，
// 也不预先登记未使用的边）；fluid MUST NOT 反向依赖 sim/network/render/storage，
// 否则它会退化成 sim 的内部实现，丧失独立测试的意义。
var allowed = map[string][]string{
	"internal/archcheck":  {},
	"internal/companion":  {"internal/core"},
	"internal/core":       {"internal/nativeabi"},
	"internal/nativeabi":  {},
	"internal/config":     {"internal/companion", "internal/core", "internal/physics", "internal/sim", "internal/logging"},
	"internal/fluid":      {"internal/core"},
	"internal/physics":    {"internal/core", "internal/nativeabi"},
	"internal/logging":    {},
	"internal/network":    {"internal/companion", "internal/core"},
	"internal/profile":    {"internal/core"},
	"internal/sim":        {"internal/companion", "internal/core", "internal/fluid", "internal/physics", "internal/world"},
	"internal/storage":    {"internal/companion", "internal/core", "internal/world"},
	"internal/world":      {"internal/core"},
	"internal/worldgen":   {"internal/core", "internal/world", "internal/nativeabi"},
	"internal/mesh":       {"internal/core", "internal/world", "internal/nativeabi"},
	"internal/assets":     {"internal/core", "internal/world", "internal/mesh", "internal/worldgen"},
	"internal/render":     {"internal/core", "internal/world", "internal/mesh", "internal/assets"},
	"internal/render/hud": {"internal/core", "internal/mesh", "internal/assets", "internal/render"},
	"internal/server":     {"internal/companion", "internal/core", "internal/network", "internal/physics", "internal/world", "internal/worldgen", "internal/sim", "internal/storage"},
	"internal/client":     {"internal/companion", "internal/core", "internal/physics", "internal/network", "internal/world", "internal/mesh", "internal/assets", "internal/render"},
}

func TestInternalDependenciesAreOneWay(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{.ImportPath}}|{{join .Imports \" \"}}", "./internal/...")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("枚举 internal 包失败: %v", err)
	}

	actual := make(map[string]bool)
	imports := make(map[string][]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 2)
		pkg := localName(parts[0])
		actual[pkg] = true
		if _, ok := allowed[pkg]; !ok {
			t.Errorf("新增内部包 %s 未登记依赖白名单", pkg)
			continue
		}
		if len(parts) == 2 {
			imports[pkg] = strings.Fields(parts[1])
		}
	}
	for pkg := range allowed {
		if !actual[pkg] {
			t.Errorf("依赖白名单中的包 %s 不存在", pkg)
		}
	}

	for pkg, packageImports := range imports {
		allowSet := make(map[string]bool, len(allowed[pkg]))
		for _, dependency := range allowed[pkg] {
			allowSet[dependency] = true
		}
		for _, importPath := range packageImports {
			dependency := localName(importPath)
			if dependency == importPath {
				continue
			}
			if !allowSet[dependency] {
				t.Errorf("%s 不允许直接依赖 %s", pkg, dependency)
			}
		}
	}
}
