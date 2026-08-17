// 本文件定义 Dialogue 的输入请求值类型与响应严格解码。与 planner.go 同一
// 纪律：persona、最近对话摘要、环境摘要与模型输出全部视为不可信数据，权限
// 边界只有本文件的白名单校验；解码器绝不执行模型返回的代码、URL、工具名或
// 任意函数调用，错误上下文绝不包含密钥或响应正文原文。全部为纯类型与纯
// 函数，无 I/O、无 goroutine——网络半部（HTTP 客户端与 worker）属后续任务。
package companion

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/channing771/mornlea/internal/core"
)

// Dialogue 输入与输出的有界常量。全部上限都在构造/解码边界一次性强制，保证
// 请求的内存占用与响应的处理成本不随模型行为无界增长。
const (
	// MaxDialogueLineBytes 是单句台词的字节上限，与 network 聊天行及
	// CompanionSpeech 事件的台词上界同源（spec：≤256 bytes）。
	MaxDialogueLineBytes = 256
	// MaxDialogueSummaryBytes 是最近对话摘要的字节上限（spec：≤2,048 bytes）。
	// 请求输入与终态响应共用同一上界，两个方向由同一校验函数裁决，不可能漂移。
	MaxDialogueSummaryBytes = 2048
	// MaxDialogueResponseBytes 是台词响应正文的防御性分配前上限：上游 worker
	// 已用 LimitReader 限长（对齐 Planner 纪律），这里在解码入口再做一次长度
	// 检查，保证即使未来出现绕过限长的直调路径，解码器也不会为超大缓冲做
	// 完整 JSON 解析。
	MaxDialogueResponseBytes = 64 << 10
)

// ErrDialogueInvalidResponse 表示台词响应不符合受限 schema（非法 JSON、未知
// 字段、尾随数据、超限、缺字段或文本违例）或请求构造输入越界。上层把它映射
// 为「跳过该台词」——台词是尽力而为的表达平面输出，任何失败都不改变任务
// 状态、FIFO 或任何世界事实。
var ErrDialogueInvalidResponse = errors.New("companion: dialogue 输出或输入非法")

// dialogueResponseWire 是台词响应正文的解码中间形。两个字段都用 *string 以
// 区分「字段缺席」与「空串」：终态必须有 summary（缺席即非法）、非终态不得
// 出现 summary（出现即非法）、line 在两种形态下都必须出现，三条规则都依赖
// 缺席语义，值校验（长度、编码、控制字符）在其后单独裁决。
type dialogueResponseWire struct {
	Line    *string `json:"line"`
	Summary *string `json:"summary"`
}

// DecodeDialogueResponse 把台词响应正文严格解码为 (line, summary)。
//
// 严格性契约（spec：companion-dialogue「台词与摘要响应严格解码」）：
//   - 正文先做 MaxDialogueResponseBytes 长度检查，超限直接拒绝；
//   - json.Decoder + DisallowUnknownFields 解码为单一 JSON object，未知字段
//     拒绝；object 之后的任何尾随数据（合法第二个 JSON 值或非 JSON 垃圾）
//     拒绝——More() 检出合法后续值，第二次 Decode 必须 io.EOF 兜住垃圾后缀；
//   - line 必须出现且是 1..MaxDialogueLineBytes 字节的有效 UTF-8，不含 NUL
//     或任何 Unicode control，首尾不得有空白；
//   - terminal=true 时 summary 必须出现且不超过 MaxDialogueSummaryBytes
//     字节、有效 UTF-8、无 NUL（允许空串，等价于清空记忆）；terminal=false
//     时 summary 字段一旦出现即非法。
//
// 解码成功的文本经 strings.Clone 复制为服务端拥有的值：不保留对响应缓冲或
// 解码中间态的任何引用，调用方可以安全复用或释放 body。任何失败都包装
// ErrDialogueInvalidResponse，错误文本只含违例类别，不含正文原文。
func DecodeDialogueResponse(body []byte, terminal bool) (line string, summary string, err error) {
	if len(body) > MaxDialogueResponseBytes {
		return "", "", fmt.Errorf("companion: dialogue 响应 %d 字节超过上限 %d: %w",
			len(body), MaxDialogueResponseBytes, ErrDialogueInvalidResponse)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var wire dialogueResponseWire
	if err := decoder.Decode(&wire); err != nil {
		return "", "", fmt.Errorf("companion: dialogue 响应不是合法 JSON object: %w: %w",
			ErrDialogueInvalidResponse, err)
	}
	// 尾随数据双重检查：More() 对合法后续 JSON 值返回 true；非 JSON 垃圾后缀
	// （例如 `}xyz`）会让 More() 的 peek 失败返回 false，因此再用一次 Decode
	// 必须命中 io.EOF 兜底，两条路径都拒绝。
	if decoder.More() {
		return "", "", fmt.Errorf("companion: dialogue 响应 JSON 之后存在尾随数据: %w",
			ErrDialogueInvalidResponse)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", "", fmt.Errorf("companion: dialogue 响应 JSON 之后存在尾随数据: %w",
			ErrDialogueInvalidResponse)
	}

	if wire.Line == nil {
		return "", "", fmt.Errorf("companion: dialogue 响应缺少 line 字段: %w", ErrDialogueInvalidResponse)
	}
	if err := validateDialogueLine(*wire.Line); err != nil {
		return "", "", fmt.Errorf("companion: dialogue %w: %w", ErrDialogueInvalidResponse, err)
	}
	if terminal {
		if wire.Summary == nil {
			return "", "", fmt.Errorf("companion: 终态 dialogue 响应缺少 summary 字段: %w",
				ErrDialogueInvalidResponse)
		}
		if err := validateDialogueSummary(*wire.Summary); err != nil {
			return "", "", fmt.Errorf("companion: dialogue %w: %w", ErrDialogueInvalidResponse, err)
		}
	} else if wire.Summary != nil {
		return "", "", fmt.Errorf("companion: 非终态 dialogue 响应不得携带 summary 字段: %w",
			ErrDialogueInvalidResponse)
	}
	// strings.Clone 显式复制：虽然 JSON 解码到 string 字段已是新分配，这里把
	// 「服务端拥有值」写成显式契约，防止未来重构（例如复用解码缓冲）无声
	// 破坏不可变假设。
	line = strings.Clone(*wire.Line)
	if terminal {
		return line, strings.Clone(*wire.Summary), nil
	}
	return line, "", nil
}

