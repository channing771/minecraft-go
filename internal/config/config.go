// Package config 负责读写调参配置文件，并把生效值写入各包的运行时快照。
//
// 本包只应被 cmd 导入。internal 下其他包一律不得依赖它——否则一台机器上的本地
// 调参会污染性能基线比对与抓帧 golden 比对，让自动化验证的结论取决于开发者本机
// 的配置文件内容。该约束由 internal/archcheck 守住。
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"minecraft-go/internal/core"
	"minecraft-go/internal/logging"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/sim"
)

// CurrentVersion 是本程序认识的配置文件版本。
const CurrentVersion = 1

// Render 是渲染相关的可调项。它是一个纯数据结构体，不依赖 internal/render，
// 由 cmd/mcgo 自行读取并消费——这样 mcgod（无图形专用服务端）也能导入 config
// 而不会传递性拖入图形依赖。
type Render struct {
	ViewDistance     int
	FovDegrees       float32
	MouseSensitivity float32
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

// DefaultPath 返回默认配置文件路径：用户配置目录下的 minecraft-go/config.json。
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: 定位用户配置目录: %w", err)
	}
	return filepath.Join(dir, "minecraft-go", "config.json"), nil
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
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults(), nil
		}
		return Config{}, fmt.Errorf("config: 读取配置文件: %w", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
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
		return Config{}, fmt.Errorf("config: 不支持的配置文件版本 %d，期望 %d", version, CurrentVersion)
	}
	cfg.Version = CurrentVersion

	if raw, ok := lookupCaseInsensitive(top, "logging"); ok {
		if err := applyLogging(&cfg, raw); err != nil {
			return Config{}, fmt.Errorf("config: 解析 logging 字段: %w", err)
		}
	}
	if raw, ok := lookupCaseInsensitive(top, "physics"); ok {
		warnUnknownFields("physics", raw, reflect.TypeOf(cfg.Physics))
		if err := json.Unmarshal(raw, &cfg.Physics); err != nil {
			return Config{}, fmt.Errorf("config: 解析 physics 字段: %w", err)
		}
	}
	if raw, ok := lookupCaseInsensitive(top, "sim"); ok {
		warnUnknownFields("sim", raw, reflect.TypeOf(cfg.Sim))
		if err := json.Unmarshal(raw, &cfg.Sim); err != nil {
			return Config{}, fmt.Errorf("config: 解析 sim 字段: %w", err)
		}
	}
	if raw, ok := lookupCaseInsensitive(top, "render"); ok {
		warnUnknownFields("render", raw, reflect.TypeOf(cfg.Render))
		if err := json.Unmarshal(raw, &cfg.Render); err != nil {
			return Config{}, fmt.Errorf("config: 解析 render 字段: %w", err)
		}
	}
	warnUnknownTopLevel(top)

	clampFields(&cfg)
	return cfg, nil
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

// warnUnknownFields 对 raw 对象中不属于 fieldType 的键逐个 slog.Warn，
// 便于用户发现拼写错误的配置项；这些键本身会在随后的正式反序列化中被忽略。
func warnUnknownFields(group string, raw json.RawMessage, fieldType reflect.Type) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return // 结构不是对象，交由后续正式反序列化报错。
	}
	known := make(map[string]bool, fieldType.NumField())
	for i := 0; i < fieldType.NumField(); i++ {
		known[strings.ToLower(fieldType.Field(i).Name)] = true
	}
	for key := range probe {
		if !known[strings.ToLower(key)] {
			slog.Warn("配置项未知字段已忽略", "field", group+"."+key)
		}
	}
}

// warnUnknownTopLevel 对不认识的顶层分组名 slog.Warn。
func warnUnknownTopLevel(top map[string]json.RawMessage) {
	known := map[string]bool{"version": true, "logging": true, "physics": true, "sim": true, "render": true}
	for key := range top {
		if !known[strings.ToLower(key)] {
			slog.Warn("配置项未知字段已忽略", "field", key)
		}
	}
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
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
// render 组不在这里处理：它是纯数据，由 cmd/mcgo 自行消费。
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

// clampFields 依次把 cfg 中每个 Fields() 覆盖的字段钳制进 [Min, Max]，
// 越界时 slog.Warn 并写回钳制后的值。
func clampFields(cfg *Config) {
	for _, field := range Fields() {
		group := groupValue(cfg, field.Group)
		target := group.FieldByName(exportedFieldName(field.Name))
		if !target.IsValid() {
			continue // 不应该发生；Fields() 与对应结构体字段应保持同步。
		}
		current := floatOf(target)
		clamped := current
		if clamped < field.Min {
			clamped = field.Min
		}
		if clamped > field.Max {
			clamped = field.Max
		}
		if clamped != current {
			setFloat(target, clamped)
			slog.Warn("配置项越界已钳制", "field", field.Group+"."+field.Name, "value", current, "clamped", clamped)
		}
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

// floatOf 把数值类型的反射值统一读成 float64，供区间比较使用。
func floatOf(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint())
	default:
		panic("config: 不支持的字段类型 " + v.Kind().String())
	}
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
