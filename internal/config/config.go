// Package config 负责读写调参配置文件，并把生效值写入各包的运行时快照。
//
// 本包只应被 cmd 导入。internal 下其他包一律不得依赖它——否则一台机器上的本地
// 调参会污染性能基线比对与抓帧 golden 比对，让自动化验证的结论取决于开发者本机
// 的配置文件内容。该约束由 internal/archcheck 守住。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/logging"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/sim"
)

// CurrentVersion 是本程序认识的配置文件版本。
const CurrentVersion = 1

// 远环 LOD 调参项的合法域（rust-engine-lod-shell design「Go 编排」裁决）。
// 这组常量是 multiplier 钳制与 step 离散集的唯一权威：Load 的解析、
// NormalizeLOD 与 cmd/mornlea 的接线推导都从这里取值，不另写第二份数字。
const (
	// LodFarMultiplierMin 是 lodFarMultiplier 的合法下界（含）。
	LodFarMultiplierMin = 2
	// LodFarMultiplierMax 是 lodFarMultiplier 的合法上界（含）。最大合法
	// 组合 viewDistance=64 × multiplier=8 决定了 client 渲染器的 tile 表
	// 容量需求（见 cmd/mornlea 的 lodMaxTiles 锚定注释）。
	LodFarMultiplierMax = 8
	// LodFarMultiplierDefault 是 lodFarMultiplier 的编译期默认值：默认
	// 几何 32×3×16 = 1536 block 远环半径，雾锚点 768/1152 与 Rust 渲染器
	// 的编译期默认一致。
	LodFarMultiplierDefault = 3
	// LodStepDefault 是 lodStep 的编译期默认值。
	LodStepDefault = 4
)

// Render 是渲染相关的可调项。它是一个纯数据结构体，不依赖 internal/render，
// 由 cmd/mornlea 自行读取并消费——这样 mornlea-server（无图形专用服务端）也能导入 config
// 而不会传递性拖入图形依赖。
//
// json tag 与 Fields() 的 Name 逐字对应，保证配置文件写出的键名就是设计文档
// 与 README 里写的小写驼峰；读取侧大小写不敏感，加 tag 之前写出的文件仍可
// 正常读入。LOD 三项（lodEnabled/lodFarMultiplier/lodStep）不进 Fields()：
// 布尔与离散合法集 {2,4,8} 表达不了连续数值的 min/max 钳制语义，由
// applyRenderLOD 与 NormalizeLOD 按各自规则归一。
type Render struct {
	ViewDistance     int     `json:"viewDistance"`
	FovDegrees       float32 `json:"fovDegrees"`
	MouseSensitivity float32 `json:"mouseSensitivity"`
	// LodEnabled 是远环 LOD 的行为级开关，false 时客户端零参与（不建
	// Scheduler、不消费登录种子、渲染器远环 pass 空转）。
	LodEnabled bool `json:"lodEnabled"`
	// LodFarMultiplier 是远环半径相对近环视距的倍率，合法区间
	// [LodFarMultiplierMin, LodFarMultiplierMax]，默认 3。
	LodFarMultiplier int `json:"lodFarMultiplier"`
	// LodStep 是远环壳的列合并步长，离散合法集 {2,4,8}，默认 4
	// （与 internal/lod.ValidStep 同一契约）。
	LodStep int `json:"lodStep"`
}

// AI 是配置文件中的可选 ai 组：模型运行时设置与伙伴定义。
//
// 嵌入 companion.ModelSettings 让 endpoint/model/apiKeyEnv/taskTimeoutMinutes
// 四个字段提升为 ai 组的直接键，随 Save/Load 与调试面板的"只覆盖 physics/sim/
// render、其余原样保留"保存策略自动往返。APIKeyEnv 只是环境变量名——密钥值
// 绝不进入配置文件，由各入口进程启动时解析进内存。
type AI struct {
	companion.ModelSettings
	Companions []companion.Definition `json:"companions,omitempty"`
}

// Config 是完整的调参配置文件内容。
//
// 它内嵌的 logging.Config 含 map 字段，因此 Config 整体不可比较，不能用 ==。
type Config struct {
	Version int              `json:"version"`
	Logging logging.Config   `json:"logging"`
	Physics physics.Tunables `json:"physics"`
	Sim     sim.Tunables     `json:"sim"`
	Render  Render           `json:"render"`
	AI      *AI              `json:"ai,omitempty"`
	// TexturePackPath 是配置文件原文，供保存时无损往返。
	TexturePackPath string `json:"texturePackPath,omitempty"`
	// ResolvedTexturePackPath 是相对配置文件目录解析后的客户端启动路径。
	ResolvedTexturePackPath string `json:"-"`

	// FluidEnabled 控制世界生成是否在海平面及其以下注水（权威流体，变更
	// authoritative-fluid）。它是独立顶层布尔开关，不进 Fields()/physics/sim
	// 那套数值分组与钳制机制——那套机制只认 float64 可钳制的数值字段。
	//
	// 变更 fluid-presentation-survival 已补齐流体的呈现与生存：斜水面几何、
	// 半透明 water pass、天空光穿水衰减、浸没物理与水中移动、溺水与氧气、
	// 水下可瞄准与采掘放置。原先阻止默认开启的三条后果（水下全黑、水体周围
	// 地形不出几何、水下无法交互）均已消除，故默认值改为开启。
	//
	// 显式写 false 仍然有效，用来生成不含水的世界（例如复现旧存档的地形）。
	FluidEnabled bool `json:"fluidEnabled"`
}