// validateDialogueLine 校验单句台词：1..MaxDialogueLineBytes 字节、有效
// UTF-8、不含 NUL 或任何 Unicode control、首尾无空白。长度按字节计（与
// CompanionSpeech wire 上界同源）；「无首尾空白」覆盖 Unicode 空白全集
// （含全角空格），防止台词在聊天 HUD 里呈现为缩进行。校验只拒绝、绝不截断
// 或清洗——超长台词直接跳过，不存在「截断后的台词」这种中间产物。
func validateDialogueLine(line string) error {
	if len(line) == 0 {
		return errors.New("台词为空")
	}
	if !utf8.ValidString(line) {
		return errors.New("台词不是有效 UTF-8")
	}
	if len(line) > MaxDialogueLineBytes {
		return fmt.Errorf("台词 %d 字节超过上限 %d", len(line), MaxDialogueLineBytes)
	}
	for _, r := range line {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("台词含 NUL 或控制字符")
		}
	}
	if strings.TrimSpace(line) != line {
		return errors.New("台词首尾含空白")
	}
	return nil
}

// validateDialogueSummary 校验最近对话摘要：不超过 MaxDialogueSummaryBytes
// 字节、有效 UTF-8、无 NUL。摘要只进入后续 Dialogue 请求的输入，不上屏，
// 因此与 line 相比不设非空、控制字符与首尾空白约束（spec 只要求长度、编码
// 与无 NUL）；空串合法，等价于清空记忆。该函数同时服务请求构造侧（摘要作
// 为输入的上界）与响应解码侧（终态摘要的上界），保证两个方向的边界一致。
func validateDialogueSummary(summary string) error {
	if !utf8.ValidString(summary) {
		return errors.New("摘要不是有效 UTF-8")
	}
	if strings.ContainsRune(summary, 0) {
		return errors.New("摘要包含 NUL")
	}
	if len(summary) > MaxDialogueSummaryBytes {
		return fmt.Errorf("摘要 %d 字节超过上限 %d", len(summary), MaxDialogueSummaryBytes)
	}
	return nil
}

// DialogueEnvDigest 是一次台词请求可携带的极小附近环境摘要，与观察快照的
// 环境半部同构：直接复用 PlanBlock/PlanHeight 值类型与 PlanSnapshot 的同组
// 数量上界（MaxPlanExposedBlocks/MaxPlanHeightSamples），由 server 侧复用
// 环境摘要的既有有界构造器产出（归一规则同 BoundExposedBlocks），因此这里
// 只做防御性校验、不重复提供归一入口。环境摘要绝不包含 API key、其他玩家
// 聊天文本或世界存档路径。
type DialogueEnvDigest struct {
	// ExposedBlocks 是伙伴周围按 (X,Y,Z) 严格升序的暴露/特殊方块，至多
	// MaxPlanExposedBlocks 条。
	ExposedBlocks []PlanBlock
	// Heights 是按 (X,Z) 严格升序的地表高度样本，至多 MaxPlanHeightSamples 条。
	Heights []PlanHeight
}

