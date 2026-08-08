package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	}
	if !seen["render.viewDistance"] {
		t.Fatal("Fields 必须包含 render.viewDistance")
	}
	for _, field := range fields {
		if field.Group == "render" && field.Name == "viewDistance" && !field.ReadOnly {
			t.Fatal("viewDistance 必须标记为只读（重启生效）")
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