// Defaults 返回全部字段取编译期默认值的配置。它是配置文件缺省或字段缺失时的
// 取值，也是调试面板“重置”的目标值。
func Defaults() Config {
	return Config{
		Version: CurrentVersion,
		Logging: logging.Config{
			Default: slog.LevelInfo,
			Modules: map[string]slog.Level{},
		},
		Physics: physics.DefaultTunables(),
		Sim:     sim.DefaultTunables(),
		Render: Render{
			ViewDistance:     32,
			FovDegrees:       70,
			MouseSensitivity: 1,
			LodEnabled:       true,
			LodFarMultiplier: LodFarMultiplierDefault,
			LodStep:          LodStepDefault,
		},
		// 默认开启：fluid-presentation-survival 交付呈现与生存后，水已是
		// 面向普通玩家的正常世界内容，见字段 GoDoc。
		FluidEnabled: true,
	}
}

// DefaultPath 返回默认配置文件路径：用户配置目录下的 mornlea/config.json。
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: 定位用户配置目录: %w", err)
	}
	return filepath.Join(dir, "mornlea", "config.json"), nil
}

func defaultPaths() (current, legacy string, err error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("config: 定位用户配置目录: %w", err)
	}
	return filepath.Join(dir, "mornlea", "config.json"), filepath.Join(dir, "minecraft-go", "config.json"), nil
}

// LoadDefault 从 Mornlea 默认路径加载配置，并在新文件缺失时迁移旧配置。
func LoadDefault() (Config, error) {
	current, legacy, err := defaultPaths()
	if err != nil {
		return Config{}, err
	}
	return loadDefaultFromPaths(current, legacy, publishConfigExclusively)
}

// rawLogging 是日志分组的中间反序列化结构体。等级字段必须先解码为字符串再经
// logging.ParseLevel 转换，不能让 json.Unmarshal 直接填进 slog.Level——
// slog.Level 自带 UnmarshalJSON/UnmarshalText，遇到未知等级会让整个 Load 失败，
// 而本设计要求未知等级落回默认并告警。
type rawLogging struct {
	Default string            `json:"default"`
	Modules map[string]string `json:"modules"`
}

// Load 读取 path 处的配置文件。文件不存在时返回全默认配置且不视为错误、也不
// 会创建文件。缺失字段保留默认值，越界值被钳制，未知字段被忽略，这些情形都不
// 报错，仅通过 slog.Warn 提示；只有 JSON 语法错误与不认识的 version 才报错。
func Load(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: 读取配置文件 %s: %w", path, err)
	}
	return decodeConfig(path, contents)
}

func decodeConfig(path string, contents []byte) (Config, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(contents, &top); err != nil {
		return Config{}, fmt.Errorf("config: 解析配置文件: %w", err)
	}

	cfg := Defaults()

	version := CurrentVersion
	if raw, ok := lookupCaseInsensitive(top, "version"); ok {
		if err := json.Unmarshal(raw, &version); err != nil {
			return Config{}, fmt.Errorf("config: 解析 version 字段: %w", err)
		}
	}
	if version != CurrentVersion {
		return Config{}, fmt.Errorf(
			"config: 不支持的配置文件版本 %d，期望 %d；请升级到能识别该版本的程序，"+
				"或删除 %s 让程序按编译默认值重新开始",
			version, CurrentVersion, path)
	}
	cfg.Version = CurrentVersion

	if raw, ok := lookupCaseInsensitive(top, "logging"); ok {
		if err := applyLogging(&cfg, raw); err != nil {
			return Config{}, fmt.Errorf("config: 解析 logging 字段: %w", err)
		}
	}
	if raw, ok := lookupCaseInsensitive(top, "ai"); ok {
		if err := applyAI(&cfg, raw, path); err != nil {
			return Config{}, fmt.Errorf("config: 解析 ai 字段: %w", err)
		}
	}
	if raw, ok := lookupCaseInsensitive(top, "texturePackPath"); ok {
		var texturePackPath *string
		if err := json.Unmarshal(raw, &texturePackPath); err != nil {
			return Config{}, fmt.Errorf("config: 解析 texturePackPath 字段: %w", err)
		}
		if texturePackPath == nil {
			return Config{}, errors.New("config: 解析 texturePackPath 字段: 必须是字符串")
		}
		cfg.TexturePackPath = *texturePackPath
		if cfg.TexturePackPath != "" {
			if filepath.IsAbs(cfg.TexturePackPath) {
				cfg.ResolvedTexturePackPath = filepath.Clean(cfg.TexturePackPath)
			} else {
				resolved, err := filepath.Abs(filepath.Join(filepath.Dir(path), cfg.TexturePackPath))
				if err != nil {
					return Config{}, fmt.Errorf("config: 解析 texturePackPath 字段: %w", err)
				}
				cfg.ResolvedTexturePackPath = resolved
			}
		}
	}
	// fluidEnabled 是独立顶层布尔字段，不经 applyGroups 的数值钳制路径：
	// 类型错误（如写成字符串）与 version 字段一样直接报错，不做静默降级——
	// 这是一个门控生成行为的开关，写错类型多半是配置文件手误，应该在加载
	// 时暴露而不是悄悄回落默认值。
	if raw, ok := lookupCaseInsensitive(top, "fluidEnabled"); ok {
		if err := json.Unmarshal(raw, &cfg.FluidEnabled); err != nil {
			return Config{}, fmt.Errorf("config: 解析 fluidEnabled 字段: %w", err)
		}
	}
	if err := applyGroups(&cfg, top); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	// 兜底归一：无论键缺席、解析告警回退还是默认值，出函数的 Render.LOD
	// 三字段必须落在合法域内（cmd/mornlea 直接用它派生雾距离与环形半径，
	// 越界值会在渲染器侧触发 panic 契约）。对已合法值是恒等变换。
	cfg.Render = cfg.Render.NormalizeLOD()
	warnUnknownTopLevel(top)

	return cfg, nil
}

func loadDefaultFromPaths(current, legacy string, publish func(string, []byte) (bool, error)) (Config, error) {
	return loadDefaultFromPathsWithOpen(current, legacy, publish, os.Open)
}

