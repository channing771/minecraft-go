package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"minecraft-go/internal/config"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/sim"
)

// 注意：config.Config 内嵌的 logging.Config 含 map 字段，因此 Config 整体
// **不可比较**，不能用 == 断言。涉及整体比较一律用 reflect.DeepEqual。
// 不含 map 的 physics.Tunables 与 sim.Tunables 仍可直接用 ==。

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写测试配置: %v", err)
	}
	return path
}

func TestMissingFileYieldsDefaults(t *testing.T) {
	loaded, err := config.Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("文件不存在不应报错: %v", err)
	}
	if !reflect.DeepEqual(loaded, config.Defaults()) {
		t.Fatal("文件不存在时必须返回全默认配置")
	}
}

func TestMissingFileIsNotCreated(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "absent.json")
	if _, err := config.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Load 不得创建配置文件")
	}
}

func TestMissingFieldsFallBackToDefaults(t *testing.T) {
	path := writeConfig(t, `{"version":1,"physics":{"gravity":20}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Physics.Gravity != 20 {
		t.Fatalf("Gravity = %v，want 20", loaded.Physics.Gravity)
	}
	if loaded.Physics.JumpSpeed != physics.DefaultTunables().JumpSpeed {
		t.Fatal("未出现的字段必须保持默认值")
	}
	if loaded.Sim != sim.DefaultTunables() {
		t.Fatal("未出现的分组必须整组保持默认值")
	}
}

func TestOutOfRangeValuesAreClamped(t *testing.T) {
	path := writeConfig(t, `{"version":1,"sim":{"spawnRadius":100000}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("越界值必须钳制而不是报错: %v", err)
	}
	if loaded.Sim.SpawnRadius > 64 {
		t.Fatalf("SpawnRadius = %v，必须钳制到上界 64", loaded.Sim.SpawnRadius)
	}
}

// TestOutOfRangeIntegerValuesAreClampedNotRejected 覆盖评审 Finding 1：
// encoding/json 对窄整数字段（uint8/uint32/int32）自带的范围检查曾经先于
// clampFields 触发，导致手改配置文件多写一个越界数字就让 Load 直接报错。
// 这里逐一验证越界/负值/小数都被钳制而不是让 Load 失败。
func TestOutOfRangeIntegerValuesAreClampedNotRejected(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		check func(t *testing.T, cfg config.Config)
	}{
		{
			name: "uint8字段超过上界255",
			body: `{"version":1,"sim":{"dropPickupDelayTicks":300}}`,
			check: func(t *testing.T, cfg config.Config) {
				if cfg.Sim.DropPickupDelayTicks != 255 {
					t.Fatalf("DropPickupDelayTicks = %v，want 255", cfg.Sim.DropPickupDelayTicks)
				}
			},
		},
		{
			name: "uint8字段为负数",
			body: `{"version":1,"sim":{"playerDropPickupDelayTicks":-5}}`,
			check: func(t *testing.T, cfg config.Config) {
				if cfg.Sim.PlayerDropPickupDelayTicks != 0 {
					t.Fatalf("PlayerDropPickupDelayTicks = %v，want 0", cfg.Sim.PlayerDropPickupDelayTicks)
				}
			},
		},
		{
			name: "uint32字段为负数触及硬约束下限",
			body: `{"version":1,"sim":{"regenIntervalTicks":-1}}`,
			check: func(t *testing.T, cfg config.Config) {
				if cfg.Sim.RegenIntervalTicks != 1 {
					t.Fatalf("RegenIntervalTicks = %v，want 1（硬约束下限）", cfg.Sim.RegenIntervalTicks)
				}
			},
		},
		{
			name: "int32字段区间内的小数不报错",
			body: `{"version":1,"sim":{"spawnRadius":3.7}}`,
			check: func(t *testing.T, cfg config.Config) {
				if cfg.Sim.SpawnRadius < 1 || cfg.Sim.SpawnRadius > 64 {
					t.Fatalf("SpawnRadius = %v，超出区间 [1,64]", cfg.Sim.SpawnRadius)
				}
			},
		},
		{
			name: "furnaceSmeltTicks超过core.FurnaceSmeltTicks上限",
			body: `{"version":1,"sim":{"furnaceSmeltTicks":300}}`,
			check: func(t *testing.T, cfg config.Config) {
				if cfg.Sim.FurnaceSmeltTicks != 200 {
					t.Fatalf("FurnaceSmeltTicks = %v，want 200（硬约束上限 core.FurnaceSmeltTicks）", cfg.Sim.FurnaceSmeltTicks)
				}
			},
		},
		{
			name: "furnaceBurnTicks超过core.FurnaceBurnTicks上限",
			body: `{"version":1,"sim":{"furnaceBurnTicks":70000}}`,
			check: func(t *testing.T, cfg config.Config) {
				if cfg.Sim.FurnaceBurnTicks != 1600 {
					t.Fatalf("FurnaceBurnTicks = %v，want 1600（硬约束上限 core.FurnaceBurnTicks）", cfg.Sim.FurnaceBurnTicks)
				}
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeConfig(t, testCase.body)
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatalf("越界整数必须被钳制而不是让 Load 报错: %v", err)
			}
			testCase.check(t, loaded)
		})
	}
}

