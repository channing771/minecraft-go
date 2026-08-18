package config_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/sim"
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

func TestConfigAICompanionUnknownFieldsWarnAndIgnore(t *testing.T) {
	previous := slog.Default()
	var records bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&records, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	// M5B 起 ai.endpoint/ai.model 是已识别字段；M5D 起 companions[].persona
	// 也已识别并解析进 Definition（不再告警）。用合法模型字段（loopback http
	// 免密钥）构造配置，未知字段告警断言只保留 task 一项。
	path := writeConfig(t, `{"version":1,"ai":{"endpoint":"http://127.0.0.1:1/v1","model":"test-model","companions":[`+
		`{"id":"00112233-4455-4677-8899-aabbccddeeff","name":"阿木","persona":"later","task":"later"}]}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	definitions := loaded.CompanionDefinitions()
	if len(definitions) != 1 || definitions[0].Name != "阿木" || definitions[0].ID.String() != "00112233-4455-4677-8899-aabbccddeeff" {
		t.Fatalf("CompanionDefinitions = %+v", definitions)
	}
	if definitions[0].Persona != "later" {
		t.Fatalf("persona 已是已识别字段，应解析进定义：got %q", definitions[0].Persona)
	}
	for _, path := range []string{"ai.companions[0].task"} {
		if !strings.Contains(records.String(), `"field":"`+path+`"`) {
			t.Errorf("未知字段日志 %q 缺少精确路径 %q", records.String(), path)
		}
	}
	if strings.Contains(records.String(), `"field":"ai.companions[0].persona"`) {
		t.Errorf("persona 不应再触发未知字段告警: %s", records.String())
	}
}

func TestConfigAIDisabledFormsAndDefinitionValidation(t *testing.T) {
	for _, body := range []string{
		`{"version":1}`,
		`{"version":1,"ai":null}`,
		`{"version":1,"ai":{}}`,
		`{"version":1,"ai":{"companions":null}}`,
		`{"version":1,"ai":{"companions":[]}}`,
	} {
		loaded, err := config.Load(writeConfig(t, body))
		if err != nil || len(loaded.CompanionDefinitions()) != 0 {
			t.Fatalf("Load(%s) = %+v, %v，want AI disabled", body, loaded.CompanionDefinitions(), err)
		}
	}

	if _, err := config.Load(writeConfig(t, `{"version":1,"ai":{"companions":[`+
		`{"id":"00112233-4455-4677-8899-aabbccddeeff","name":"A"},`+
		`{"id":"10112233-4455-4677-8899-aabbccddeeff","name":"A"}]}}`)); err == nil {
		t.Fatal("Load 接受重复伙伴名称")
	}
}

func TestConfigAIKnownFieldErrorsIncludeExactPath(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantPath string
	}{
		{name: "ai", body: `{"version":1,"ai":[]}`, wantPath: "ai"},
		{name: "companions", body: `{"version":1,"ai":{"companions":{}}}`, wantPath: "ai.companions"},
		{name: "id", body: `{"version":1,"ai":{"companions":[{"id":7,"name":"阿木"}]}}`, wantPath: "ai.companions[0].id"},
		{name: "name", body: `{"version":1,"ai":{"companions":[{"id":"00112233-4455-4677-8899-aabbccddeeff","name":7}]}}`, wantPath: "ai.companions[0].name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := config.Load(writeConfig(t, test.body))
			if err == nil || !strings.Contains(err.Error(), test.wantPath) {
				t.Fatalf("Load error = %v，want path %q", err, test.wantPath)
			}
		})
	}
}

func TestConfigAICompanionDefinitionsReturnsOwnedCopy(t *testing.T) {
	wantID, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.AI = &config.AI{Companions: []companion.Definition{{ID: wantID, Name: "阿木"}}}
	got := cfg.CompanionDefinitions()
	if !reflect.DeepEqual(got, cfg.AI.Companions) {
		t.Fatalf("CompanionDefinitions = %+v，want %+v", got, cfg.AI.Companions)
	}
	got[0].Name = "已改"
	if cfg.AI.Companions[0].Name != "阿木" {
		t.Fatalf("修改返回值反向改写了 Config：%+v", cfg.AI.Companions)
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

// TestSaveWritesDocumentedLowerCamelKeys 钉住磁盘格式与文档一致。
//
// 三个可调结构体与 logging.Config 原先都没有 json tag，Save 写出的是 Go 字段
// 名（"Gravity"、"ViewDistance"、"Default"），而设计 §4.1、Fields() 与 README
// 全用小写驼峰。加载侧大小写不敏感所以没人报错，但用户按 F5 存盘再打开文件，
// 看到的格式与所有文档都对不上，两种大小写还会长期并存。
func TestSaveWritesDocumentedLowerCamelKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Defaults().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var written struct {
		Version int                        `json:"version"`
		Logging map[string]json.RawMessage `json:"logging"`
		Physics map[string]json.RawMessage `json:"physics"`
		Sim     map[string]json.RawMessage `json:"sim"`
		Render  map[string]json.RawMessage `json:"render"`
	}
	if err := json.Unmarshal(body, &written); err != nil {
		t.Fatalf("保存产物不是合法 JSON: %v", err)
	}
	groups := map[string]map[string]json.RawMessage{
		"physics": written.Physics,
		"sim":     written.Sim,
		"render":  written.Render,
	}
	for _, field := range config.Fields() {
		if _, ok := groups[field.Group][field.Name]; !ok {
			t.Errorf("保存产物缺少文档约定的键 %s.%s，实际该组的键是 %v",
				field.Group, field.Name, sortedKeys(groups[field.Group]))
		}
	}
	for _, key := range []string{"default", "modules"} {
		if _, ok := written.Logging[key]; !ok {
			t.Errorf("保存产物缺少文档约定的键 logging.%s，实际该组的键是 %v",
				key, sortedKeys(written.Logging))
		}
	}
	if written.Version != config.CurrentVersion {
		t.Errorf("保存产物 version = %d，want %d", written.Version, config.CurrentVersion)
	}
}

// TestLoadAcceptsLegacyGoFieldNameKeys 证明加 json tag 是向后兼容的：本次修补
// 之前 Save 写出的是 Go 字段名，那些已经落在用户磁盘上的文件必须照常读入。
func TestLoadAcceptsLegacyGoFieldNameKeys(t *testing.T) {
	path := writeConfig(t, `{"version":1,"Logging":{"Default":"warn"},`+
		`"Physics":{"Gravity":24},"Render":{"ViewDistance":16}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Physics.Gravity != 24 {
		t.Errorf("Physics.Gravity = %v，want 24", loaded.Physics.Gravity)
	}
	if loaded.Render.ViewDistance != 16 {
		t.Errorf("Render.ViewDistance = %v，want 16", loaded.Render.ViewDistance)
	}
	if loaded.Logging.Default != slog.LevelWarn {
		t.Errorf("Logging.Default = %v，want warn", loaded.Logging.Default)
	}
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	defaults := config.Defaults()
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
		// 默认值必须落在自己的区间内：否则面板上第一次按方向键就会把值
		// 弹回区间边界，Load 也会在用户什么都没改的情况下报"越界已钳制"。
		if value := defaultFieldFloat(t, defaults, field); value < field.Min || value > field.Max {
			t.Errorf("%s 的默认值 %v 落在区间 [%v, %v] 之外", key, value, field.Min, field.Max)
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

// defaultFieldFloat 读出 field 在 defaults 里的取值并统一成 float64，
// 命名规则同 config.Fields()：小写驼峰的 Name 对应首字母大写的结构体字段。
func defaultFieldFloat(t *testing.T, defaults config.Config, field config.Field) float64 {
	t.Helper()
	var group reflect.Value
	switch field.Group {
	case "physics":
		group = reflect.ValueOf(defaults.Physics)
	case "sim":
		group = reflect.ValueOf(defaults.Sim)
	case "render":
		group = reflect.ValueOf(defaults.Render)
	default:
		t.Fatalf("未知分组 %s", field.Group)
	}
	value := group.FieldByName(strings.ToUpper(field.Name[:1]) + field.Name[1:])
	if !value.IsValid() {
		t.Fatalf("%s.%s 在结构体中不存在", field.Group, field.Name)
	}
	switch value.Kind() {
	case reflect.Float32, reflect.Float64:
		return value.Float()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(value.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(value.Uint())
	default:
		t.Fatalf("%s.%s 的类型 %s 不是数值", field.Group, field.Name, value.Kind())
		return 0
	}
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