func loadDefaultFromPathsWithOpen(current, legacy string, publish func(string, []byte) (bool, error), open func(string) (*os.File, error)) (Config, error) {
	if err := validateDefaultConfigParent(current); err != nil {
		return Config{}, err
	}
	cfg, exists, err := readDefaultConfigIfExistsWithOpen(current, open)
	if err != nil {
		return Config{}, err
	}
	if exists {
		return cfg, nil
	}

	legacyContents, err := os.ReadFile(legacy)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: 读取旧配置文件 %s: %w", legacy, err)
	}
	legacyConfig, err := decodeConfig(legacy, legacyContents)
	if err != nil {
		return Config{}, fmt.Errorf("config: 解码旧配置文件 %s: %w", legacy, err)
	}
	contents, err := json.MarshalIndent(legacyConfig, "", "  ")
	if err != nil {
		return Config{}, fmt.Errorf("config: 序列化旧配置文件 %s: %w", legacy, err)
	}
	published, err := publish(current, contents)
	if err != nil {
		return Config{}, err
	}
	if err := validateDefaultConfigParent(current); err != nil {
		return Config{}, err
	}
	cfg, exists, err = readDefaultConfigIfExistsWithOpen(current, open)
	if err != nil {
		return Config{}, err
	}
	if !exists {
		return Config{}, fmt.Errorf("config: 读取默认配置文件 %s: %w", current, fs.ErrNotExist)
	}
	if published {
		slog.Info("旧配置已迁移到 Mornlea 默认路径", "legacy_path", legacy, "current_path", current)
	}
	return cfg, nil
}

func readDefaultConfigIfExistsWithOpen(path string, open func(string) (*os.File, error)) (Config, bool, error) {
	checked, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, fmt.Errorf("config: 检查默认配置文件 %s: %w", path, err)
	}
	if err := validateDefaultConfigFile(path, checked); err != nil {
		return Config{}, false, err
	}

	file, err := open(path)
	if err != nil {
		return Config{}, false, fmt.Errorf("config: 打开默认配置文件 %s: %w", path, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return Config{}, false, fmt.Errorf("config: 检查已打开默认配置文件 %s: %w", path, err)
	}
	if err := validateDefaultConfigFile(path, opened); err != nil {
		return Config{}, false, err
	}
	if !os.SameFile(checked, opened) {
		return Config{}, false, fmt.Errorf("config: 默认配置文件 %s 在校验后已被替换: %w", path, fs.ErrPermission)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return Config{}, false, fmt.Errorf("config: 重新检查已打开默认配置文件 %s: %w", path, err)
	}
	if err := validateDefaultConfigFile(path, current); err != nil {
		return Config{}, false, err
	}
	if !os.SameFile(current, opened) {
		return Config{}, false, fmt.Errorf("config: 默认配置文件 %s 在打开后已被替换: %w", path, fs.ErrPermission)
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		return Config{}, false, fmt.Errorf("config: 读取默认配置文件 %s: %w", path, err)
	}
	cfg, err := decodeConfig(path, contents)
	if err != nil {
		return Config{}, false, fmt.Errorf("config: 解码默认配置文件 %s: %w", path, err)
	}
	return cfg, true, nil
}

func validateDefaultConfigFile(path string, info fs.FileInfo) error {
	mode := info.Mode()
	if mode&os.ModeSymlink != 0 || !mode.IsRegular() || mode.Perm() != 0o600 {
		return fmt.Errorf("config: 默认配置文件 %s 权限不安全（mode %s，期望 regular 0600）: %w",
			path, mode, fs.ErrPermission)
	}
	return nil
}

func validateDefaultConfigParent(path string) error {
	parent := filepath.Dir(path)
	info, err := os.Stat(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("config: 检查默认配置目录 %s: %w", parent, err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("config: 默认配置路径 %s 的父目录 %s 权限不安全（实际 %04o，期望 0700）: %w",
			path, parent, info.Mode().Perm(), fs.ErrPermission)
	}
	return nil
}

func publishConfigExclusively(path string, contents []byte) (bool, error) {
	return publishConfigExclusivelyWithLink(path, contents, os.Link)
}

func publishConfigExclusivelyWithLink(path string, contents []byte, link func(string, string) error) (published bool, err error) {
	return publishConfigExclusivelyWithLinkAndSync(path, contents, link, syncConfigDirectory)
}

func publishConfigExclusivelyWithLinkAndSync(path string, contents []byte, link func(string, string) error, syncParent func(string) error) (published bool, err error) {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return false, fmt.Errorf("config: 创建默认配置目录 %s: %w", parent, err)
	}
	if err := validateDefaultConfigParent(path); err != nil {
		return false, err
	}

	temp, err := os.CreateTemp(parent, "config-*.json.tmp")
	if err != nil {
		return false, fmt.Errorf("config: 创建默认配置临时文件 %s: %w", path, err)
	}
	tempPath := temp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
		_ = os.Remove(tempPath)
	}()

	if err := temp.Chmod(0o600); err != nil {
		return false, fmt.Errorf("config: 设置默认配置临时文件权限 %s: %w", path, err)
	}
	if _, err := temp.Write(contents); err != nil {
		return false, fmt.Errorf("config: 写入默认配置临时文件 %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		return false, fmt.Errorf("config: 落盘默认配置临时文件 %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return false, fmt.Errorf("config: 关闭默认配置临时文件 %s: %w", path, err)
	}
	closed = true

	if err := link(tempPath, path); errors.Is(err, fs.ErrExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("config: 发布默认配置文件 %s: %w", path, err)
	}
	published = true
	if err := os.Remove(tempPath); err != nil {
		return true, fmt.Errorf("config: 清理默认配置临时文件 %s: %w", path, err)
	}
	if err := syncParent(parent); err != nil {
		return true, fmt.Errorf("config: 落盘默认配置文件 %s 的父目录 %s: %w", path, parent, err)
	}
	return true, nil
}

func syncConfigDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return err
	}
	return directory.Close()
}

