package config_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/config"
)

// TestRenderLODDefaults 锁住远环 LOD 三个调参项的编译期默认值:
// lodEnabled=true、lodFarMultiplier=3、lodStep=4(rust-engine-lod-shell
// 设计「Go 编排」裁决)。默认几何与 Rust 渲染器的默认雾锚点(768/1152,
// 即 0.5/0.75 × 3×32×16)一致,默认配置下不接线推导也不改变画面。
func TestRenderLODDefaults(t *testing.T) {
	render := config.Defaults().Render
	if !render.LodEnabled {
		t.Fatal("lodEnabled 默认必须为 true")
	}
	if render.LodFarMultiplier != 3 {
		t.Fatalf("lodFarMultiplier 默认 = %d, want 3", render.LodFarMultiplier)
	}
	if render.LodStep != 4 {
		t.Fatalf("lodStep 默认 = %d, want 4", render.LodStep)
	}
}

// TestRenderLODFieldsLoad 验证三个 LOD 键从配置文件读入且大小写不敏感,
// 与 physics/sim/render 既有键同一套纪律。
func TestRenderLODFieldsLoad(t *testing.T) {
	path := writeConfig(t, `{"version":1,"render":{
		"lodEnabled":false,"lodFarMultiplier":8,"lodStep":2}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Render.LodEnabled {
		t.Fatal("lodEnabled=false 必须被读入")
	}
	if loaded.Render.LodFarMultiplier != 8 {
		t.Fatalf("lodFarMultiplier = %d, want 8", loaded.Render.LodFarMultiplier)
	}
	if loaded.Render.LodStep != 2 {
		t.Fatalf("lodStep = %d, want 2", loaded.Render.LodStep)
	}
}

// TestRenderLODMissingFieldsFallBackToDefaults 验证字段缺席时保留默认值
// (既有配置文件升级路径:不写 LOD 键 = 行为与默认一致)。
func TestRenderLODMissingFieldsFallBackToDefaults(t *testing.T) {
	path := writeConfig(t, `{"version":1,"render":{"viewDistance":16}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := config.Defaults().Render
	if loaded.Render.LodEnabled != want.LodEnabled ||
		loaded.Render.LodFarMultiplier != want.LodFarMultiplier ||
		loaded.Render.LodStep != want.LodStep {
		t.Fatalf("LOD 字段缺席时必须保留默认值, got %+v", loaded.Render)
	}
}

// TestRenderLODFarMultiplierClamped 验证 lodFarMultiplier 越界被钳制到
// [2,8] 而不是报错——与 `viewDistance` 等连续数值字段同一纪律。
func TestRenderLODFarMultiplierClamped(t *testing.T) {
	cases := []struct {
		body string
		want int
	}{
		{`{"version":1,"render":{"lodFarMultiplier":1}}`, 2},
		{`{"version":1,"render":{"lodFarMultiplier":-7}}`, 2},
		{`{"version":1,"render":{"lodFarMultiplier":9}}`, 8},
		{`{"version":1,"render":{"lodFarMultiplier":100000}}`, 8},
	}
	for _, testCase := range cases {
		path := writeConfig(t, testCase.body)
		loaded, err := config.Load(path)
		if err != nil {
			t.Fatalf("越界 lodFarMultiplier 必须钳制而不是报错: %v", err)
		}
		if loaded.Render.LodFarMultiplier != testCase.want {
			t.Fatalf("body %s: lodFarMultiplier = %d, want %d",
				testCase.body, loaded.Render.LodFarMultiplier, testCase.want)
		}
	}
}

// TestRenderLODStepDiscreteSet 验证 lodStep 的离散合法集 {2,4,8}:
// 合法值原样保留,非法值落回默认 4 并告警(镜像 logging 未知等级的
// 「落回默认并告警」纪律——步长不是连续区间,钳制语义不成立)。
func TestRenderLODStepDiscreteSet(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"步长2", `{"version":1,"render":{"lodStep":2}}`, 2},
		{"步长8", `{"version":1,"render":{"lodStep":8}}`, 8},
		{"非法3", `{"version":1,"render":{"lodStep":3}}`, 4},
		{"非法6", `{"version":1,"render":{"lodStep":6}}`, 4},
		{"非法16", `{"version":1,"render":{"lodStep":16}}`, 4},
		{"非法0", `{"version":1,"render":{"lodStep":0}}`, 4},
		{"非法负数", `{"version":1,"render":{"lodStep":-4}}`, 4},
		{"非法小数", `{"version":1,"render":{"lodStep":2.5}}`, 4},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := writeConfig(t, testCase.body)
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatalf("非法 lodStep 必须落回默认而不是报错: %v", err)
			}
			if loaded.Render.LodStep != testCase.want {
				t.Fatalf("lodStep = %d, want %d", loaded.Render.LodStep, testCase.want)
			}
		})
	}
}

