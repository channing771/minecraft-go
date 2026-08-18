package config_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
)

// captureConfigLogs 把默认 slog 换成内存 JSON handler 并返回累积缓冲，供
// 断言 persona 告警的出现与缺席。相关测试不使用 t.Parallel，SetDefault 安全。
func captureConfigLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var records bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&records, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &records
}

// writeAICompanionConfig 在 t.TempDir 的 config.json 写一份只含 ai 组的 v1
// 配置；companionTail 是伙伴条目对象里 name 之后的补充字段。使用 loopback http
// endpoint（免密钥）让配置满足 M5B 起的模型字段完整性要求。
func writeAICompanionConfig(t *testing.T, companionTail string) string {
	t.Helper()
	body := `{"version":1,"ai":{"endpoint":"http://127.0.0.1:8080/v1","model":"test-model",` +
		`"companions":[{"id":"00112233-4455-4677-8899-aabbccddeeff","name":"阿木"` + companionTail + `}]}}`
	return writeConfig(t, body)
}

// writeAICompanionConfigNamed 与 writeAICompanionConfig 相同，但允许自定义
// 伙伴名称（路径穿越矩阵需要非常规名称）。
func writeAICompanionConfigNamed(t *testing.T, name string) string {
	t.Helper()
	body := `{"version":1,"ai":{"endpoint":"http://127.0.0.1:8080/v1","model":"test-model",` +
		`"companions":[{"id":"00112233-4455-4677-8899-aabbccddeeff","name":` +
		strconv.Quote(name) + `}]}}`
	return writeConfig(t, body)
}

// writePersonaFile 在配置文件所在目录的 personas/ 下写出外部人设文件并返回
// 其精确路径；目录不存在时一并创建。
func writePersonaFile(t *testing.T, configPath, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(configPath), "personas", name+".txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("创建 personas 目录: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("写外部人设文件: %v", err)
	}
	return path
}

// assertSinglePersona 断言加载结果恰好一个伙伴且生效人设 ResolvedPersona
// 等于 want；wantInline 断言磁盘镜像字段 Persona 的期望值（内联原文）。
func assertSinglePersona(t *testing.T, loaded config.Config, want string) {
	t.Helper()
	definitions := loaded.CompanionDefinitions()
	if len(definitions) != 1 {
		t.Fatalf("CompanionDefinitions 数量 = %d, want 1", len(definitions))
	}
	if definitions[0].ResolvedPersona != want {
		t.Fatalf("ResolvedPersona = %q (len %d), want %q (len %d)",
			definitions[0].ResolvedPersona, len(definitions[0].ResolvedPersona), want, len(want))
	}
}

// assertSingleDefinition 断言加载结果恰好一个伙伴，并返回该定义供调用方
// 检查 Persona（磁盘镜像）与 ResolvedPersona（生效值）两个字段。
func assertSingleDefinition(t *testing.T, loaded config.Config) companion.Definition {
	t.Helper()
	definitions := loaded.CompanionDefinitions()
	if len(definitions) != 1 {
		t.Fatalf("CompanionDefinitions 数量 = %d, want 1", len(definitions))
	}
	return definitions[0]
}

// TestConfigCompanionInlinePersonaBounds 验证内联 persona 的字节边界与降级：
// 4,096 字节接受且零告警，4,097 字节与含 NUL 告警（带精确字段路径）后生效
// 人设为空，绝不阻止启动；降级只影响 ResolvedPersona，磁盘镜像 Persona
// 保留用户原文（Save 不得清除数据）。告警不回显人设文本。
func TestConfigCompanionInlinePersonaBounds(t *testing.T) {
	oversize := strings.Repeat("A", companion.MaxPersonaBytes+1)
	cases := []struct {
		name        string
		persona     string
		wantPersona string
		wantWarn    bool
	}{
		{name: "4096字节接受", persona: strings.Repeat("A", companion.MaxPersonaBytes),
			wantPersona: strings.Repeat("A", companion.MaxPersonaBytes)},
		{name: "4097字节告警降级空人设", persona: oversize, wantWarn: true},
		{name: "含NUL告警降级空人设", persona: "山\x00民", wantWarn: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			records := captureConfigLogs(t)
			encoded, err := json.Marshal(testCase.persona)
			if err != nil {
				t.Fatal(err)
			}
			path := writeAICompanionConfig(t, `,"persona":`+string(encoded))
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatalf("persona 内容越界必须告警降级而不是让 Load 失败: %v", err)
			}
			assertSinglePersona(t, loaded, testCase.wantPersona)
			// 磁盘镜像必须保留内联原文：生效值与镜像分离，越界原文不被清除。
			if definition := assertSingleDefinition(t, loaded); definition.Persona != testCase.persona {
				t.Fatalf("Persona 磁盘镜像 = %q (len %d), want 原文 (len %d)",
					definition.Persona, len(definition.Persona), len(testCase.persona))
			}
			logs := records.String()
			if testCase.wantWarn {
				if !strings.Contains(logs, `"field":"ai.companions[0].persona"`) {
					t.Errorf("告警缺少精确字段路径 ai.companions[0].persona: %s", logs)
				}
			} else if records.Len() != 0 {
				t.Errorf("合法内联人设不得产生任何告警: %s", logs)
			}
			// persona 不外泄：告警正文不得包含人设文本（这里用前缀片段探针）。
			probe := testCase.persona
			if len(probe) > 32 {
				probe = probe[:32]
			}
			if strings.Contains(logs, probe) {
				t.Errorf("告警回显了人设文本片段 %q: %s", probe, logs)
			}
		})
	}
}