// applyLogging 把 logging 分组的原始 JSON 解析进 cfg.Logging。未知等级文本
// 落回默认并告警，不会让 Load 整体失败。
func applyLogging(cfg *Config, raw json.RawMessage) error {
	var decoded rawLogging
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	if decoded.Default != "" {
		if level, err := logging.ParseLevel(decoded.Default); err != nil {
			slog.Warn("未知日志等级已回退默认值", "field", "logging.default", "value", decoded.Default)
		} else {
			cfg.Logging.Default = level
		}
	}
	if decoded.Modules != nil {
		modules := make(map[string]slog.Level, len(decoded.Modules))
		for name, text := range decoded.Modules {
			level, err := logging.ParseLevel(text)
			if err != nil {
				slog.Warn("未知日志等级已回退默认值", "field", "logging.modules."+name, "value", text)
				continue
			}
			modules[name] = level
		}
		cfg.Logging.Modules = modules
	}
	return nil
}

// knownAIFieldKeys 是 ai 分组已识别的键（大小写不敏感）。未列出的键按既有
// 未知字段纪律告警后忽略。伙伴条目内的已识别键（id/name/persona）见
// applyAI 的条目级检查。
var knownAIFieldKeys = []string{
	"companions",
	"endpoint",
	"model",
	"apiKeyEnv",
	"taskTimeoutMinutes",
}

// knownAIField 报告 key（任意大小写）是否为 ai 分组的已识别键。
func knownAIField(key string) bool {
	for _, known := range knownAIFieldKeys {
		if strings.EqualFold(key, known) {
			return true
		}
	}
	return false
}