// Validate 校验环境摘要的不变量：两类列表的数量上界、方块编号与 Y 边界、
// 高度取值边界与两组严格升序去重。规则与 PlanSnapshot.Validate 的对应分支
// 逐字一致（同构输入必须同构校验），防止两套边界漂移。
func (d DialogueEnvDigest) Validate() error {
	if len(d.ExposedBlocks) > MaxPlanExposedBlocks {
		return fmt.Errorf("companion: dialogue 环境方块数 %d 超过上限 %d",
			len(d.ExposedBlocks), MaxPlanExposedBlocks)
	}
	for index, block := range d.ExposedBlocks {
		if block.Block == core.AirID || !core.RegisteredBlock(block.Block) {
			return fmt.Errorf("companion: dialogue 环境方块[%d] 编号 %d 非法（空气或未注册）",
				index, block.Block)
		}
		if !validPlanBlockY(block.Pos.Y) {
			return fmt.Errorf("companion: dialogue 环境方块[%d] Y=%d 越界", index, block.Pos.Y)
		}
		if index > 0 && !planBlockAfter(block.Pos, d.ExposedBlocks[index-1].Pos) {
			return fmt.Errorf("companion: dialogue 环境方块[%d] 未按 (X,Y,Z) 严格升序", index)
		}
	}
	if len(d.Heights) > MaxPlanHeightSamples {
		return fmt.Errorf("companion: dialogue 高度样本数 %d 超过上限 %d",
			len(d.Heights), MaxPlanHeightSamples)
	}
	for index, height := range d.Heights {
		// core.MinY-1 是空列哨兵，其余取值必须是真实方块 Y（同快照规则）。
		if height.Height != core.MinY-1 && !validPlanBlockY(height.Height) {
			return fmt.Errorf("companion: dialogue 高度样本[%d] Height=%d 越界", index, height.Height)
		}
		if index > 0 {
			previous := d.Heights[index-1]
			if (previous.X > height.X) || (previous.X == height.X && previous.Z >= height.Z) {
				return fmt.Errorf("companion: dialogue 高度样本[%d] 未按 (X,Z) 严格升序", index)
			}
		}
	}
	return nil
}

// DialogueRequest 是一次台词请求的不可变输入值：恰好四类有界数据——人设、
// 最近对话摘要、当前事实节点与极小环境摘要（字段集合由测试反射冻结，防止
// 未来无声追加 key、聊天文本或存档路径等泄漏面）。请求由 worker goroutine
// 在构造后视为只读；正常路径的上游（persona 装载、摘要持久层、manager 节点
// 评估）已保证边界，NewDialogueRequest 的校验是防御性的第二层。
type DialogueRequest struct {
	// Persona 是伙伴人设自由文本，≤MaxPersonaBytes 字节、可为空（空人设的
	// 伙伴照常触发台词，只是没有风格约束）。
	Persona string
	// Summary 是最近对话摘要，≤MaxDialogueSummaryBytes 字节、可为空（尚无
	// 近期记忆）。摘要绝不进入 Planner 输入。
	Summary string
	// Node 是当前台词触发节点的事实身份（类型与载荷见 dialogue_nodes.go）。
	Node DialogueNode
	// Env 是极小附近环境摘要。
	Env DialogueEnvDigest
}

// NewDialogueRequest 构造并校验一份台词请求：persona 复用 ValidatePersona、
// summary 与 env 各自校验、node 校验稳定事实组合。任何越界输入返回包装
// ErrDialogueInvalidResponse 的错误，错误文本只描述违例类别与长度，不回显
// 文本内容——人设与摘要是不可信数据，不能随错误进入日志。
func NewDialogueRequest(persona, summary string, node DialogueNode, env DialogueEnvDigest) (DialogueRequest, error) {
	if err := ValidatePersona(persona); err != nil {
		return DialogueRequest{}, fmt.Errorf("companion: dialogue %w: %w", ErrDialogueInvalidResponse, err)
	}
	if err := validateDialogueSummary(summary); err != nil {
		return DialogueRequest{}, fmt.Errorf("companion: dialogue %w: %w", ErrDialogueInvalidResponse, err)
	}
	if err := node.Validate(); err != nil {
		return DialogueRequest{}, fmt.Errorf("companion: dialogue %w: %w", ErrDialogueInvalidResponse, err)
	}
	if err := env.Validate(); err != nil {
		return DialogueRequest{}, fmt.Errorf("companion: dialogue %w: %w", ErrDialogueInvalidResponse, err)
	}
	return DialogueRequest{Persona: persona, Summary: summary, Node: node, Env: env}, nil
}

// Validate 重新校验请求的全部不变量，供值经传输或拷贝后的防御性复核。
func (r DialogueRequest) Validate() error {
	_, err := NewDialogueRequest(r.Persona, r.Summary, r.Node, r.Env)
	return err
}