// TestConfigCompanionPersonaShapeErrorRejected 验证 persona 的 JSON 形状错误
// （非字符串）与 id/name 一样按解析错误拒绝 Load，错误带精确字段路径。
func TestConfigCompanionPersonaShapeErrorRejected(t *testing.T) {
	path := writeAICompanionConfig(t, `,"persona":5`)
	_, err := config.Load(path)
	if err == nil || !strings.Contains(err.Error(), "ai.companions[0].persona") {
		t.Fatalf("persona 形状错误必须拒绝 Load 且带精确字段路径, got %v", err)
	}
}

// TestConfigCompanionPersonaFromFile 验证内联缺失时按配置文件所在目录的
// personas/<canonical 名称>.txt 读取外部人设，且正常读取零告警；磁盘镜像
// 字段保持为空（外部来源不落配置文件）。
func TestConfigCompanionPersonaFromFile(t *testing.T) {
	records := captureConfigLogs(t)
	path := writeAICompanionConfig(t, "")
	want := "沉静寡言的山民，只在必要时说话。"
	writePersonaFile(t, path, "阿木", []byte(want))
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSinglePersona(t, loaded, want)
	if definition := assertSingleDefinition(t, loaded); definition.Persona != "" {
		t.Fatalf("外部文件来源不得写入磁盘镜像 Persona: %q", definition.Persona)
	}
	if records.Len() != 0 {
		t.Errorf("正常外部人设文件不得产生告警: %s", records)
	}
}

// TestConfigCompanionPersonaMissingSourcesSilent 验证既无内联也无外部文件时
// 得到空人设且完全静默（personas/ 目录不存在不告警），对应"无 persona 伙伴
// 正常工作"场景。
func TestConfigCompanionPersonaMissingSourcesSilent(t *testing.T) {
	records := captureConfigLogs(t)
	path := writeAICompanionConfig(t, "")
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSinglePersona(t, loaded, "")
	if records.Len() != 0 {
		t.Errorf("双源缺失必须零告警: %s", records)
	}
}

// TestConfigCompanionPersonaInlinePriorityOverFile 验证内联优先：内联与外部
// 文件同时存在时内联生效，并告警外部文件被忽略（含精确文件路径）。
func TestConfigCompanionPersonaInlinePriorityOverFile(t *testing.T) {
	records := captureConfigLogs(t)
	path := writeAICompanionConfig(t, `,"persona":"内联人设"`)
	personaPath := writePersonaFile(t, path, "阿木", []byte("文件人设"))
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSinglePersona(t, loaded, "内联人设")
	logs := records.String()
	if !strings.Contains(logs, personaPath) {
		t.Errorf("双源告警缺少被忽略文件的精确路径 %s: %s", personaPath, logs)
	}
	if strings.Contains(logs, "文件人设") {
		t.Errorf("告警回显了外部人设文本: %s", logs)
	}
}

