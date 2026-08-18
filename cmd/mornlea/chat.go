//go:build darwin

package main

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render/hud"
)

// chatInput 的字节上限直接复用 companion.MaxPlanCommandBytes（E7 同源化）：
// 客户端能输入的每条指令必须总能通过 network 校验并进入权威 Planner 快照，
// 三个边界由同一常量保证不漂移，取代原 `const maxChatCommandBytes = 1024`
// 裸字面量；行为级锁测试（chat_test.go 的 BoundaryLocks 测试）在漂移时变红。
type chatInput struct {
	open     bool
	runes    [companion.MaxPlanCommandBytes]rune
	count    int
	bytes    int
	overflow bool
	text     string
}

func (input *chatInput) Open() {
	*input = chatInput{open: true}
}

func (input *chatInput) Cancel() {
	*input = chatInput{}
}

func (input *chatInput) Append(char rune) {
	if !input.open {
		return
	}
	size := utf8.RuneLen(char)
	if size < 0 || unicode.IsControl(char) || input.count == len(input.runes) || input.bytes+size > companion.MaxPlanCommandBytes {
		input.overflow = true
		return
	}
	input.runes[input.count] = char
	input.count++
	input.bytes += size
	input.text = string(input.runes[:input.count])
}

func (input *chatInput) Backspace() {
	if !input.open || input.count == 0 {
		return
	}
	input.count--
	input.bytes -= utf8.RuneLen(input.runes[input.count])
	input.runes[input.count] = 0
	input.text = string(input.runes[:input.count])
}

func (input *chatInput) Submit() (network.ChatCommand, bool) {
	if !input.open || input.overflow {
		return network.ChatCommand{}, false
	}
	command := network.ChatCommand{Text: strings.TrimSpace(input.text)}
	if err := command.Validate(); err != nil {
		return network.ChatCommand{}, false
	}
	input.Cancel()
	return command, true
}

func (a *application) chatOverlay() hud.ChatOverlay {
	a.refreshChatLines()
	return hud.ChatOverlay{
		Open:  a.chatInput.open,
		Input: a.chatInput.text,
		Lines: a.chatLines[:a.chatLineCount],
	}
}

func (a *application) refreshChatLines() {
	if a.chatEvents == nil {
		a.clearFormattedChatLines()
		return
	}
	events := a.chatEvents.Events(a.chatEventBuffer[:0])
	if len(events) == 0 {
		a.clearFormattedChatLines()
		return
	}
	latest := events[len(events)-1].EventID
	if latest == a.formattedChatEventID {
		return
	}
	start := max(0, len(events)-len(a.chatLines))
	for index := range a.chatLines {
		a.chatLines[index] = ""
	}
	a.chatLineCount = len(events) - start
	for index, event := range events[start:] {
		a.chatLines[index] = truncateChatLine(formatChatEvent(event))
	}
	a.formattedChatEventID = latest
}

func (a *application) clearFormattedChatLines() {
	if a.chatLineCount == 0 && a.formattedChatEventID == 0 {
		return
	}
	a.chatLines = [6]string{}
	a.chatLineCount = 0
	a.formattedChatEventID = 0
}

// formatChatEvent 将服务器确认的聊天事实格式化为稳定中文事实行。
// 任务生命周期事件（Task*）只复述伙伴名、固定中文模板与玩家原始指令摘要，
// 不显示任何模型生成的自由文本；每行最终经 truncateChatLine 截断到 32 rune。
// 唯一例外是 v19 的 CompanionSpeech：它是客户端唯一显示模型文本的位置，
// 仅以「伙伴名：台词原文」一行呈现，台词原样上屏，不改写、不清洗、不加引号。
func formatChatEvent(event network.ChatEvent) string {
	switch event.Kind {
	case network.ChatEventCompanionSpeech:
		// wire 校验已保证台词是 1..256 bytes 的有效 UTF-8 且不含控制字符，
		// 因此这里不做任何二次清洗；行宽沿用与事实行相同的 truncateChatLine
		// 截断（伙伴名前缀计入 32 rune 上限），台词行与任务事实行各占一行。
		return event.CompanionName + "：" + event.Speech
	case network.ChatEventTaskStarted:
		return event.CompanionName + " 开始执行：" + event.Command
	case network.ChatEventTaskProgress:
		return event.CompanionName + " 正在执行：" + event.Command
	case network.ChatEventTaskCompleted:
		return event.CompanionName + " 已完成：" + event.Command
	case network.ChatEventTaskTimedOut:
		return event.CompanionName + " 任务超时：" + event.Command
	case network.ChatEventTaskStopped:
		// v18 的停止终态是玩家的成功意图：与失败/超时刻意分离，行文对齐
		// 已完成/任务超时的事实短语模板（替换 C1 期间的 Accepted 兜底格式）。
		return event.CompanionName + " 已停止：" + event.Command
	case network.ChatEventTaskFailed:
		return event.CompanionName + " 任务失败（" + taskFailReasonText(event.RejectReason) + "）：" + event.Command
	}
	switch event.RejectReason {
	case network.ChatRejectInvalidFormat:
		return "系统：格式应为 @伙伴名 指令"
	case network.ChatRejectUnknownCompanion:
		return "系统：未找到伙伴 " + event.CompanionName
	case network.ChatRejectQueueFull:
		return "系统：" + event.CompanionName + " 任务队列已满：" + event.Command
	case network.ChatRejectNotFollowing:
		// v18 的停止旁路同步拒绝：目标伙伴当前没有可停止的持续任务，
		// 携带完整身份与被拒指令供发令者核对。
		return "系统：" + event.CompanionName + " 没有可停止的持续任务：" + event.Command
	default:
		return event.PlayerName + " → " + event.CompanionName + "：" + event.Command
	}
}

// taskFailReasonText 把 TaskFailed 携带的固定失败原因枚举映射为稳定中文短语。
// 枚举值只会在 network.Validate 通过的组合中出现；未知值仍给出占位事实而非模型文本。
func taskFailReasonText(reason network.ChatRejectReason) string {
	switch network.TaskFailReason(reason) {
	case network.TaskFailPlannerUnavailable:
		return "规划器不可用"
	case network.TaskFailInvalidPlan:
		return "计划无效"
	case network.TaskFailPathUnreachable:
		return "路径不可达"
	case network.TaskFailWorldChanged:
		return "世界已变化"
	case network.TaskFailInventoryFull:
		return "背包已满"
	default:
		return "未知原因"
	}
}

func truncateChatLine(text string) string {
	runes := []rune(text)
	if len(runes) <= 32 {
		return text
	}
	runes[31] = '…'
	return string(runes[:32])
}
