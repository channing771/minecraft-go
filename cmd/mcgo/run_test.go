//go:build darwin

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/config"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/profile"
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

func TestResolveConfigUsesDefaultMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	legacyPath := filepath.Join(base, "minecraft-go", "config.json")
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
	legacyPath := filepath.Join(base, "minecraft-go", "config.json")
	currentPath := filepath.Join(base, "mornlea", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"version":`), 0o600); err != nil {
		t.Fatalf("WriteFile legacy: %v", err)
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
	if _, err := os.Stat(currentPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("显式配置不得触发默认迁移，Stat err = %v", err)
	}
}

func TestLoadApplicationIdentityUsesDefaultMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("UserConfigDir: %v", err)
	}
	legacyPath := filepath.Join(base, "minecraft-go", "profile.json")
	currentPath := filepath.Join(base, "mornlea", "profile.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o700); err != nil {
		t.Fatalf("MkdirAll legacy: %v", err)
	}
	legacy := []byte(`{"version":1,"player_id":"00112233-4455-4677-8899-aabbccddeeff","display_name":"Chen"}`)
	if err := os.WriteFile(legacyPath, legacy, 0o600); err != nil {
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
	if string(contents) != string(legacy) {
		t.Fatalf("迁移后 profile = %q，want %q", contents, legacy)
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
// 只告警不强制回落默认值：README 把这份配置文件描述为 mcgo 与 mcgod 共用，
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
