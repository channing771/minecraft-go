package server

import (
	"context"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
)

// hostTestAIModel 返回 host 测试通用的合法 loopback 模型设置（无需密钥）。
func hostTestAIModel() companion.ModelSettings {
	return companion.ModelSettings{
		Endpoint:           "http://127.0.0.1:1/v1",
		Model:              "test-model",
		TaskTimeoutMinutes: 10,
	}
}

// TestNewHostRejectsCompanionsWithoutModelSettings 验证非空伙伴配置缺少模型设置时
// NewHost 以错误（而非 panic）拒绝启动。
func TestNewHostRejectsCompanionsWithoutModelSettings(t *testing.T) {
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: companionBootstrapID(1), Name: "阿木"}}
	config.AIModel = companion.ModelSettings{}
	config.AIAPIKey = ""

	store := newCompanionBootstrapStore()
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err == nil {
		cleanupCompanionBootstrapHost(t, host)
		t.Fatal("缺少模型设置的伙伴配置被 NewHost 接受")
	}
	if !strings.Contains(err.Error(), "model") && !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("错误信息缺少可定位字段: %v", err)
	}
}

// TestNewHostRejectsHTTPSWithoutResolvedAPIKey 验证 https endpoint 必须携带已解析的
// 非空密钥，且错误信息不回显密钥值。
func TestNewHostRejectsHTTPSWithoutResolvedAPIKey(t *testing.T) {
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: companionBootstrapID(1), Name: "阿木"}}
	config.AIModel = hostTestAIModel()
	config.AIModel.Endpoint = "https://example.invalid/v1"
	config.AIAPIKey = ""
	store := newCompanionBootstrapStore()
	if host, err := NewHost(context.Background(), config, flatTestGenerator{}, store); err == nil {
		cleanupCompanionBootstrapHost(t, host)
		t.Fatal("https 缺少已解析密钥被 NewHost 接受")
	}

	// 密钥存在但 model 缺失：错误必须可定位且不得包含密钥值。
	config.AIModel.Model = ""
	config.AIAPIKey = "super-secret-value"
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, store)
	if err == nil {
		cleanupCompanionBootstrapHost(t, host)
		t.Fatal("缺 model 的配置被 NewHost 接受")
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("错误信息泄漏密钥值: %v", err)
	}
}

// TestNewHostAcceptsLoopbackModelWithoutAPIKey 验证 loopback http 无密钥可正常启动。
func TestNewHostAcceptsLoopbackModelWithoutAPIKey(t *testing.T) {
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: companionBootstrapID(1), Name: "阿木"}}
	config.AIModel = hostTestAIModel()
	config.AIAPIKey = ""
	host, err := NewHost(context.Background(), config, flatTestGenerator{}, newCompanionBootstrapStore())
	if err != nil {
		t.Fatalf("loopback 模型配置被拒绝: %v", err)
	}
	cleanupCompanionBootstrapHost(t, host)
}
