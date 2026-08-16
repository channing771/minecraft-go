package config

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
)

// writeAIConfig 写出一份只含 ai 组的 v1 配置，减少测试样板。
func writeAIConfig(t *testing.T, aiBody string) string {
	t.Helper()
	return writeConfig(t, `{"version":1,"ai":`+aiBody+`}`)
}

const aiCompanionEntry = `{"id":"00112233-4455-4677-8899-aabbccddeeff","name":"阿木"}`

// TestConfigAIModelSettingsParse 验证四个模型字段的解析与默认超时。
func TestConfigAIModelSettingsParse(t *testing.T) {
	path := writeAIConfig(t, `{"endpoint":"https://example.invalid/v1","model":"test-model",`+
		`"apiKeyEnv":"MORNLEA_AI_API_KEY","taskTimeoutMinutes":30,"companions":[`+aiCompanionEntry+`]}`)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AI == nil {
		t.Fatal("AI 组未解析")
	}
	if loaded.AI.Endpoint != "https://example.invalid/v1" || loaded.AI.Model != "test-model" ||
		loaded.AI.APIKeyEnv != "MORNLEA_AI_API_KEY" || loaded.AI.TaskTimeoutMinutes != 30 {
		t.Fatalf("模型字段 = %+v", loaded.AI.ModelSettings)
	}

	defaults := writeAIConfig(t, `{"endpoint":"http://127.0.0.1:8080/v1","model":"m","companions":[`+aiCompanionEntry+`]}`)
	loadedDefaults, err := Load(defaults)
	if err != nil {
		t.Fatalf("Load 缺省超时: %v", err)
	}
	if got := loadedDefaults.AI.TaskTimeout(); got != companion.TaskTimeoutDefaultMinutes {
		t.Fatalf("默认超时 = %d，want %d", got, companion.TaskTimeoutDefaultMinutes)
	}
}

// TestConfigAIModelSettingsEndpointValidation 验证 endpoint 形态在 Load 时即被校验，
// 错误信息带精确字段路径。
func TestConfigAIModelSettingsEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{
		"https://user:pw@example.invalid/v1",
		"https://example.invalid/v1?x=1",
		"http://example.invalid/v1",
		"http://localhost:8080/v1",
	} {
		path := writeAIConfig(t, `{"endpoint":`+strconv.Quote(endpoint)+`,"model":"m","companions":[`+aiCompanionEntry+`]}`)
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "ai.endpoint") {
			t.Errorf("Load 接受非法 endpoint %q 或错误缺少字段路径: %v", endpoint, err)
		}
	}
}

// TestConfigAIModelSettingsTimeoutBounds 验证 taskTimeoutMinutes 只接受 1..60（缺省 10），
// 越界直接拒绝而不是钳制。
func TestConfigAIModelSettingsTimeoutBounds(t *testing.T) {
	for _, minutes := range []int{1, 60} {
		path := writeAIConfig(t, `{"endpoint":"http://127.0.0.1:8080/v1","model":"m",`+
			`"taskTimeoutMinutes":`+strconv.Itoa(minutes)+`,"companions":[`+aiCompanionEntry+`]}`)
		if _, err := Load(path); err != nil {
			t.Errorf("timeout=%d 被拒绝: %v", minutes, err)
		}
	}
	for _, body := range []string{
		`{"endpoint":"http://127.0.0.1:8080/v1","model":"m","taskTimeoutMinutes":0,"companions":[` + aiCompanionEntry + `]}`,
		`{"endpoint":"http://127.0.0.1:8080/v1","model":"m","taskTimeoutMinutes":61,"companions":[` + aiCompanionEntry + `]}`,
	} {
		if _, err := Load(writeAIConfig(t, body)); err == nil {
			t.Errorf("Load 接受越界超时: %s", body)
		}
	}
}

// TestConfigAIRequiresModelSettingsWhenCompanionsConfigured 验证非空伙伴配置缺少
// endpoint/model/apiKeyEnv 时 Load 失败，AI 关闭时不要求任何模型字段。
func TestConfigAIRequiresModelSettingsWhenCompanionsConfigured(t *testing.T) {
	for name, body := range map[string]string{
		"缺 endpoint":  `{"model":"m","companions":[` + aiCompanionEntry + `]}`,
		"缺 model":     `{"endpoint":"https://example.invalid/v1","companions":[` + aiCompanionEntry + `]}`,
		"https 缺 key": `{"endpoint":"https://example.invalid/v1","model":"m","companions":[` + aiCompanionEntry + `]}`,
	} {
		if _, err := Load(writeAIConfig(t, body)); err == nil {
			t.Errorf("%s 被接受", name)
		}
	}

	// loopback http 允许省略密钥。
	loopback := `{"endpoint":"http://127.0.0.1:8080/v1","model":"m","companions":[` + aiCompanionEntry + `]}`
	if _, err := Load(writeAIConfig(t, loopback)); err != nil {
		t.Errorf("loopback http 无密钥被拒绝: %v", err)
	}

	// AI 关闭：不要求任何模型字段，孤立的模型字段不启用 AI。
	for _, body := range []string{`{}`, `{"endpoint":"https://example.invalid/v1","model":"m"}`} {
		loaded, err := Load(writeAIConfig(t, body))
		if err != nil || loaded.AI != nil {
			t.Errorf("Load(%s) = %+v, %v，want AI disabled", body, loaded.AI, err)
		}
	}
}

// TestConfigAIModelSettingsRoundTrip 验证调试面板 Save 后模型字段完整保留。
func TestConfigAIModelSettingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	want := Defaults()
	want.AI = &AI{
		ModelSettings: companion.ModelSettings{
			Endpoint:           "https://example.invalid/v1",
			Model:              "test-model",
			APIKeyEnv:          "MORNLEA_AI_API_KEY",
			TaskTimeoutMinutes: 25,
		},
		Companions: []companion.Definition{{ID: id, Name: "阿木"}},
	}
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AI == nil || loaded.AI.ModelSettings != want.AI.ModelSettings {
		t.Fatalf("模型字段往返丢失：got %+v，want %+v", loaded.AI, want.AI)
	}
}