// TestRenderLODEnabledRejectsNonBool 验证 lodEnabled 收到非布尔值时告警并
// 保留默认 true,而不是让 `Load` 失败。
func TestRenderLODEnabledRejectsNonBool(t *testing.T) {
	previous := slog.Default()
	var records bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&records, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	path := writeConfig(t, `{"version":1,"render":{"lodEnabled":"yes"}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("非布尔 lodEnabled 必须告警并回退默认: %v", err)
	}
	if !loaded.Render.LodEnabled {
		t.Fatal("非布尔 lodEnabled 必须保留默认 true")
	}
	if !strings.Contains(records.String(), "render.lodEnabled") {
		t.Fatalf("告警日志 %q 必须指明 render.lodEnabled", records.String())
	}
}

// TestRenderLODKeysAreKnownFields 验证三个 LOD 键被识别为 render 分组的
// 已知字段——不写进 `Fields`()(离散集/布尔不进连续数值面板)之后,最大的
// 风险就是它们被当成未知字段告警忽略,配置文件写了却不生效。
func TestRenderLODKeysAreKnownFields(t *testing.T) {
	previous := slog.Default()
	var records bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&records, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	path := writeConfig(t, `{"version":1,"render":{
		"lodEnabled":false,"lodFarMultiplier":5,"lodStep":8}}`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Render.LodEnabled || loaded.Render.LodFarMultiplier != 5 || loaded.Render.LodStep != 8 {
		t.Fatalf("LOD 键必须生效, got %+v", loaded.Render)
	}
	for _, key := range []string{"render.lodEnabled", "render.lodFarMultiplier", "render.lodStep"} {
		if strings.Contains(records.String(), `"field":"`+key+`"`) {
			t.Errorf("已识别键 %q 不得触发未知字段告警: %s", key, records.String())
		}
	}
}

// TestRenderLODRoundTrip 验证 `Save`/`Load` 往返保留 LOD 三字段——调试面板 F5
// 的保存路径按整组 `Render` 落盘,读回必须不丢。
func TestRenderLODRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := config.Defaults()
	original.Render.LodEnabled = false
	original.Render.LodFarMultiplier = 6
	original.Render.LodStep = 8
	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(loaded.Render, original.Render) {
		t.Fatalf("Render 往返不一致: got %+v want %+v", loaded.Render, original.Render)
	}
	// 写出的 JSON 必须带小写驼峰键名,与设计文档措辞逐字一致。
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回配置文件: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contents, &fields); err != nil {
		t.Fatalf("解析写出的配置文件: %v", err)
	}
	render := fields["render"]
	if render == nil {
		t.Fatalf("写出的配置缺少 render 分组: %s", contents)
	}
	for _, key := range []string{"lodEnabled", "lodFarMultiplier", "lodStep"} {
		if !strings.Contains(string(render), `"`+key+`"`) {
			t.Errorf("写出的 render 分组缺少键 %q: %s", key, render)
		}
	}
}
