package companion

import (
	"errors"
	"fmt"
	"net"
	"net/url"
)

// TaskTimeoutDefaultMinutes 是 taskTimeoutMinutes 未设置（字段缺席、结构体零值）
// 时使用的缺省任务超时，单位分钟。
const TaskTimeoutDefaultMinutes = 10

// 模型任务超时的合法区间。下界 1 防止"立即超时"的退化配置；上界 60 让误填
// 单位（例如把 tick 当分钟）的配置在启动时暴露而不是运行期悬挂。区间在
// ValidateTaskTimeoutMinutes 一处定义，config 解析与静态校验共用同一权威。
const (
	taskTimeoutMinMinutes = 1
	taskTimeoutMaxMinutes = 60
)

// ModelSettings 是伙伴 AI 模型的运行时静态配置。
//
// 它同时服务于三条链路：配置文件 ai 组（json tag 与文件键逐字对应，字段随
// config.AI 的嵌入参与 Save/Load 往返）、server.Config 的启动校验，以及密钥
// 解析——APIKeyEnv 只保存环境变量名，密钥值永远不落盘，由入口进程在启动时
// 从环境变量读进内存（server.Config.AIAPIKey）。
//
// TaskTimeoutMinutes 为 0 表示未设置，由 TaskTimeout() 归一为缺省值；显式
// 写入 0 由配置解析层拒绝（"未设置"只对字段缺席成立）。
type ModelSettings struct {
	// Endpoint 是模型服务的基址，形态约束见 ValidateModelEndpoint。
	Endpoint string `json:"endpoint"`
	// Model 是请求时使用的模型名，非空。
	Model string `json:"model"`
	// APIKeyEnv 是存放密钥的环境变量名；https endpoint 必须配置，loopback
	// http（本地联调）可以省略。
	APIKeyEnv string `json:"apiKeyEnv"`
	// TaskTimeoutMinutes 是单次模型任务的超时分钟数，0 表示未设置。
	//
	// json 序列化带 omitempty：0（未设置）写出时省略该键，而不是落成
	// "taskTimeoutMinutes": 0——配置解析层会拒绝显式 0（显式写 0 几乎必然是
	// 填错了单位或漏了数字），若 Save 把未设置写成显式 0，调试面板的
	// Load→改 render→Save 流程会产出下一次启动无法加载的配置文件。
	TaskTimeoutMinutes int `json:"taskTimeoutMinutes,omitempty"`
}

// ValidateModelEndpoint 校验模型 endpoint 的形态：只接受无 userinfo、query 与
// fragment 的 https URL，或 host 为 loopback IP 字面量的 http URL。
//
// 三种附加成分一律拒绝，因为它们都是密钥外泄的常见通道（userinfo 内嵌凭据、
// query 常被日志与错误文本原样回显）。http 只放行 loopback IP 字面量（不含
// "localhost" 主机名：它可能被 /etc/hosts 改写指向非本机，IP 字面量没有这种
// 歧义），用于本地联调；放行非 loopback 明文地址会诱导密钥走未加密链路。
// 其余 scheme 与空串一律拒绝。
func ValidateModelEndpoint(endpoint string) error {
	if endpoint == "" {
		return errors.New("companion: endpoint 为空")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("companion: 解析 endpoint %q: %w", endpoint, err)
	}
	// 错误信息只回显 endpoint（本就是配置文件内容），绝不涉及密钥。
	if parsed.User != nil {
		return fmt.Errorf("companion: endpoint %q 不得携带 userinfo", endpoint)
	}
	if parsed.RawQuery != "" {
		return fmt.Errorf("companion: endpoint %q 不得携带 query", endpoint)
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("companion: endpoint %q 不得携带 fragment", endpoint)
	}
	hostname := parsed.Hostname()
	switch parsed.Scheme {
	case "https":
		if hostname == "" {
			return fmt.Errorf("companion: endpoint %q 缺少 host", endpoint)
		}
		return nil
	case "http":
		ip := net.ParseIP(hostname)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf(
				"companion: http endpoint 仅允许 loopback IP 字面量，got %q",
				endpoint,
			)
		}
		return nil
	default:
		return fmt.Errorf("companion: endpoint %q 必须是 https 或 loopback http", endpoint)
	}
}

// ValidateTaskTimeoutMinutes 校验显式出现的 taskTimeoutMinutes 值：必须落在
// 1..60。0 不在这里放行——"0 表示未设置"是结构体层的约定（字段缺席或零值），
// 配置文件里显式写 0 由解析层按错误拒绝，避免"想填 1 却漏写"被悄悄吞掉。
func ValidateTaskTimeoutMinutes(minutes int) error {
	if minutes < taskTimeoutMinMinutes || minutes > taskTimeoutMaxMinutes {
		return fmt.Errorf(
			"companion: taskTimeoutMinutes %d 超出合法区间 %d..%d",
			minutes, taskTimeoutMinMinutes, taskTimeoutMaxMinutes,
		)
	}
	return nil
}

// Validate 校验模型设置的静态完整性：endpoint 非空且形态合法、model 非空、
// https endpoint 必须配置 apiKeyEnv、超时为 0（未设置）或落在 1..60。
//
// 错误信息按字段定位（含 endpoint/model/apiKeyEnv/taskTimeoutMinutes 字样），
// 供上层把启动失败精确归因到配置字段；任何错误都只引用字段名与 endpoint，
// 绝不包含密钥值——本结构体本来就只持有环境变量名。
func (s ModelSettings) Validate() error {
	if err := ValidateModelEndpoint(s.Endpoint); err != nil {
		return fmt.Errorf("companion: 模型设置 endpoint: %w", err)
	}
	if s.Model == "" {
		return errors.New("companion: 模型设置缺少 model")
	}
	if parsed, err := url.Parse(s.Endpoint); err == nil && parsed.Scheme == "https" && s.APIKeyEnv == "" {
		return errors.New("companion: 模型设置使用 https endpoint 时必须配置 apiKeyEnv")
	}
	if s.TaskTimeoutMinutes != 0 {
		if err := ValidateTaskTimeoutMinutes(s.TaskTimeoutMinutes); err != nil {
			return fmt.Errorf("companion: 模型设置 taskTimeoutMinutes: %w", err)
		}
	}
	return nil
}

// TaskTimeout 返回生效的任务超时分钟数：未设置（0）归一为
// TaskTimeoutDefaultMinutes，已设置值原样返回（区间合法性由 Validate 保证，
// 这里不做重复校验）。
func (s ModelSettings) TaskTimeout() int {
	if s.TaskTimeoutMinutes == 0 {
		return TaskTimeoutDefaultMinutes
	}
	return s.TaskTimeoutMinutes
}
