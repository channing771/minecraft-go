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

// Render 是渲染相关的可调项。它是一个纯数据结构体，不依赖 internal/render，
// 由 cmd/mornlea 自行读取并消费——这样 mornlea-server（无图形专用服务端）也能导入 config
// 而不会传递性拖入图形依赖。
//
// json tag 与 Fields() 的 Name 逐字对应，保证配置文件写出的键名就是设计文档
// 与 README 里写的小写驼峰；读取侧大小写不敏感，加 tag 之前写出的文件仍可
// 正常读入。
type Render struct {
	ViewDistance     int     `json:"viewDistance"`
	FovDegrees       float32 `json:"fovDegrees"`
	MouseSensitivity float32 `json:"mouseSensitivity"`
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
		},
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
		if err := applyAI(&cfg, raw); err != nil {
			return Config{}, fmt.Errorf("config: 解析 ai 字段: %w", err)
		}
	}
	if err := applyGroups(&cfg, top); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
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
// 未知字段纪律告警后忽略——persona 等未交付字段继续走这条路径。
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

// applyAI 解析 ai 分组：M5A 的 companions[].id/name 与 M5B 的四个模型运行时
// 字段（endpoint/model/apiKeyEnv/taskTimeoutMinutes，均大小写不敏感）。
//
// 模型字段只要出现就立即校验语法（endpoint 形态、超时区间），错误带
// ai.endpoint 等精确路径，让配置问题在读文件时暴露而不是等到启动；而
// endpoint/model/apiKeyEnv 的完整性（非空伙伴时必须齐全、https 必须配
// apiKeyEnv）在确认伙伴列表非空后才检查——AI 关闭时孤立的模型字段只做语法
// 校验、不启用 AI，也不要求任何模型字段。密钥值永远不进配置文件，这里只
// 处理环境变量名。
func applyAI(cfg *Config, raw json.RawMessage) error {
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
			if !strings.EqualFold(key, "id") && !strings.EqualFold(key, "name") {
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
	}
	// 先验证伙伴定义（M5A 语义），再检查模型设置完整性：重复名称等定义错误
	// 优先暴露，与既有错误路径保持一致。
	if err := companion.ValidateDefinitions(definitions); err != nil {
		return err
	}
	if err := settings.Validate(); err != nil {
		return fmt.Errorf("ai: %w", err)
	}
	cfg.AI = &AI{ModelSettings: settings, Companions: definitions}
	return nil
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

	known := make(map[string]bool, len(Fields()))
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

	for group, fields := range rawGroups {
		for key := range fields {
			if !known[group+"."+strings.ToLower(key)] {
				slog.Warn("配置项未知字段已忽略", "field", group+"."+key)
			}
		}
	}
	return nil
}

// warnUnknownTopLevel 对不认识的顶层分组名 slog.Warn。
func warnUnknownTopLevel(top map[string]json.RawMessage) {
	known := map[string]bool{"version": true, "logging": true, "physics": true, "sim": true, "render": true, "ai": true}
	for key := range top {
		if !known[strings.ToLower(key)] {
			slog.Warn("配置项未知字段已忽略", "field", key)
		}
	}
}

// CompanionDefinitions 返回当前配置中的伙伴定义；缺失或禁用时返回 nil。
func (c Config) CompanionDefinitions() []companion.Definition {
	if c.AI == nil {
		return nil
	}
	return slices.Clone(c.AI.Companions)
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
//   - sim.spawnRadius 区间为 1..64：internal/sim/spawn.go 用它按平方分配切片，
//     不钳制会触发巨额分配。
//   - sim.dropPickupDelayTicks 与 sim.playerDropPickupDelayTicks 上限为 255：
//     由持久化的 1 字节字段 world.DropSlot.PickupDelayTicks 决定。
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

		{Group: "sim", Name: "interactionReach", Min: 1, Max: 32, Step: 0.5},
		{Group: "sim", Name: "regenDelayTicks", Min: 0, Max: 2000, Step: 1},
		{Group: "sim", Name: "regenIntervalTicks", Min: 1, Max: 600, Step: 1},
		{Group: "sim", Name: "dropPickupDelayTicks", Min: 0, Max: 255, Step: 1},
		{Group: "sim", Name: "playerDropPickupDelayTicks", Min: 0, Max: 255, Step: 1},
		{Group: "sim", Name: "dropLifetimeTicks", Min: 1, Max: 120000, Step: 100},
		{Group: "sim", Name: "dropPickupRange", Min: 0.1, Max: 16, Step: 0.05},
		{Group: "sim", Name: "spawnRadius", Min: 1, Max: 64, Step: 1},
		{Group: "sim", Name: "furnaceSmeltTicks", Min: 1, Max: float64(core.FurnaceSmeltTicks), Step: 1},
		{Group: "sim", Name: "furnaceBurnTicks", Min: 1, Max: float64(core.FurnaceBurnTicks), Step: 1},

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