// TestConfigCompanionPersonaFileDegrades 验证外部文件的损坏矩阵：超 4,096
// 字节、含 NUL、非法 UTF-8（该分支只服务文件来源：内联经 encoding/json
// 恒为有效 UTF-8）、不可读（目录当文件）都告警精确路径后按空人设降级，
// 绝不阻止启动，也不回显文件内容。
func TestConfigCompanionPersonaFileDegrades(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, configPath string) (personaPath string, probe string)
	}{
		{
			name: "5KiB超限",
			setup: func(t *testing.T, configPath string) (string, string) {
				return writePersonaFile(t, configPath, "阿木",
					[]byte(strings.Repeat("B", 5*1024))), strings.Repeat("B", 32)
			},
		},
		{
			name: "含NUL",
			setup: func(t *testing.T, configPath string) (string, string) {
				return writePersonaFile(t, configPath, "阿木", []byte("山\x00民")), "山\x00民"
			},
		},
		{
			name: "非法UTF8",
			setup: func(t *testing.T, configPath string) (string, string) {
				return writePersonaFile(t, configPath, "阿木", []byte("山\xff\xfe民")), "\xff\xfe"
			},
		},
		{
			name: "不可读目录当文件",
			setup: func(t *testing.T, configPath string) (string, string) {
				path := filepath.Join(filepath.Dir(configPath), "personas", "阿木.txt")
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatalf("创建目录占位: %v", err)
				}
				return path, ""
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			records := captureConfigLogs(t)
			path := writeAICompanionConfig(t, "")
			personaPath, probe := testCase.setup(t, path)
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatalf("损坏人设文件必须降级而不是让 Load 失败: %v", err)
			}
			assertSinglePersona(t, loaded, "")
			logs := records.String()
			if !strings.Contains(logs, personaPath) {
				t.Errorf("降级告警缺少精确文件路径 %s: %s", personaPath, logs)
			}
			if probe != "" && strings.Contains(logs, probe) {
				t.Errorf("告警回显了人设文件内容片段: %s", logs)
			}
		})
	}
}

// TestConfigCompanionPersonaNoPathTraversal 锁定外部文件名的路径穿越面。
// 前提核实：ValidateName 只保证 canonical 与无空白，并不拒绝名称中的路径
// 分隔符（"../sneaky" 是合法伙伴名），因此 persona 文件解析必须自行保证
// 不拼出逃出 personas/ 的路径——含分隔符的名称按无外部文件处理；纯点号
// 名称（".."、"."）拼接 ".txt" 后退化为 personas/ 内的字面文件名，不构成
// 穿越，可正常读取。
func TestConfigCompanionPersonaNoPathTraversal(t *testing.T) {
	if err := companion.ValidateName("../sneaky"); err != nil {
		t.Fatalf("前提变化：ValidateName 已拒绝含 / 名称，请同步更新人设文件名安全论证: %v", err)
	}
	cases := []struct {
		name          string
		companionName string
		// plant 在配置目录埋穿越目标或字面文件，返回其内容探针（空串表示不埋）。
		plant func(t *testing.T, configPath string) string
		// wantResolved 是期望的生效人设。
		wantResolved string
		// wantSilent 断言整个 Load 过程零告警。
		wantSilent bool
	}{
		{
			name:          "正斜杠穿越被拒",
			companionName: "../sneaky",
			plant: func(t *testing.T, configPath string) string {
				// 在 personas/ 之外埋穿越目标：若解析拼出 "../sneaky.txt" 就会读到它。
				planted := filepath.Join(filepath.Dir(configPath), "sneaky.txt")
				if err := os.WriteFile(planted, []byte("不该被读到"), 0o600); err != nil {
					t.Fatal(err)
				}
				return "不该被读到"
			},
			wantResolved: "",
			wantSilent:   true,
		},
		{
			name:          "反斜杠名称不解析",
			companionName: `..\sneaky`,
			// 反斜杠在 POSIX 只是字面字符，但守卫统一拒绝，防跨平台误用；
			// 不埋任何文件即可验证"不解析"。
			plant:        func(t *testing.T, configPath string) string { return "" },
			wantResolved: "",
			wantSilent:   true,
		},
		{
			name:          "名称两点退化为字面文件",
			companionName: "..",
			plant: func(t *testing.T, configPath string) string {
				// ".."+".txt" = "...txt"，是 personas/ 内的字面文件名。
				writePersonaFile(t, configPath, "..", []byte("字面内容三点"))
				return "字面内容三点"
			},
			wantResolved: "字面内容三点",
			wantSilent:   true,
		},
		{
			name:          "名称单点退化为字面文件",
			companionName: ".",
			plant: func(t *testing.T, configPath string) string {
				// "."+".txt" = "..txt"，同样是 personas/ 内的字面文件名。
				writePersonaFile(t, configPath, ".", []byte("字面内容两点"))
				return "字面内容两点"
			},
			wantResolved: "字面内容两点",
			wantSilent:   true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			records := captureConfigLogs(t)
			path := writeAICompanionConfigNamed(t, testCase.companionName)
			probe := testCase.plant(t, path)
			loaded, err := config.Load(path)
			if err != nil {
				t.Fatalf("非常规名称的合法配置不得被 persona 解析破坏: %v", err)
			}
			assertSinglePersona(t, loaded, testCase.wantResolved)
			logs := records.String()
			if probe != "" && strings.Contains(logs, probe) {
				t.Errorf("发生穿越读取或内容外泄: %s", logs)
			}
			if testCase.wantSilent && records.Len() != 0 {
				t.Errorf("该名称应按文件不存在静默处理: %s", logs)
			}
		})
	}
}

