//go:build darwin

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/profile"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

// absentConfigArgs 返回指向本次测试临时目录下一个不存在文件的 --config 参数。
// 它让普通运行测试走显式 config.Load 的 Defaults 回落，避免读写开发者的默认目录。
func absentConfigArgs(t *testing.T) []string {
	t.Helper()
	return []string{"--config", filepath.Join(t.TempDir(), "absent.json")}
}

func TestRunWithDependenciesLoadsProfileOnceForLocalAndRemote(t *testing.T) {
	for _, args := range [][]string{nil, {"--connect", "127.0.0.1:25565"}} {
		t.Run("mode", func(t *testing.T) {
			loads := 0
			identity := network.Identity{PlayerID: core.PlayerID{1}, DisplayName: "Chen"}
			err := runWithDependencies(append(append([]string{}, args...), absentConfigArgs(t)...), runDependencies{
				loadIdentity: func(requested *string) (network.Identity, error) {
					loads++
					return identity, nil
				},
				newApplication: func(options applicationOptions) (*application, error) {
					if options.Identity == nil || *options.Identity != identity {
						t.Fatalf("application identity=%+v", options.Identity)
					}
					return nil, errors.New("stop before window")
				},
			})
			if err == nil || loads != 1 {
				t.Fatalf("run error=%v profile loads=%d, want construction error and 1", err, loads)
			}
		})
	}
}

