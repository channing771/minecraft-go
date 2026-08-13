//go:build darwin

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"minecraft-go/internal/config"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/sim"
)

type mainOptions struct {
	Application   applicationOptions
	PerfOutput    string
	RequestedName *string
	CaptureDir    string
	// UpdateGolden 为真时，抓帧结果写入 golden 基线而不是与之比对。
	// 与 applicationOptions 无关：它只影响 runCapture 的行为，从
	// runWithDependencies 直接传给 dependencies.runCapture。
	UpdateGolden bool
	// ConfigPath 是调参配置文件路径；留空表示使用 config.DefaultPath()。
	ConfigPath string
	// Dev 为真时启用调试面板（F3 切换）。它只门控面板可用性，不门控配置文件
	// 是否生效——配置文件里调过的值无论 Dev 是否为真都同样生效。
	Dev bool
}

func parseMainOptions(args []string) (mainOptions, error) {
	flags := flag.NewFlagSet("mcgo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	benchmark := flags.Bool("benchmark", false, "运行固定 1440p 性能场景")
	benchmarkTransport := flags.String("benchmark-transport", "memory", "benchmark 业务传输: memory 或 tcp")
	perfOutput := flags.String("perf-output", "", "性能报告 JSON 输出路径")
	worldPath := flags.String("world", "worlds/default", "世界存档目录")
	connect := flags.String("connect", "", "远程 TCP 服务器地址")
	name := flags.String("name", "", "玩家显示名")
	capture := flags.String("capture", "", "视觉抓帧输出目录；非空时走无头抓帧模式")
	updateGolden := flags.Bool("update-golden", false, "把本次抓帧结果写入 golden 基线")
	dev := flags.Bool("dev", false, "启用调试面板（F3 切换）")
	configPath := flags.String("config", "", "配置文件路径，留空使用默认路径")
	if err := flags.Parse(args); err != nil {
		return mainOptions{}, err
	}
	if flags.NArg() != 0 {
		return mainOptions{}, fmt.Errorf("未知位置参数: %v", flags.Args())
	}
	if *benchmark && *perfOutput == "" {
		return mainOptions{}, errors.New("--benchmark 必须同时提供 --perf-output")
	}
	if *capture != "" && *benchmark {
		return mainOptions{}, errors.New("--capture 不能与 --benchmark 同时使用")
	}
	if *capture != "" && *connect != "" {
		return mainOptions{}, errors.New("--capture 不能与 --connect 同时使用")
	}
	if *updateGolden && *capture == "" {
		return mainOptions{}, errors.New("--update-golden 只能与 --capture 同时使用")
	}
	var worldExplicit, nameExplicit, benchmarkTransportExplicit bool
	flags.Visit(func(flag *flag.Flag) {
		worldExplicit = worldExplicit || flag.Name == "world"
		nameExplicit = nameExplicit || flag.Name == "name"
		benchmarkTransportExplicit = benchmarkTransportExplicit || flag.Name == "benchmark-transport"
	})
	if *connect != "" && worldExplicit {
		return mainOptions{}, errors.New("--connect 不能与显式 --world 同时使用")
	}
	if *connect != "" && *benchmark {
		return mainOptions{}, errors.New("--connect 不能与 --benchmark 同时使用")
	}
	if *benchmark && nameExplicit {
		return mainOptions{}, errors.New("--name 不能与 --benchmark 同时使用")
	}
	if benchmarkTransportExplicit && !*benchmark {
		return mainOptions{}, errors.New("--benchmark-transport 只能与 --benchmark 同时使用")
	}
	if *benchmarkTransport != "memory" && *benchmarkTransport != "tcp" {
		return mainOptions{}, fmt.Errorf("无效 --benchmark-transport %q：只支持 memory 或 tcp", *benchmarkTransport)
	}
	seed := int64(42)
	if *benchmark {
		seed = benchmarkSeed
	}
	return mainOptions{
		Application: applicationOptions{
			Seed:               seed,
			Benchmark:          *benchmark,
			BenchmarkTransport: *benchmarkTransport,
			WorldPath:          *worldPath,
			Connect:            *connect,
			CaptureDir:         *capture,
		},
		PerfOutput: *perfOutput,
		RequestedName: func() *string {
			if nameExplicit {
				return name
			}
			return nil
		}(),
		CaptureDir:   *capture,
		UpdateGolden: *updateGolden,
		ConfigPath:   *configPath,
		Dev:          *dev,
	}, nil
}

// resolveConfigPath 决定调参配置文件的实际路径：显式 --config 优先，
// 否则落回用户配置目录下的默认路径。
func resolveConfigPath(options mainOptions) (string, error) {
	if options.ConfigPath != "" {
		return options.ConfigPath, nil
	}
	return config.DefaultPath()
}

// resolveConfig 决定本次运行的生效配置。
//
// benchmark 与抓帧路径强制使用编译默认值：这两条路径的产出会与基线比对，
// 若读入本机配置，结论就取决于开发者本机的配置文件内容而非代码。
func resolveConfig(options mainOptions) (config.Config, error) {
	if options.Application.Benchmark || options.CaptureDir != "" {
		return config.Defaults(), nil
	}
	if options.ConfigPath != "" {
		return config.Load(options.ConfigPath)
	}
	return config.LoadDefault()
}

// remoteTuningDiverges 报告本次运行是否"连远端服务端 + 本机 physics/sim 偏离
// 编译默认值"。
//
// 设计 §3.2：physics/sim 同时被客户端预测与服务端权威模拟消费，两侧参数不同
// 会让位置持续回弹。调试面板已经用 fieldReadOnly 挡住了联机时的改写，但
// 配置文件是始终生效的（§3.1），它绕过面板那道锁。
//
// 这里只告警不回落默认值：README 明确把这份配置文件描述为 mcgo 与 mcgod 共用，
// 局域网下两端读同一份调过的文件恰恰是正确且一致的用法，强制客户端回落默认值
// 反而会在那个本来能用的场景里制造分歧。
func remoteTuningDiverges(options mainOptions, effective config.Config) bool {
	if options.Application.Connect == "" {
		return false
	}
	return effective.Physics != physics.DefaultTunables() ||
		effective.Sim != sim.DefaultTunables()
}
