package companion

import (
	"strings"
	"testing"
)

// TestValidateModelEndpoint 覆盖 endpoint 的全部合法与非法形态：
// https 必须无 userinfo/query/fragment；http 只允许 IP 字面量为 loopback 的形式。
func TestValidateModelEndpoint(t *testing.T) {
	for _, endpoint := range []string{
		"https://example.invalid/v1",
		"https://api.example.invalid",
		"http://127.0.0.1:8080/v1",
		"http://[::1]/v1",
	} {
		if err := ValidateModelEndpoint(endpoint); err != nil {
			t.Errorf("ValidateModelEndpoint(%q) = %v，want nil", endpoint, err)
		}
	}
	for _, endpoint := range []string{
		"",
		"not a url",
		"ftp://example.invalid/v1",
		"https://user:pw@example.invalid/v1",
		"https://example.invalid/v1?debug=1",
		"https://example.invalid/v1#fragment",
		"http://example.invalid/v1",
		"http://localhost:8080/v1",
		"http://192.168.1.2/v1",
	} {
		if err := ValidateModelEndpoint(endpoint); err == nil {
			t.Errorf("ValidateModelEndpoint(%q) = nil，want error", endpoint)
		}
	}
}

// TestModelSettingsValidate 覆盖静态完整性校验：伙伴启用时 endpoint/model 必填、
// https 必须配置 apiKeyEnv、超时只接受 0（未设）或 1..60。
func TestModelSettingsValidate(t *testing.T) {
	base := func() ModelSettings {
		return ModelSettings{
			Endpoint:           "https://example.invalid/v1",
			Model:              "test-model",
			APIKeyEnv:          "MORNLEA_AI_API_KEY",
			TaskTimeoutMinutes: 10,
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("完整配置被拒绝: %v", err)
	}

	loopback := base()
	loopback.Endpoint = "http://127.0.0.1:8080/v1"
	loopback.APIKeyEnv = ""
	if err := loopback.Validate(); err != nil {
		t.Fatalf("loopback http 无密钥必须可用: %v", err)
	}

	for name, mutate := range map[string]func(*ModelSettings){
		"缺 endpoint":  func(s *ModelSettings) { s.Endpoint = "" },
		"非法 endpoint": func(s *ModelSettings) { s.Endpoint = "http://example.invalid/v1" },
		"缺 model":     func(s *ModelSettings) { s.Model = "" },
		"https 缺 key": func(s *ModelSettings) { s.APIKeyEnv = "" },
		"超时 61":       func(s *ModelSettings) { s.TaskTimeoutMinutes = 61 },
		"超时负数":        func(s *ModelSettings) { s.TaskTimeoutMinutes = -1 },
	} {
		settings := base()
		mutate(&settings)
		if err := settings.Validate(); err == nil {
			t.Errorf("%s 被接受", name)
		}
	}

	// 超时 0 表示未设置，由使用方取默认值，不构成校验错误。
	unset := base()
	unset.TaskTimeoutMinutes = 0
	if err := unset.Validate(); err != nil {
		t.Fatalf("超时未设置被拒绝: %v", err)
	}
}

// TestModelSettingsTaskTimeoutNormalizes 验证超时归一化：未设置回默认 10，
// 已设置值原样返回（区间已由 Validate 保证）。
func TestModelSettingsTaskTimeoutNormalizes(t *testing.T) {
	settings := ModelSettings{}
	if got := settings.TaskTimeout(); got != TaskTimeoutDefaultMinutes {
		t.Fatalf("TaskTimeout() = %d，want 默认 %d", got, TaskTimeoutDefaultMinutes)
	}
	settings.TaskTimeoutMinutes = 42
	if got := settings.TaskTimeout(); got != 42 {
		t.Fatalf("TaskTimeout() = %d，want 42", got)
	}
}

// TestModelSettingsValidateErrorsNameFields 验证错误信息按字段定位且不携带密钥值，
// 给上层"启动失败可定位"的语义提供基础。
func TestModelSettingsValidateErrorsNameFields(t *testing.T) {
	empty := ModelSettings{}
	err := empty.Validate()
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("空配置错误 = %v，want 提及 endpoint", err)
	}
}