// applyAI 解析 ai 分组：M5A 的 companions[].id/name、M5B 的四个模型运行时
// 字段（endpoint/model/apiKeyEnv/taskTimeoutMinutes）与 M5D 的可选
// companions[].persona（均大小写不敏感）。
//
// 模型字段只要出现就立即校验语法（endpoint 形态、超时区间），错误带
// ai.endpoint 等精确路径，让配置问题在读文件时暴露而不是等到启动；而
// endpoint/model/apiKeyEnv 的完整性（非空伙伴时必须齐全、https 必须配
// apiKeyEnv）在确认伙伴列表非空后才检查——AI 关闭时孤立的模型字段只做语法
// 校验、不启用 AI，也不要求任何模型字段。密钥值永远不进配置文件，这里只
// 处理环境变量名。
//
// persona 只做 JSON 形状解析，内容校验与外部文件优先级在 resolvePersonas
// 中按宽松纪律完成（告警降级，不阻止启动）。
func applyAI(cfg *Config, raw json.RawMessage, configPath string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for key := range fields {
		if !knownAIField(key) {
			slog.Warn("配置项未知字段已忽略", "field", "ai."+key)
		}
	}
	var settings companion.ModelSettings
	if value, exists := lookupCaseInsensitive(fields, "endpoint"); exists {
		if err := json.Unmarshal(value, &settings.Endpoint); err != nil {
			return fmt.Errorf("解析 ai.endpoint: %w", err)
		}
		if err := companion.ValidateModelEndpoint(settings.Endpoint); err != nil {
			return fmt.Errorf("ai.endpoint: %w", err)
		}
	}
	if value, exists := lookupCaseInsensitive(fields, "model"); exists {
		if err := json.Unmarshal(value, &settings.Model); err != nil {
			return fmt.Errorf("解析 ai.model: %w", err)
		}
	}
	if value, exists := lookupCaseInsensitive(fields, "apiKeyEnv"); exists {
		if err := json.Unmarshal(value, &settings.APIKeyEnv); err != nil {
			return fmt.Errorf("解析 ai.apiKeyEnv: %w", err)
		}
	}
	if value, exists := lookupCaseInsensitive(fields, "taskTimeoutMinutes"); exists {
		var minutes int
		if err := json.Unmarshal(value, &minutes); err != nil {
			return fmt.Errorf("解析 ai.taskTimeoutMinutes: %w", err)
		}
		// 显式 0 也拒绝："0=未设置"只对字段缺席成立。显式写 0 几乎必然是想
		// 表达别的意思（单位搞错、漏填数字），按错误暴露而不是悄悄落回默认。
		if err := companion.ValidateTaskTimeoutMinutes(minutes); err != nil {
			return fmt.Errorf("ai.taskTimeoutMinutes: %w", err)
		}
		settings.TaskTimeoutMinutes = minutes
	}
	rawCompanions, ok := lookupCaseInsensitive(fields, "companions")
	if !ok || string(rawCompanions) == "null" {
		return nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(rawCompanions, &entries); err != nil {
		return fmt.Errorf("解析 ai.companions: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	definitions := make([]companion.Definition, len(entries))
	for index, entry := range entries {
		var definitionFields map[string]json.RawMessage
		if err := json.Unmarshal(entry, &definitionFields); err != nil {
			return fmt.Errorf("解析 ai.companions[%d]: %w", index, err)
		}
		for key := range definitionFields {
			if !strings.EqualFold(key, "id") && !strings.EqualFold(key, "name") &&
				!strings.EqualFold(key, "persona") {
				slog.Warn("配置项未知字段已忽略", "field", fmt.Sprintf("ai.companions[%d].%s", index, key))
			}
		}
		if value, exists := lookupCaseInsensitive(definitionFields, "id"); exists {
			if err := json.Unmarshal(value, &definitions[index].ID); err != nil {
				return fmt.Errorf("解析 ai.companions[%d].id: %w", index, err)
			}
		}
		if value, exists := lookupCaseInsensitive(definitionFields, "name"); exists {
			if err := json.Unmarshal(value, &definitions[index].Name); err != nil {
				return fmt.Errorf("解析 ai.companions[%d].name: %w", index, err)
			}
		}
		// persona 形状错误（如数字）与 id/name 一样按解析错误拒绝；内容越界
		// （超长/NUL/非法 UTF-8）则留给 resolvePersonas 按宽松纪律告警降级。
		// JSON null 与字段缺席等价：Unmarshal 保持零值空串=无内联人设。
		if value, exists := lookupCaseInsensitive(definitionFields, "persona"); exists {
			if err := json.Unmarshal(value, &definitions[index].Persona); err != nil {
				return fmt.Errorf("解析 ai.companions[%d].persona: %w", index, err)
			}
		}
	}
	// 先验证伙伴定义（M5A 语义），再检查模型设置完整性：重复名称等定义错误
	// 优先暴露，与既有错误路径保持一致。
	if err := companion.ValidateDefinitions(definitions); err != nil {
		return err
	}
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("ai: %w", err)
	}
	resolvePersonas(configPath, definitions)
	cfg.AI = &AI{ModelSettings: settings, Companions: definitions}
	return nil
}

// resolvePersonas 在启动加载配置时为每个伙伴定义解析生效人设并写入
// ResolvedPersona：内联 ai.companions[].persona 优先，其次读取配置文件所在
// 目录下 personas/<canonical 名称>.txt，两者皆无则为空人设。
//
// 只写 ResolvedPersona、绝不改写 Persona：后者是磁盘镜像（内联原文，含越界
// 原文），Save 与旧配置迁移会全量序列化它——若把生效值（文件全文或降级后
// 的空串）写回去，外部文件内容会被静默吸收为内联、越界原文会被从磁盘
// 清除，两者都是对用户数据的静默篡改。
//
// 宽松纪律：任何人设问题（内联或文件越界、损坏、不可读）都只 slog.Warn 后
// 降级为空人设，绝不阻止启动；告警只引用字段路径或文件路径与原因，绝不
// 回显人设文本（persona 不外泄原则）。本函数只在 Load 时随 applyAI 执行
// 一次，运行期不做热更新。configPath 为配置文件本身的路径，外部文件相对
// 它定位；调用前定义已通过 ValidateDefinitions（canonical 名称、无空白）。
func resolvePersonas(configPath string, definitions []companion.Definition) {
	personasDir := filepath.Join(filepath.Dir(configPath), "personas")
	for index := range definitions {
		definitions[index].ResolvedPersona = resolveDefinitionPersona(personasDir, index, definitions[index])
	}
}

// resolveDefinitionPersona 解析单个伙伴的生效人设（ResolvedPersona），优先级
// 与降级规则见 resolvePersonas 的说明。内联为空串表示字段缺席或显式空——
// 两种情形都让外部文件有机会生效；内联存在（即使内容越界被降级）则不再读
// 文件，双源语义保持"内联优先、降级不回退文件"。
func resolveDefinitionPersona(personasDir string, index int, definition companion.Definition) string {
	if definition.Persona != "" {
		if err := companion.ValidatePersona(definition.Persona); err != nil {
			slog.Warn("伙伴内联人设无效，已按空人设处理",
				"field", fmt.Sprintf("ai.companions[%d].persona", index),
				"companion", definition.Name,
				"reason", err.Error())
			return ""
		}
		if path := personaFilePath(personasDir, definition.Name); path != "" {
			if _, err := os.Stat(path); err == nil {
				slog.Warn("内联 persona 优先生效，已忽略外部人设文件",
					"path", path, "companion", definition.Name)
			}
		}
		return definition.Persona
	}
	path := personaFilePath(personasDir, definition.Name)
	if path == "" {
		return ""
	}
	// 先按 Stat 预检大小再读：人设文件上界只有 4 KiB，预检避免误放的巨型
	// 文件在启动时造成无界内存分配。Stat 失败（含不存在）交给 ReadFile 统一
	// 裁决，避免两套存在性判断漂移。
	if info, err := os.Stat(path); err == nil && info.Size() > companion.MaxPersonaBytes {
		slog.Warn("外部人设文件超过大小上限，已按空人设处理",
			"path", path, "companion", definition.Name,
			"reason", fmt.Sprintf("文件 %d 字节超过上限 %d", info.Size(), companion.MaxPersonaBytes))
		return ""
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "" // 文件不存在是常态：静默空人设，不告警。
		}
		slog.Warn("外部人设文件不可读，已按空人设处理",
			"path", path, "companion", definition.Name, "reason", err.Error())
		return ""
	}
	if err := companion.ValidatePersona(string(contents)); err != nil {
		slog.Warn("外部人设文件内容无效，已按空人设处理",
			"path", path, "companion", definition.Name, "reason", err.Error())
		return ""
	}
	return string(contents)
}

// personaFilePath 返回 canonical 名称对应的外部人设文件路径。
//
// 安全边界：ValidateName 只保证 canonical 与无空白，并不拒绝名称中的路径
// 分隔符（"../sneaky" 是合法伙伴名），直接拼接会拼出逃出 personas/ 的路径。
// 因此含分隔符（含 Windows 的反斜杠，防跨平台误用）或空名称一律返回空串，
// 调用方按"无外部文件"静默处理——文件名约定是平面单文件，无法映射的名称
// 没有外部文件语义，也绝不构成路径穿越面。
func personaFilePath(personasDir, name string) string {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return ""
	}
	return filepath.Join(personasDir, name+".txt")
}

