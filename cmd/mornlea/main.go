//go:build darwin

// Command mornlea 启动 M3B TCP 直连与持久世界客户端。
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"slices"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/logging"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/profile"
)

const steadyFrameMeshWorkMax = 64

func init() {
	runtime.LockOSThread()
}

type runDependencies struct {
	newApplication func(applicationOptions) (*application, error)
	loadIdentity   func(*string) (network.Identity, error)
	runInteractive func(*application) error
	runBenchmark   func(*application, string) error
	runCapture     func(*application, string, bool) error
}

func run(args []string) error {
	return runWithDependencies(args, runDependencies{
		newApplication: newApplication,
		loadIdentity:   loadApplicationIdentity,
		runInteractive: runInteractive,
		runBenchmark:   runBenchmark,
		runCapture:     runCapture,
	})
}

func runWithDependencies(args []string, dependencies runDependencies) error {
	options, err := parseMainOptions(args)
	if err != nil {
		return err
	}
	if !options.Application.Benchmark {
		identity, err := dependencies.loadIdentity(options.RequestedName)
		if err != nil {
			return fmt.Errorf("加载本机 profile: %w", err)
		}
		options.Application.Identity = &identity
	}

	effective, err := resolveConfig(options)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	// 内层 handler 的 Level 固定为 LevelDebug：过滤全部交给 logging 包的包装器，
	// 内层不得二次过滤，否则模块放宽会失效。
	logging.Install(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}), effective.Logging)
	effective.Apply()
	if options.Application.Connect == "" && !options.Application.Benchmark && options.CaptureDir == "" {
		options.Application.Companions = slices.Clone(effective.CompanionDefinitions())
		// 模型设置与已解析密钥只注入普通本地模式，与伙伴注入共用同一分支：
		// 远程、benchmark、capture 三条路径永远不携带半套 AI 运行时。配置层
		// （config.Load）已保证非空伙伴时模型字段完整，这里只做转发与密钥解析。
		if effective.AI != nil {
			options.Application.AIModel = effective.AI.ModelSettings
			options.Application.AIAPIKey = resolveAIAPIKey(effective.AI.ModelSettings)
		}
	}
	if remoteTuningDiverges(options, effective) {
		slog.Warn("联机时本机配置改动了 physics/sim：这两组必须与服务端一致，"+
			"否则客户端预测会与权威模拟持续分歧（面板在联机时已锁这两组，配置文件不受该锁约束）",
			"connect", options.Application.Connect)
	}
	// benchmark 不构造面板渲染器：它的产出要与性能基线比对，面板既不该占用
	// GPU 资源，也不该让结果随 --dev 变化。
	//
	// 抓帧相反，必须无条件构造：debug-panel 场景要拍的就是面板本身，而基线
	// 重生成与 CI 调用 capture 时都不会带 --dev。面板默认隐藏，只有该场景的
	// Apply 会把它打开，因此其余场景的画面不受影响。
	options.Application.Dev = (options.Dev || options.CaptureDir != "") &&
		!options.Application.Benchmark
	options.Application.Render = effective.Render
	// 面板 F5 保存需要落盘路径；benchmark 与抓帧路径不进交互循环，不需要它。
	if !options.Application.Benchmark && options.CaptureDir == "" {
		configPath, err := resolveConfigPath(options)
		if err != nil {
			return fmt.Errorf("解析配置文件路径: %w", err)
		}
		options.Application.ConfigPath = configPath
	}

	app, err := dependencies.newApplication(options.Application)
	if err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}

	if options.CaptureDir != "" {
		return errors.Join(
			dependencies.runCapture(app, options.CaptureDir, options.UpdateGolden),
			app.Close(),
		)
	}

	var runErr error
	if options.Application.Benchmark {
		if err := dependencies.runBenchmark(app, options.PerfOutput); err != nil {
			runErr = fmt.Errorf("性能记录失败: %w", err)
		}
	} else {
		runErr = dependencies.runInteractive(app)
	}
	return errors.Join(runErr, app.Close())
}

func loadApplicationIdentity(requestedName *string) (network.Identity, error) {
	loaded, err := profile.LoadOrCreate(profile.Options{RequestedName: requestedName})
	if err != nil {
		return network.Identity{}, err
	}
	return network.Identity{PlayerID: loaded.PlayerID, DisplayName: loaded.DisplayName}, nil
}

// resolveAIAPIKey 按 settings.APIKeyEnv 指向的环境变量名解析密钥值。
//
// 环境变量名为空（loopback http 免密钥的本地联调形态）返回空串；变量未设置
// 或值为空同样返回空串——是否构成启动错误由 server.NewHost 的密钥边界统一
// 裁决，这里不做二次判断。密钥值只流入内存中的 applicationOptions，绝不写
// 日志或配置文件。
func resolveAIAPIKey(settings companion.ModelSettings) string {
	if settings.APIKeyEnv == "" {
		return ""
	}
	return os.Getenv(settings.APIKeyEnv)
}

// clientMemoryLimit 是客户端进程的 Go 堆软上限。
//
// 视距 32 下快速移动会造成密集的区块加载、卸载与网格化周转。Go 默认要等堆长到
// 活跃集的两倍才回收，实测这会让堆保留冲到 1635MiB，而其中约 400MiB 只是尚未
// 回收的空闲堆。设定软上限让 GC 在接近该值时更积极，实测把进程 RSS 峰值压低约
// 121MiB，活跃数据与帧时间分位数均不受影响。
//
// 取值需高于实测活跃堆峰值（约 1252MiB），否则 GC 会因长期贴近上限而频繁运行。
const clientMemoryLimit = 1500 << 20

func main() {
	debug.SetMemoryLimit(clientMemoryLimit)
	if err := run(os.Args[1:]); err != nil {
		slog.Error("mornlea 退出失败", "error", err)
		os.Exit(1)
	}
}