// TestConfigCompanionPersonaNotUnknownField 验证 persona 已成为已识别字段：
// 不再触发未知字段告警，而其他未知字段（task）仍按既有纪律告警忽略。
func TestConfigCompanionPersonaNotUnknownField(t *testing.T) {
	records := captureConfigLogs(t)
	path := writeAICompanionConfig(t, `,"persona":"温和的向导","task":"later"`)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSinglePersona(t, loaded, "温和的向导")
	logs := records.String()
	if !strings.Contains(logs, `"field":"ai.companions[0].task"`) {
		t.Errorf("其他未知字段仍必须告警: %s", logs)
	}
	if strings.Contains(logs, `"field":"ai.companions[0].persona"`) {
		t.Errorf("persona 不应再触发未知字段告警: %s", logs)
	}
}

// TestConfigPersonaSaveDoesNotAbsorbExternalFile 锁定 Important 场景 A：
// 外部文件提供人设时，Config.Save（调试面板保存路径同构：Load 后改
// physics/sim/render 再 Save）不得把文件内容吸收为 config.json 内联——
// 否则此后编辑外部文件不再生效。
func TestConfigPersonaSaveDoesNotAbsorbExternalFile(t *testing.T) {
	path := writeAICompanionConfig(t, "")
	filePersona := "文件来源的人设"
	writePersonaFile(t, path, "阿木", []byte(filePersona))
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if definition := assertSingleDefinition(t, loaded); definition.ResolvedPersona != filePersona {
		t.Fatalf("ResolvedPersona = %q, want 文件内容", definition.ResolvedPersona)
	}
	if err := loaded.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), filePersona) {
		t.Fatal("Save 把外部文件内容吸收为内联 persona，此后编辑外部文件将不再生效")
	}
	if strings.Contains(string(raw), `"persona"`) {
		t.Fatalf("Save 不应为无内联人设的伙伴写出 persona 键: %s", raw)
	}
	// 重启（重新 Load）后外部文件仍然生效。
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("重新 Load: %v", err)
	}
	definition := assertSingleDefinition(t, reloaded)
	if definition.ResolvedPersona != filePersona {
		t.Fatalf("重启后外部人设不再生效: ResolvedPersona = %q", definition.ResolvedPersona)
	}
	if definition.Persona != "" {
		t.Fatalf("重启后磁盘镜像被污染: Persona = %q", definition.Persona)
	}
}

// TestConfigPersonaSavePreservesOversizeInlineRaw 锁定 Important 场景 B：
// 4,097-byte 越界内联在降级为空生效人设后，Config.Save 仍逐字节保留磁盘
// 原文——降级是运行期判断，绝不能变成对用户配置数据的静默清除。
func TestConfigPersonaSavePreservesOversizeInlineRaw(t *testing.T) {
	oversize := strings.Repeat("A", companion.MaxPersonaBytes+1)
	encoded, err := json.Marshal(oversize)
	if err != nil {
		t.Fatal(err)
	}
	path := writeAICompanionConfig(t, `,"persona":`+string(encoded))
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("越界内联必须降级而不是让 Load 失败: %v", err)
	}
	if definition := assertSingleDefinition(t, loaded); definition.ResolvedPersona != "" ||
		definition.Persona != oversize {
		t.Fatalf("降级后 ResolvedPersona = %q, Persona = %d 字节，want 空生效值 + 原文保留",
			definition.ResolvedPersona, len(definition.Persona))
	}
	if err := loaded.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("重新 Load: %v", err)
	}
	if definition := assertSingleDefinition(t, reloaded); definition.Persona != oversize {
		t.Fatalf("Save 未逐字节保留越界原文: got %d 字节, want %d 字节",
			len(definition.Persona), len(oversize))
	}
}