func TestUnknownFieldsAreIgnored(t *testing.T) {
	path := writeConfig(t, `{"version":1,"physics":{"antigravity":true}}`)
	if _, err := config.Load(path); err != nil {
		t.Fatalf("未知字段必须忽略而不是报错: %v", err)
	}
}

func TestMalformedJSONFails(t *testing.T) {
	path := writeConfig(t, `{"version":1,`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("JSON 语法错误必须报错")
	}
}

func TestUnknownVersionFails(t *testing.T) {
	path := writeConfig(t, `{"version":99}`)
	if _, err := config.Load(path); err == nil {
		t.Fatal("不认识的 version 必须报错")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	want := config.Defaults()
	want.Physics.Gravity = 24
	want.Render.FovDegrees = 90
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip 不一致：%+v != %+v", got, want)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	if err := config.Defaults().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("保存后目录必须只剩目标文件，实际 %v", entries)
	}
	// 保存产物必须是合法 JSON。
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var probe map[string]any
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatalf("保存产物不是合法 JSON: %v", err)
	}
}

func TestFieldsCoverEveryTunable(t *testing.T) {
	fields := config.Fields()
	if len(fields) == 0 {
		t.Fatal("Fields 不得为空")
	}
	seen := make(map[string]bool, len(fields))
	byGroup := map[string]map[string]bool{"physics": {}, "sim": {}, "render": {}}
	for _, field := range fields {
		key := field.Group + "." + field.Name
		if seen[key] {
			t.Fatalf("重复字段 %s", key)
		}
		seen[key] = true
		if field.Min >= field.Max {
			t.Fatalf("%s 的区间非法：[%v, %v]", key, field.Min, field.Max)
		}
		if field.Step <= 0 {
			t.Fatalf("%s 的步长必须为正，实际 %v", key, field.Step)
		}
		names, ok := byGroup[field.Group]
		if !ok {
			t.Fatalf("Fields 出现未知分组 %s", field.Group)
		}
		names[field.Name] = true
	}
	if !seen["render.viewDistance"] {
		t.Fatal("Fields 必须包含 render.viewDistance")
	}
	for _, field := range fields {
		if field.Group == "render" && field.Name == "viewDistance" && !field.ReadOnly {
			t.Fatal("viewDistance 必须标记为只读（重启生效）")
		}
	}

	// 反射校验 Fields() 与 physics.Tunables / sim.Tunables / config.Render 的
	// 真实字段一一对应：任何一边多一个或少一个字段都必须被测试暴露出来。
	// 这条覆盖是 Finding 1 那类问题的根本防线——不校验覆盖度的话，往
	// sim.Tunables 加字段或者把 Fields() 里的 Name 敲错一个字母，那个字段就
	// 永久漏过钳制，而这个测试原本会一直是绿的。
	assertFieldsMatchStruct(t, "physics", reflect.TypeOf(physics.Tunables{}), byGroup["physics"])
	assertFieldsMatchStruct(t, "sim", reflect.TypeOf(sim.Tunables{}), byGroup["sim"])
	assertFieldsMatchStruct(t, "render", reflect.TypeOf(config.Render{}), byGroup["render"])
}

// assertFieldsMatchStruct 双向校验：structType 的每个字段都在 fieldNames 中
// 以小写开头的驼峰名出现过，且 fieldNames 中的每一项都能在 structType 上找到
// 对应的导出字段（首字母大写）。
func assertFieldsMatchStruct(t *testing.T, group string, structType reflect.Type, fieldNames map[string]bool) {
	t.Helper()
	if structType.NumField() != len(fieldNames) {
		t.Errorf("%s: Fields() 有 %d 项，%s 结构体有 %d 个字段，数量不匹配",
			group, len(fieldNames), structType.Name(), structType.NumField())
	}
	for i := 0; i < structType.NumField(); i++ {
		goName := structType.Field(i).Name
		lowerCamel := strings.ToLower(goName[:1]) + goName[1:]
		if !fieldNames[lowerCamel] {
			t.Errorf("%s: 结构体字段 %s 没有出现在 Fields() 中（期望 Name=%q）", group, goName, lowerCamel)
		}
	}
	for name := range fieldNames {
		exported := strings.ToUpper(name[:1]) + name[1:]
		if _, ok := structType.FieldByName(exported); !ok {
			t.Errorf("%s: Fields() 中的 %q 在结构体里找不到对应字段 %s", group, name, exported)
		}
	}
}

func TestApplySetsActiveTunables(t *testing.T) {
	t.Cleanup(func() { config.Defaults().Apply() })

	custom := config.Defaults()
	custom.Physics.Gravity = 24
	custom.Sim.InteractionReach = 4
	custom.Apply()

	if physics.ActiveTunables().Gravity != 24 {
		t.Fatal("Apply 必须写入 physics 生效参数")
	}
	if sim.ActiveTunables().InteractionReach != 4 {
		t.Fatal("Apply 必须写入 sim 生效参数")
	}
}