func TestRunWithDependenciesBypassesProfileForBenchmark(t *testing.T) {
	loads := 0
	err := runWithDependencies([]string{"--benchmark", "--perf-output", "x.json"}, runDependencies{
		loadIdentity: func(*string) (network.Identity, error) {
			loads++
			return network.Identity{}, nil
		},
		newApplication: func(options applicationOptions) (*application, error) {
			if options.Identity != nil {
				t.Fatalf("benchmark identity=%+v, want nil", options.Identity)
			}
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || loads != 0 {
		t.Fatalf("run error=%v profile loads=%d, want construction error and 0", err, loads)
	}
}

// TestRunWithDependenciesDisablesDevForBenchmark 守住"benchmark 产出不应受
// --dev 影响"：同时传 --benchmark 与 --dev 时，传给 newApplication 的
// options.Dev 必须被强制为 false，不能给 benchmark 进程构造面板渲染器、
// 占用它的 GPU 资源。
func TestRunWithDependenciesDisablesDevForBenchmark(t *testing.T) {
	sawCall := false
	var gotDev bool
	err := runWithDependencies([]string{"--benchmark", "--perf-output", "x.json", "--dev"}, runDependencies{
		loadIdentity: func(*string) (network.Identity, error) { return network.Identity{}, nil },
		newApplication: func(options applicationOptions) (*application, error) {
			sawCall = true
			gotDev = options.Dev
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || !sawCall {
		t.Fatalf("run error=%v sawCall=%v，想要构造期错误且确实调用了 newApplication", err, sawCall)
	}
	if gotDev {
		t.Fatal("--benchmark 必须让 --dev 失效：options.Dev = true")
	}
}

// TestRunWithDependenciesAlwaysEnablesDevForCapture 守住抓帧路径必须构造面板
// 渲染器这条契约。
//
// 该断言与它的前身相反，是有意的：早先抓帧被当作"与 benchmark 同类的基线路径"
// 而排除了 --dev。但 debug-panel 场景要拍的就是面板本身，而基线重生成与 CI
// 调用 capture 时都不会带 --dev——沿用旧规则会让那个场景永远拍到空画面。
//
// 两条基线路径的待遇本就不该一致：benchmark measures 性能，面板不该占用 GPU；
// capture 记录画面，面板是被记录的对象之一。面板默认隐藏，只有该场景的 Apply
// 打开它，因此其余场景的基线不受影响。
func TestRunWithDependenciesAlwaysEnablesDevForCapture(t *testing.T) {
	for _, test := range []struct {
		name string
		dev  bool
	}{
		{"带 --dev", true},
		{"不带 --dev", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sawCall := false
			var gotDev bool
			args := []string{"--capture", t.TempDir()}
			if test.dev {
				args = append(args, "--dev")
			}
			args = append(args, absentConfigArgs(t)...)
			err := runWithDependencies(args, runDependencies{
				loadIdentity: func(*string) (network.Identity, error) { return network.Identity{}, nil },
				newApplication: func(options applicationOptions) (*application, error) {
					sawCall = true
					gotDev = options.Dev
					return nil, errors.New("stop before window")
				},
			})
			if err == nil || !sawCall {
				t.Fatalf("run error=%v sawCall=%v，想要构造期错误且确实调用了 newApplication", err, sawCall)
			}
			if !gotDev {
				t.Fatal("--capture 必须构造面板渲染器：options.Dev = false，debug-panel 场景会拍到空画面")
			}
		})
	}
}

// TestFluidGatePerRunPath 钉住注水门控在三条运行路径上的取值，是 F1 契约
// 「benchmark 与 capture 路径显式解耦、不随配置漂移」在默认值翻转后的承重断言。
//
// 三个用例都显式写了一份 fluidEnabled=false 的用户配置文件，且编译期默认值
// 已经是 true：
//   - benchmark 想要 false —— 但**不能**是因为读到了配置里的 false（resolveConfig
//     对 benchmark 直接返回 Defaults()，根本不看这份文件），只能是因为 main.go
//     的 !Benchmark 钉死。所以本用例还配了一个 benchmark + 配置写 true 的孪生
//     用例：那一条排除了"恰好读到 false"这种恒真解释。
//   - capture 想要 true —— 证明抓帧既不读用户配置（文件里写的是 false），
//     也没有被钉死成 false，而是跟随编译期默认值。
//   - 普通本地游玩想要 false —— 证明普通路径确实读用户配置，配置写 false 生效。
//     没有这一条，"门控恒为编译默认值"这种实现也会让前两条通过。
func TestFluidGatePerRunPath(t *testing.T) {
	writeFluidConfig := func(t *testing.T, enabled bool) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.json")
		custom := config.Defaults()
		custom.FluidEnabled = enabled
		if err := custom.Save(path); err != nil {
			t.Fatalf("Save: %v", err)
		}
		return path
	}
	if !config.Defaults().FluidEnabled {
		t.Fatal("本测试的前提是编译期默认值为 true；默认值一旦改回 false，" +
			"capture 用例就会变成恒真，请连同断言一起重新设计")
	}
	t.Cleanup(func() { config.Defaults().Apply() })

	for _, test := range []struct {
		name        string
		args        func(t *testing.T) []string
		configFluid bool
		wantFluid   bool
	}{
		{
			name:        "benchmark 路径钉死关闭（配置写 false）",
			configFluid: false,
			wantFluid:   false,
			args: func(t *testing.T) []string {
				return []string{"--benchmark", "--perf-output", filepath.Join(t.TempDir(), "perf.json")}
			},
		},
		{
			name:        "benchmark 路径钉死关闭（配置写 true）",
			configFluid: true,
			wantFluid:   false,
			args: func(t *testing.T) []string {
				return []string{"--benchmark", "--perf-output", filepath.Join(t.TempDir(), "perf.json")}
			},
		},
		{
			name:        "抓帧路径跟随编译默认值",
			configFluid: false,
			wantFluid:   true,
			args: func(t *testing.T) []string {
				return []string{"--capture", t.TempDir()}
			},
		},
		{
			name:        "普通本地游玩读用户配置",
			configFluid: false,
			wantFluid:   false,
			args:        func(t *testing.T) []string { return nil },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append(test.args(t), "--config", writeFluidConfig(t, test.configFluid))
			sawCall := false
			var gotFluid bool
			err := runWithDependencies(args, runDependencies{
				loadIdentity: func(*string) (network.Identity, error) { return network.Identity{}, nil },
				newApplication: func(options applicationOptions) (*application, error) {
					sawCall = true
					gotFluid = options.FluidEnabled
					return nil, errors.New("stop before window")
				},
			})
			if gotFluid != test.wantFluid {
				t.Fatalf("options.FluidEnabled = %v，want %v", gotFluid, test.wantFluid)
			}
			if err == nil || !sawCall {
				t.Fatalf("run error=%v sawCall=%v，想要构造期错误且确实调用了 newApplication", err, sawCall)
			}
		})
	}
}

func TestRunWithDependenciesPassesExplicitNameToProfile(t *testing.T) {
	name := "Chen"
	var got *string
	err := runWithDependencies(append([]string{"--name", name}, absentConfigArgs(t)...), runDependencies{
		loadIdentity: func(requested *string) (network.Identity, error) {
			got = requested
			return network.Identity{}, nil
		},
		newApplication: func(applicationOptions) (*application, error) {
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || got == nil || *got != name {
		t.Fatalf("run error=%v requested name=%v", err, got)
	}
}

var _ = profile.Options{}

func legacyDataPath(base, name string) string {
	return filepath.Join(base, "minecraft-go", name)
}

func TestResolveConfigUsesDefaultMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	legacyPath := legacyDataPath(base, "config.json")
	currentPath := filepath.Join(base, "mornlea", "config.json")
	legacy := config.Defaults()
	legacy.Physics.Gravity = 24
	if err := legacy.Save(legacyPath); err != nil {
		t.Fatalf("Save legacy config: %v", err)
	}

	got, err := resolveConfig(mainOptions{})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if got.Physics.Gravity != 24 {
		t.Fatalf("gravity = %v，want 24", got.Physics.Gravity)
	}
	if _, err := os.ReadFile(currentPath); err != nil {
		t.Fatalf("读取迁移后默认配置: %v", err)
	}
}

func TestResolveConfigExplicitPathSkipsDefaultMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	currentPath := filepath.Join(base, "mornlea", "config.json")
	if err := os.MkdirAll(filepath.Dir(currentPath), 0o700); err != nil {
		t.Fatalf("MkdirAll current: %v", err)
	}
	const invalidDefault = `{"version":`
	if err := os.WriteFile(currentPath, []byte(invalidDefault), 0o600); err != nil {
		t.Fatalf("WriteFile current: %v", err)
	}
	explicitPath := filepath.Join(t.TempDir(), "explicit.json")
	explicit := config.Defaults()
	explicit.Physics.Gravity = 31
	if err := explicit.Save(explicitPath); err != nil {
		t.Fatalf("Save explicit config: %v", err)
	}

	got, err := resolveConfig(mainOptions{ConfigPath: explicitPath})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if got.Physics.Gravity != 31 {
		t.Fatalf("gravity = %v，want 31", got.Physics.Gravity)
	}
	contents, err := os.ReadFile(currentPath)
	if err != nil || string(contents) != invalidDefault {
		t.Fatalf("显式配置不得读取或修改默认配置，contents = %q, err = %v", contents, err)
	}
}

func TestLoadApplicationIdentityUsesDefaultMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	legacyPath := legacyDataPath(base, "profile.json")
	currentPath := filepath.Join(base, "mornlea", "profile.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy: %v", err)
	}
	stored := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	if err := os.WriteFile(legacyPath, stored, 0o600); err != nil {
		t.Fatalf("WriteFile legacy: %v", err)
	}

	got, err := loadApplicationIdentity(nil)
	if err != nil {
		t.Fatalf("loadApplicationIdentity: %v", err)
	}
	if got.PlayerID.String() != "00112233-4455-4677-8899-aabbccddeeff" || got.DisplayName != "Chen" {
		t.Fatalf("identity = %+v，want 旧 profile 身份", got)
	}
	contents, err := os.ReadFile(currentPath)
	if err != nil {
		t.Fatalf("读取迁移后默认 profile: %v", err)
	}
	if string(contents) != string(stored) {
		t.Fatalf("迁移后 profile = %q，want %q", contents, stored)
	}
}

// TestBenchmarkIgnoresUserConfig 守住"性能门禁不读本机配置"这条不变量。
func TestBenchmarkIgnoresUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	custom := config.Defaults()
	custom.Physics.Gravity = 1
	if err := custom.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.Defaults().Apply() })

	effective, err := resolveConfig(mainOptions{
		ConfigPath:  path,
		Application: applicationOptions{Benchmark: true},
	})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("benchmark 路径必须使用编译默认值，不得读用户配置")
	}
}