// applyGroups 把 physics/sim/render 三个分组的原始 JSON 应用到 cfg。
//
// Fields() 是这三个分组已知字段与钳制区间的唯一权威来源，这里同时拿它做“未知
// 字段识别”与“越界钳制”，不为同一件事各写一份判断。
//
// 之所以不直接 json.Unmarshal 进 physics.Tunables/sim.Tunables/Render，是因为
// encoding/json 自身的整数范围检查发生在钳制之前：越界或负值会让 Unmarshal 直
// 接返回错误，越界值根本走不到钳制逻辑（例如 uint8 字段收到 300 或 -5）。这里
// 改为先把逐字段的原始 JSON 值解码成 float64（float64 不做范围收窄，不会因为
// 数值偏大或为负而报错），在 float64 空间里完成钳制，再用 setFloat 写回真实字
// 段——这样越界值永远不会在钳制之前就被写进窄整数字段。
func applyGroups(cfg *Config, top map[string]json.RawMessage) error {
	rawGroups := make(map[string]map[string]json.RawMessage, 3)
	for _, group := range []string{"physics", "sim", "render"} {
		raw, ok := lookupCaseInsensitive(top, group)
		if !ok {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("解析 %s 字段: %w", group, err)
		}
		rawGroups[group] = fields
	}

	known := make(map[string]bool, len(Fields())+3)
	for _, field := range Fields() {
		known[field.Group+"."+strings.ToLower(field.Name)] = true

		fields, ok := rawGroups[field.Group]
		if !ok {
			continue // 该分组整体未出现在 JSON 中，保留默认值。
		}
		raw, ok := lookupCaseInsensitive(fields, field.Name)
		if !ok {
			continue // 该字段未出现在 JSON 中，保留默认值。
		}

		var value float64
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("字段 %s.%s 不是合法数值: %w", field.Group, field.Name, err)
		}
		clamped := value
		if clamped < field.Min {
			clamped = field.Min
		}
		if clamped > field.Max {
			clamped = field.Max
		}
		if clamped != value {
			slog.Warn("配置项越界已钳制", "field", field.Group+"."+field.Name, "value", value, "clamped", clamped)
		}

		target := groupValue(cfg, field.Group).FieldByName(exportedFieldName(field.Name))
		if !target.IsValid() {
			// 不应该发生：Fields() 与对应结构体字段必须保持同步，
			// 出现这种情况说明 Fields() 里的 Name 拼错了，直接 panic 暴露问题，
			// 而不是悄悄让这个字段永远不被钳制。
			panic("config: Fields() 声明的字段在结构体中不存在: " + field.Group + "." + field.Name)
		}
		setFloat(target, clamped)
	}

	// LOD 三键不在 Fields() 里（布尔/离散集与连续 min/max 钳制语义不兼容），
	// 但它们是 render 分组的已识别键：单独解析并计入 known（键名同样按
	// 小写登记，与上方 Fields() 条目及未知字段判定的大小写规则一致），
	// 否则会被未知字段告警误报、值也悄悄丢失。
	for _, key := range []string{"render.lodenabled", "render.lodfarmultiplier", "render.lodstep"} {
		known[key] = true
	}
	if renderFields, ok := rawGroups["render"]; ok {
		if err := applyRenderLOD(&cfg.Render, renderFields); err != nil {
			return err
		}
	}

	for group, fields := range rawGroups {
		for key := range fields {
			if !known[group+"."+strings.ToLower(key)] {
				slog.Warn("配置项未知字段已忽略", "field", group+"."+key)
			}
		}
	}
	return nil
}

// applyRenderLOD 解析 render 分组的三个远环 LOD 键（大小写不敏感，与
// physics/sim/render 既有键同一纪律）：
//
//   - lodEnabled 是布尔；解析失败告警并保留默认（Load 不因此失败）。
//   - lodFarMultiplier 按数值解码后经 Render.NormalizeLOD 的同一区间钳制，
//     越界告警不报错（与 Fields() 字段的「越界已钳制」纪律一致）。
//   - lodStep 只接受离散集 {2,4,8}（整数)；非法值告警并落回默认 4，
//     镜像 logging 未知等级的「落回默认并告警」纪律——步长不是连续区间，
//     钳制语义对它不成立。
//
// 最终域保证由 decodeConfig 末尾的 NormalizeLOD 兜底，本函数只负责带
// 精确字段路径的告警。
func applyRenderLOD(render *Render, fields map[string]json.RawMessage) error {
	if raw, ok := lookupCaseInsensitive(fields, "lodEnabled"); ok {
		var enabled bool
		if err := json.Unmarshal(raw, &enabled); err != nil {
			slog.Warn("配置项类型非法已忽略", "field", "render.lodEnabled", "want", "布尔")
		} else {
			render.LodEnabled = enabled
		}
	}
	if raw, ok := lookupCaseInsensitive(fields, "lodFarMultiplier"); ok {
		var multiplier float64
		if err := json.Unmarshal(raw, &multiplier); err != nil {
			return fmt.Errorf("字段 render.lodFarMultiplier 不是合法数值: %w", err)
		}
		clamped := multiplier
		if clamped < LodFarMultiplierMin {
			clamped = LodFarMultiplierMin
		}
		if clamped > LodFarMultiplierMax {
			clamped = LodFarMultiplierMax
		}
		if clamped != multiplier {
			slog.Warn("配置项越界已钳制", "field", "render.lodFarMultiplier",
				"value", multiplier, "clamped", clamped)
		}
		render.LodFarMultiplier = int(clamped)
	}
	if raw, ok := lookupCaseInsensitive(fields, "lodStep"); ok {
		var step float64
		if err := json.Unmarshal(raw, &step); err != nil {
			return fmt.Errorf("字段 render.lodStep 不是合法数值: %w", err)
		}
		switch step {
		case 2, 4, 8:
			render.LodStep = int(step)
		default:
			slog.Warn("lodStep 非法已落回默认值", "field", "render.lodStep",
				"value", step, "default", LodStepDefault, "legal", "{2,4,8}")
		}
	}
	return nil
}

// warnUnknownTopLevel 对不认识的顶层分组名 slog.Warn。
func warnUnknownTopLevel(top map[string]json.RawMessage) {
	known := map[string]bool{"version": true, "logging": true, "physics": true, "sim": true, "render": true, "ai": true, "texturepackpath": true, "fluidenabled": true}
	for key := range top {
		if !known[strings.ToLower(key)] {
			slog.Warn("配置项未知字段已忽略", "field", key)
		}
	}
}

// CompanionDefinitions 返回当前配置中的伙伴定义；缺失或禁用时返回 nil。
// 消费方（后续 Dialogue 等）应读取 Definition.ResolvedPersona（生效人设）；
// Definition.Persona 只是磁盘内联原文镜像，可能越界，不作运行期人设使用。
func (c Config) CompanionDefinitions() []companion.Definition {
	if c.AI == nil {
		return nil
	}
	return slices.Clone(c.AI.Companions)
}

