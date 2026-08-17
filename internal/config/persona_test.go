package config_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
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
// 配置；aiBody 是 ai 组对象的字段序列（不含外层大括号）。使用 loopback http
// endpoint（免密钥）让配置满足 M5B 起的模型字段完整性要求。
func writeAICompanionConfig(t *testing.T, companionTail string) string {
	t.Helper()
	body := `{"version":1,"ai":{"endpoint":"http://127.0.0.1:8080/v1","model":"test-model",` +
		`"companions":[{"id":"00112233-4455-4677-8899-aabbccddeeff","name":"阿木"` + companionTail + `}]}}`
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

// assertSinglePersona 断言加载结果恰好一个伙伴且 Persona 等于 want。
func assertSinglePersona(t *testing.T, loaded config.Config, want string) {
	t.Helper()
	definitions := loaded.CompanionDefinitions()
	if len(definitions) != 1 {
		t.Fatalf("CompanionDefinitions 数量 = %d, want 1", len(definitions))
	}
	if definitions[0].Persona != want {
		t.Fatalf("Persona = %q (len %d), want %q (len %d)",
			definitions[0].Persona, len(definitions[0].Persona), want, len(want))
	}
}

// TestConfigCompanionInlinePersonaBounds 验证内联 persona 的字节边界与降级：
// 4,096 字节接受且零告警，4,097 字节与含 NUL 告警（带精确字段路径）后按
// 空人设处理，绝不阻止启动；告警不回显人设文本。
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

// TestConfigCompanionPersonaFromFile 验证内联缺失时按配置文件所在目录的
// personas/<canonical 名称>.txt 读取外部人设，且正常读取零告警。
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
// 字节、含 NUL、非法 UTF-8、不可读（目录当文件）都告警精确路径后按空人设
// 降级，绝不阻止启动，也不回显文件内容。
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
// 不拼出逃出 personas/ 的路径——含分隔符的名称按无外部文件处理。
func TestConfigCompanionPersonaNoPathTraversal(t *testing.T) {
	if err := companion.ValidateName("../sneaky"); err != nil {
		t.Fatalf("前提变化：ValidateName 已拒绝含 / 名称，请同步更新人设文件名安全论证: %v", err)
	}
	records := captureConfigLogs(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	body := `{"version":1,"ai":{"endpoint":"http://127.0.0.1:8080/v1","model":"test-model",` +
		`"companions":[{"id":"00112233-4455-4677-8899-aabbccddeeff","name":"../sneaky"}]}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// 在 personas/ 之外埋一个穿越目标：若解析拼出 "../sneaky.txt" 就会读到它。
	planted := filepath.Join(directory, "sneaky.txt")
	if err := os.WriteFile(planted, []byte("不该被读到"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("含路径分隔符名称的合法配置不得被 persona 解析破坏: %v", err)
	}
	assertSinglePersona(t, loaded, "")
	logs := records.String()
	if strings.Contains(logs, "不该被读到") || strings.Contains(logs, planted) {
		t.Errorf("发生穿越读取或路径外泄: %s", logs)
	}
	if records.Len() != 0 {
		t.Errorf("无法映射文件名的名称应按文件不存在静默处理: %s", logs)
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
