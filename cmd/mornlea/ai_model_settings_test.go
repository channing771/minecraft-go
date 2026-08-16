package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

// TestRunLocalAIModelSettingsReachServerConfig 验证普通本地模式把模型设置与已解析
// 密钥从配置一路送进 server.Config，且不与 options 共享底层引用。
func TestRunLocalAIModelSettingsReachServerConfig(t *testing.T) {
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	definitions := []companion.Definition{{ID: id, Name: "阿木"}}
	options := localConnectionOptions()
	options.Companions = definitions
	options.AIModel = companion.ModelSettings{
		Endpoint:           "https://example.invalid/v1",
		Model:              "test-model",
		APIKeyEnv:          "MORNLEA_TEST_AI_KEY",
		TaskTimeoutMinutes: 20,
	}
	options.AIAPIKey = "secret-value"
	want := errors.New("stop after server config")
	dependencies := connectionTestDependencies(t)
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		return newConnectionTestStore(42), nil
	}
	dependencies.newHost = func(_ context.Context, config server.Config, _ server.Generator, _ storage.WorldStore) (applicationHost, error) {
		if config.AIModel != options.AIModel || config.AIAPIKey != "secret-value" {
			t.Fatalf("server AIModel=%+v AIAPIKey=%q，want %+v/secret-value",
				config.AIModel, config.AIAPIKey, options.AIModel)
		}
		return nil, want
	}
	_, gotErr := newApplicationWithDependencies(options, dependencies)
	if !errors.Is(gotErr, want) {
		t.Fatalf("newApplication error = %v，want %v", gotErr, want)
	}
}

// TestRunResolvesAIModelKeyFromEnvironment 验证 --config 路径把 apiKeyEnv 指向的
// 环境变量解析进 server.Config；config 文件本体不含密钥。
func TestRunResolvesAIModelKeyFromEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	cfg.AI = &config.AI{
		ModelSettings: companion.ModelSettings{
			Endpoint:           "https://example.invalid/v1",
			Model:              "test-model",
			APIKeyEnv:          "MORNLEA_TEST_AI_KEY",
			TaskTimeoutMinutes: 15,
		},
		Companions: []companion.Definition{{ID: id, Name: "阿木"}},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MORNLEA_TEST_AI_KEY", "env-secret-value")

	var gotKey string
	var gotModel companion.ModelSettings
	stop := errors.New("stop after config capture")
	err = runWithDependencies([]string{"--config", path}, runDependencies{
		loadIdentity: func(*string) (network.Identity, error) { return network.Identity{}, nil },
		newApplication: func(options applicationOptions) (*application, error) {
			gotKey = options.AIAPIKey
			gotModel = options.AIModel
			return nil, stop
		},
	})
	if !errors.Is(err, stop) {
		t.Fatalf("run error = %v，want %v", err, stop)
	}
	if gotKey != "env-secret-value" {
		t.Fatalf("AIAPIKey = %q，want 从环境变量解析", gotKey)
	}
	if gotModel != cfg.AI.ModelSettings {
		t.Fatalf("AIModel = %+v，want %+v", gotModel, cfg.AI.ModelSettings)
	}
}