// TestRemoteTuningDivergenceWarnCondition 覆盖设计 §3.2 在配置文件这条路径上的
// 缺口：面板用 fieldReadOnly 挡住了联机时改写 physics/sim，但配置文件是始终
// 生效的（§3.1），它绕过那道锁。这里钉住告警条件——只有"连远端 + 这两组偏离
// 默认值"才告警，单机或联机但全默认都不该打扰用户。
//
// 只告警不强制回落默认值：README 把这份配置文件描述为 mornlea 与 mornlea-server 共用，
// 局域网下两端读同一份调过的文件恰恰是正确用法。
func TestRemoteTuningDivergenceWarnCondition(t *testing.T) {
	tuned := config.Defaults()
	tuned.Physics.Gravity = 12
	tunedSim := config.Defaults()
	tunedSim.Sim.InteractionReach = 3

	cases := []struct {
		name      string
		connect   string
		effective config.Config
		want      bool
	}{
		{name: "联机且物理组偏离默认值", connect: "127.0.0.1:7777", effective: tuned, want: true},
		{name: "联机且模拟组偏离默认值", connect: "127.0.0.1:7777", effective: tunedSim, want: true},
		{name: "联机但全部为默认值", connect: "127.0.0.1:7777", effective: config.Defaults()},
		{name: "单机且物理组偏离默认值", effective: tuned},
		{name: "单机且全部为默认值", effective: config.Defaults()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			options := mainOptions{Application: applicationOptions{Connect: testCase.connect}}
			if got := remoteTuningDiverges(options, testCase.effective); got != testCase.want {
				t.Fatalf("remoteTuningDiverges = %v，want %v", got, testCase.want)
			}
		})
	}
}

func TestCaptureIgnoresUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	custom := config.Defaults()
	custom.Physics.Gravity = 1
	if err := custom.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.Defaults().Apply() })

	effective, err := resolveConfig(mainOptions{ConfigPath: path, CaptureDir: "out"})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("抓帧路径必须使用编译默认值，不得读用户配置")
	}
}

func TestRunInjectsAIOnlyIntoOrdinaryLocalGame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults()
	// M5B 起非空伙伴必须携带完整模型设置才能通过 config.Load；这里用免密钥的
	// loopback 形态，保持本测试"伙伴只注入普通本地模式"的主题不变。
	cfg.AI = &config.AI{
		ModelSettings: companion.ModelSettings{
			Endpoint: "http://127.0.0.1:1/v1",
			Model:    "test-model",
		},
		Companions: []companion.Definition{{ID: id, Name: "阿木"}},
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name string
		args []string
		want int
	}{
		{name: "普通本地", args: []string{"--config", path}, want: 1},
		{name: "远程", args: []string{"--config", path, "--connect", "127.0.0.1:25565"}},
		{name: "benchmark", args: []string{"--config", path, "--benchmark", "--perf-output", "x.json"}},
		{name: "capture", args: []string{"--config", path, "--capture", t.TempDir()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got []companion.Definition
			err := runWithDependencies(test.args, runDependencies{
				loadIdentity: func(*string) (network.Identity, error) { return network.Identity{}, nil },
				newApplication: func(options applicationOptions) (*application, error) {
					got = options.Companions
					return nil, errors.New("stop before window")
				},
			})
			if err == nil || len(got) != test.want {
				t.Fatalf("run error=%v companions=%+v，want %d", err, got, test.want)
			}
		})
	}
}

func TestRunLocalAIReachesServerConfigWithoutAliasingOptions(t *testing.T) {
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	definitions := []companion.Definition{{ID: id, Name: "阿木"}}
	options := localConnectionOptions()
	options.Companions = definitions
	want := errors.New("stop after server config")
	dependencies := connectionTestDependencies(t)
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		return newConnectionTestStore(42), nil
	}
	dependencies.newHost = func(_ context.Context, config server.Config, _ server.Generator, _ storage.WorldStore) (applicationHost, error) {
		if len(config.Companions) != 1 || config.Companions[0] != definitions[0] {
			t.Fatalf("server companions = %+v", config.Companions)
		}
		config.Companions[0].Name = "已改"
		return nil, want
	}
	_, gotErr := newApplicationWithDependencies(options, dependencies)
	if !errors.Is(gotErr, want) {
		t.Fatalf("newApplication error = %v，want %v", gotErr, want)
	}
	if definitions[0].Name != "阿木" {
		t.Fatalf("server.Config 与 applicationOptions 共用 backing array：%+v", definitions)
	}
}
