package mesh

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// fullCapacityRegistry 的快照条目数正好等于 nativeMaxRegistryEntries：ID 取
// 0..nativeMaxRegistryEntries-1（不要求全部已注册——mesh 侧只把 ID 当数字），
// 其余属性沿用 internalTestRegistry 的规则。
type fullCapacityRegistry struct{ internalTestRegistry }

func (r fullCapacityRegistry) MeshSnapshot() RegistrySnapshot {
	ids := make([]world.BlockID, nativeMaxRegistryEntries)
	for i := range ids {
		ids[i] = world.BlockID(i)
	}
	snapshot, err := BuildRegistrySnapshot(ids, r)
	if err != nil {
		panic(err)
	}
	return snapshot
}

// TestRegistryCapacityCoversEveryRegisteredBlock 是 registry 容量的**位置性**
// 断言：已注册方块数 core.BlockIDMax 必须不超过条目上限。
//
// 为什么必须单独写这一条：assets.NewRegistry() 会把全部已注册方块烘焙进 mesh
// snapshot，条目数一旦超过上限，encodeNativeInput 会拒绝**每一次**网格化调用，
// 症状是各处 panic 而不是一条指向容量的诊断。断言比的是大小（覆盖关系）而不是
// 某个常量恰好等于某个数字，因此追加方块编号时它会在真正越界的那一刻才变红。
func TestRegistryCapacityCoversEveryRegisteredBlock(t *testing.T) {
	if int(core.BlockIDMax) > nativeMaxRegistryEntries {
		t.Fatalf("registry 容量不足：已注册方块 %d 个（core.BlockIDMax），"+
			"nativeMaxRegistryEntries = %d；扩容必须同批移动 Rust 的 "+
			"MAX_REGISTRY_ENTRIES、Go 的 nativeMaxRegistryEntries 与 maxNativeInputBytes",
			int(core.BlockIDMax), nativeMaxRegistryEntries)
	}
}

// TestNativeAcceptsRegistryAtGoCapacity 跨语言钉住 registry 容量：把正好装满
// nativeMaxRegistryEntries 条的快照真的喂进 Rust。Rust 的 MAX_REGISTRY_ENTRIES
// 一旦小于 Go 侧上限，MeshInput::parse 会拒绝整次调用、meshSectionNative 随之
// panic，这里把那个 panic 翻译成一条明确指向容量不同步的诊断——否则症状只是
// 各个不相干的网格化用例集体 panic 在一句通用的 native 状态文本上。
//
// 两侧常量没有共享定义也没有生成步骤，只能人手同步，本用例是唯一的机械守卫。
func TestNativeAcceptsRegistryAtGoCapacity(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	// 中心区段必须非全空气，否则 meshSectionNative 会走 uniform-air 短路，
	// 根本不会调用到 Rust，本用例就成了恒真的空转。
	n.Center.Blocks.Set(8, 8, 8, core.StoneID)
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("Rust 拒绝了装满 %d 条的 registry：MAX_REGISTRY_ENTRIES 与 Go 的 "+
				"nativeMaxRegistryEntries 不同步（panic: %v）", nativeMaxRegistryEntries, recovered)
		}
	}()
	MeshSection(n, fullCapacityRegistry{}, NewLightScratch())
}