// NormalizeLOD 返回远环 LOD 三字段归一到合法域后的 Render 副本：
// LodFarMultiplier 钳制到 [LodFarMultiplierMin, LodFarMultiplierMax]，
// LodStep 只接受离散集 {2,4,8}、其余落回 LodStepDefault，LodEnabled 原样
// 保留。Load 的解析与 cmd/mornlea 的装配点（防御直接构造 Render 的调用方）
// 共用这一份规则，避免同一约束出现两处数字。已合法的输入是恒等变换。
func (r Render) NormalizeLOD() Render {
	normalized := r
	if normalized.LodFarMultiplier < LodFarMultiplierMin {
		normalized.LodFarMultiplier = LodFarMultiplierMin
	}
	if normalized.LodFarMultiplier > LodFarMultiplierMax {
		normalized.LodFarMultiplier = LodFarMultiplierMax
	}
	switch normalized.LodStep {
	case 2, 4, 8:
	default:
		normalized.LodStep = LodStepDefault
	}
	return normalized
}

func lookupCaseInsensitive(m map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if raw, ok := m[key]; ok {
		return raw, true
	}
	for k, raw := range m {
		if strings.EqualFold(k, key) {
			return raw, true
		}
	}
	return nil, false
}

// Save 把配置原子写入 path：先在同目录创建临时文件、写入并 Sync，再 rename
// 到目标路径，避免进程崩溃或掉电时留下半截文件覆盖旧配置。失败路径会清理
// 临时文件。
func (c Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: 创建配置目录: %w", err)
	}
	temp, err := os.CreateTemp(dir, "config-*.json.tmp")
	if err != nil {
		return fmt.Errorf("config: 创建临时文件: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath) // rename 成功后 tempPath 已不存在，Remove 静默失败；仅在失败路径实际清理。

	body, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		temp.Close()
		return fmt.Errorf("config: 序列化配置: %w", err)
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return fmt.Errorf("config: 写入临时文件: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("config: 落盘临时文件: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("config: 关闭临时文件: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("config: 替换配置文件: %w", err)
	}
	return nil
}

// Apply 把当前配置值写入 physics 与 sim 的运行时快照，供权威 tick 立即生效。
// render 组不在这里处理：它是纯数据，由 cmd/mornlea 自行消费。
func (c Config) Apply() {
	physics.SetTunables(c.Physics)
	sim.SetTunables(c.Sim)
}

// Field 描述一个可调项在调试面板中的区间与步长；同一份定义也用于 Load 时的
// 越界钳制，因此面板与钳制逻辑不会出现两处数字不一致。
//
// Name 是小写开头的驼峰名；对应的 Go 结构体字段名只是把首字母大写
// （例如 spawnRadius -> SpawnRadius），Fields 与钳制逻辑都靠这条规则通过反射
// 定位字段，不需要为每个字段各写一段 getter/setter。
type Field struct {
	Group    string
	Name     string
	Min      float64
	Max      float64
	Step     float64
	ReadOnly bool
}

// Fields 返回全部可调项的区间与步长定义。
//
// 以下区间来自评审定死的存盘/权威 tick 安全约束，不可自行放宽：
//   - sim.furnaceSmeltTicks 上限取 core.FurnaceSmeltTicks，超过会让
//     world.FurnaceSlot.Valid() 拒绝该值，导致区块存盘失败。
//   - sim.furnaceBurnTicks 上限取 core.FurnaceBurnTicks，理由同上。
//   - sim.regenIntervalTicks 下限为 1：internal/sim/health_regen.go 用它做
//     取模除数，0 会在权威 tick 内 panic。
//   - sim.drownDamageIntervalTicks 下限为 1：internal/sim/oxygen.go 用它做
//     「距上次溺水伤害多少 tick」的比较阈值，0 会退化成氧气一归零就每 tick 扣血。
//   - sim.spawnRadius 区间为 1..64：internal/sim/spawn.go 用它按平方分配切片，
//     不钳制会触发巨额分配。
//   - sim.dropPickupDelayTicks 与 sim.playerDropPickupDelayTicks 上限为 255：
//     由持久化的 1 字节字段 world.DropSlot.PickupDelayTicks 决定。
//
// 以下两项的区间不是安全约束，只是没有存盘/panic 风险时的合理操作区间，可
// 随实测调整（变更 authoritative-fluid 的 design.md D4 明确预算是估计值）：
//   - sim.fluidFlowDelayTicks 下限为 0（允许立即处理，不强制观感延迟）。
//   - sim.fluidUpdatesPerTick 下限为 1：0 会让流体待更新队列永远不推进，
//     internal/fluid.Queue.Advance 本身会把负预算钳到 0 但不会把 0 拒绝为
//     错误，这里在配置层面挡住"预算=0 等于永久卡死"这个几乎必错的取值。
//   - sim.fluidRescanCellsPerTick 下限同样为 1：重扫预算在区段边界检查，
//     取 1 也保证每 tick 至少推进一个区段，但取 0 会让待重扫队列永远排不空，
//     刚进入推进范围的区块里的水永远不会被唤醒。
//   - sim.randomTicksPerSection 区间为 0..64：取 0 是合法的调试取值（作物停止
//     生长、耕地不再转换干湿），不是错误；上限 64 只是操作区间，抽样对更大的
//     取值同样正确，但那已是默认值的 20 倍，再大只会线性烧掉权威 tick 预算。
//   - sim.cropGrowthChancePercent 区间为 0..100：它是一个百分比，两端都有明确
//     语义（0 = 永不推进，100 = 抽中即推进），越界值没有任何解释。
//   - sim.starvationDamageIntervalTicks 下限为 1：internal/sim/hunger.go 用它做
//     「距上次饥饿伤害多少 tick」的比较阈值，0 会退化成饥饿一归零就每 tick 扣血。
//   - sim.exhaustionThresholdMilli 下限为 1000：internal/sim/hunger.go 的
//     applyExhaustion 以它为循环条件，0 会让循环在权威 tick 内永不退出。下限取
//     1000（一点饱和度的千分位）而不是 1，是因为再小的阈值意味着「一次跳跃烧掉
//     50 点饱和度」，那不是调参而是坏配置。
//   - sim.regenHungerThreshold 区间为 0..core.MaxHunger：两端都是合法调试取值
//     （0 = 取消门控，20 = 只有吃饱才回血），越界值没有语义。
//   - sim.eatingTicks 下限为 1：internal/sim/eating.go 的 `advanceEating` 以它为
//     结算阈值，而进度从 1 起，0 会让进食永远结算不了。上限 200（10 秒）只是
//     操作区间，再长的进食在受伤中断规则下等于吃不完。
func Fields() []Field {
	return []Field{
		{Group: "physics", Name: "eyeHeight", Min: 1, Max: 2.2, Step: 0.01},
		{Group: "physics", Name: "stepHeight", Min: 0, Max: 1.5, Step: 0.05},
		{Group: "physics", Name: "walkSpeed", Min: 0.5, Max: 20, Step: 0.1},
		{Group: "physics", Name: "groundAcceleration", Min: 1, Max: 200, Step: 1},
		{Group: "physics", Name: "groundDeceleration", Min: 1, Max: 200, Step: 1},
		{Group: "physics", Name: "airAcceleration", Min: 1, Max: 100, Step: 1},
		{Group: "physics", Name: "jumpSpeed", Min: 1, Max: 30, Step: 0.1},
		{Group: "physics", Name: "gravity", Min: 1, Max: 100, Step: 1},
		{Group: "physics", Name: "terminalFallSpeed", Min: 1, Max: 200, Step: 1},
		// 以下四项只在身体浸没时生效。区间不是安全约束，只是合理操作区间：
		// fluidHorizontalDrag 上界钳到 1，因为 >1 会把水中水平速度放大到超过
		// 陆地行走速度，直接违反 fluid-survival 的「水中水平移动更慢」。
		{Group: "physics", Name: "fluidGravity", Min: 0, Max: 100, Step: 0.1},
		{Group: "physics", Name: "fluidSinkSpeed", Min: 0, Max: 200, Step: 0.1},
		{Group: "physics", Name: "fluidAscendSpeed", Min: 0, Max: 30, Step: 0.1},
		{Group: "physics", Name: "fluidHorizontalDrag", Min: 0, Max: 1, Step: 0.05},

		{Group: "sim", Name: "interactionReach", Min: 1, Max: 32, Step: 0.5},
		{Group: "sim", Name: "regenDelayTicks", Min: 0, Max: 2000, Step: 1},
		{Group: "sim", Name: "regenIntervalTicks", Min: 1, Max: 600, Step: 1},
		{Group: "sim", Name: "drownDamageIntervalTicks", Min: 1, Max: 600, Step: 1},
		{Group: "sim", Name: "dropPickupDelayTicks", Min: 0, Max: 255, Step: 1},
		{Group: "sim", Name: "playerDropPickupDelayTicks", Min: 0, Max: 255, Step: 1},
		{Group: "sim", Name: "dropLifetimeTicks", Min: 1, Max: 120000, Step: 100},
		{Group: "sim", Name: "dropPickupRange", Min: 0.1, Max: 16, Step: 0.05},
		{Group: "sim", Name: "spawnRadius", Min: 1, Max: 64, Step: 1},
		{Group: "sim", Name: "furnaceSmeltTicks", Min: 1, Max: float64(core.FurnaceSmeltTicks), Step: 1},
		{Group: "sim", Name: "furnaceBurnTicks", Min: 1, Max: float64(core.FurnaceBurnTicks), Step: 1},
		{Group: "sim", Name: "fluidFlowDelayTicks", Min: 0, Max: 2000, Step: 1},
		{Group: "sim", Name: "fluidUpdatesPerTick", Min: 1, Max: 65536, Step: 1},
		{Group: "sim", Name: "fluidRescanCellsPerTick", Min: 1, Max: 1048576, Step: 1024},
		{Group: "sim", Name: "randomTicksPerSection", Min: 0, Max: 64, Step: 1},
		{Group: "sim", Name: "cropGrowthChancePercent", Min: 0, Max: 100, Step: 1},
		{Group: "sim", Name: "starvationDamageIntervalTicks", Min: 1, Max: 2000, Step: 1},
		{Group: "sim", Name: "exhaustionThresholdMilli", Min: 1000, Max: 20000, Step: 100},
		{Group: "sim", Name: "regenHungerThreshold", Min: 0, Max: float64(core.MaxHunger), Step: 1},
		{Group: "sim", Name: "eatingTicks", Min: 1, Max: 200, Step: 1},

		{Group: "render", Name: "viewDistance", Min: 2, Max: 64, Step: 1, ReadOnly: true},
		{Group: "render", Name: "fovDegrees", Min: 30, Max: 110, Step: 1},
		{Group: "render", Name: "mouseSensitivity", Min: 0.1, Max: 5, Step: 0.1},
	}
}

// groupValue 返回 cfg 中某个分组结构体的可寻址反射值。
func groupValue(cfg *Config, group string) reflect.Value {
	switch group {
	case "physics":
		return reflect.ValueOf(&cfg.Physics).Elem()
	case "sim":
		return reflect.ValueOf(&cfg.Sim).Elem()
	case "render":
		return reflect.ValueOf(&cfg.Render).Elem()
	default:
		panic("config: 未知字段分组 " + group)
	}
}

// exportedFieldName 把小写开头的驼峰名转换成导出的 Go 字段名。
func exportedFieldName(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// setFloat 把钳制后的 float64 写回原字段的实际数值类型。
func setFloat(v reflect.Value, value float64) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		v.SetFloat(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(value))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(value))
	default:
		panic("config: 不支持的字段类型 " + v.Kind().String())
	}
}
