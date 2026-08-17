// 本文件实现 server 侧 Dialogue worker：台词请求在权威 tick 边界派发、在
// worker goroutine 上执行、结果只在 tick 边界应用。与 Planner worker 同一
// 扇入模式，但并发策略刻意不同——台词是尽力而为的表达平面输出：
//   - 共享模型槽：复用既有 m.semaphore（cap=MaxActive=4，与 Planner 共用）；
//     Planner 在 tick 边界 try-acquire 失败则下一 tick 重试（既有语义，本文件
//     不改动），Dialogue try-acquire 失败立即跳过该节点——不排队、不重试，
//     迟到台词在错误语境出现比少一句台词更糟（design.md 否决「排队等槽」）；
//   - 每伙伴最多一个在途请求：slot.dialogueInFlight 在 tick 边界置位/清除，
//     在途期间新节点直接跳过，绝不取消或替换在途请求；
//   - 失败只跳过台词：任何传输/解码失败都只记 debug 级结构化日志，绝不改变
//     任务状态、FIFO 或任何世界事实。
package server

import (
	"context"
	"log/slog"

	"github.com/channing771/mornlea/internal/companion"
)

// companionDialogue 是台词模型依赖面：生产实现是 companion.DialogueClient，
// 测试可注入假模型端点构造的真客户端（replaceDialogueForTest）。
type companionDialogue interface {
	Do(ctx context.Context, req companion.DialogueRequest, terminal bool) (line, summary string, err error)
}

// dialogueOutcome 是一次台词请求的结果，携带伙伴 ID、任务世代、节点身份与
// terminal 标志供过期判定：世代或任务状态不符即过时丢弃（spec：
// companion-dialogue「并发受限且失败只跳过台词」）。
type dialogueOutcome struct {
	id         companion.ID
	generation uint64
	node       companion.DialogueNode
	terminal   bool
	line       string
	summary    string
	err        error
}

// requestDialogue 是台词派发在 tick 边界的唯一入口（调用方必须持有 stepMu）。
// 守卫顺序：未知槽位/inactive 伙伴跳过；每伙伴在途跳过（不取消在途）；共享
// 槽 try-acquire 失败跳过（不排队、不重试）。成功则置在途标记并 spawn worker。
//
// 真实触发时机（哪些节点何时调用本方法——进入 Running、选中步骤完成、四种
// 终态）由 D6 在 companion_manager.go 的节点评估接线；D5 只交付机制本身。
func (m *companionManager) requestDialogue(id companion.ID, node companion.DialogueNode, terminal bool) {
	slot := m.slots[id]
	if slot == nil {
		// 与 enqueueCommand/stopCompanion 的防御一致：配置缺陷按跳过处理并
		// 保留可诊断日志，绝不伪装成派发成功。
		slog.Error("台词派发找不到伙伴槽位", "companion", id)
		return
	}
	if slot.dialogueInFlight {
		// 每伙伴最多一个在途台词请求：新节点到来时仍有在途即跳过，不取消、
		// 不替换在途请求（spec：「在途请求存在时新节点被跳过」）。
		return
	}
	body, active := m.body(id)
	if !active {
		// 伙伴未激活（出生扫描在途）：跳过该节点，等下一个触发节点。
		return
	}
	select {
	case m.semaphore <- struct{}{}:
	default:
		// 全服四个共享模型槽已满：立即跳过该节点。与 Planner 的差异是刻意
		// 的——任务规划必须最终发生（下一 tick 重试），台词错过即错过。
		return
	}
	// 人设来自配置解析的生效值（ResolvedPersona，D2）；摘要 D5 尚无持有方
	//（空串 = 无近期记忆），D6 接线摘要持有后在此喂入持久化的最近对话摘要。
	request, err := companion.NewDialogueRequest(
		slot.definition.ResolvedPersona, "", node, m.buildDialogueEnvDigest(body))
	if err != nil {
		// 防御路径：环境扫描与配置人设都已在各自边界校验，这里失败只可能是
		// 服务端缺陷。归还刚占用的槽位并跳过该台词，绝不影响任务平面。
		<-m.semaphore
		slog.Error("构造台词请求失败", "companion", id, "error", err)
		return
	}
	slot.dialogueInFlight = true
	m.waitGroup.Add(1)
	go m.dialogueWorker(id, slot.queue.Generation(), node, terminal, request)
}

// dialogueWorker 在 worker goroutine 上调用模型：只读不可变请求值，结果经
// 有界 channel 回 tick 边界。ctx 取消（关服）时放弃结果并释放共享槽——
// HTTP 调用直接使用 m.ctx，beginShutdown 的 cancel 同时取消在途模型请求。
func (m *companionManager) dialogueWorker(
	id companion.ID,
	generation uint64,
	node companion.DialogueNode,
	terminal bool,
	request companion.DialogueRequest,
) {
	defer m.waitGroup.Done()
	defer func() { <-m.semaphore }()
	line, summary, err := m.dialogue.Do(m.ctx, request, terminal)
	outcome := dialogueOutcome{
		id: id, generation: generation, node: node, terminal: terminal,
		line: line, summary: summary, err: err,
	}
	select {
	case m.dialogueResults <- outcome:
	case <-m.ctx.Done():
	}
}

// applyDialogueOutcomes 在 tick 边界非阻塞排空台词结果并应用（对齐
// applyPlannerOutcomes 模式）：世代或任务状态不符的结果直接丢弃。
func (m *companionManager) applyDialogueOutcomes() {
	for {
		select {
		case outcome := <-m.dialogueResults:
			m.applyDialogueOutcome(outcome)
		default:
			return
		}
	}
}

// applyDialogueOutcome 应用单条台词结果：先清在途标记（无论结果新旧，该次
// 请求的槽位生命周期已结束），再做两级过时判定——世代不匹配（任务已被替换
// 或队首已提升）直接丢弃；开始/进展节点的任务必须仍在 Running（任务已终态
// 即过时，防止「我出发了」出现在任务结束之后）。终态节点在任务离开当前槽
// 位时触发，世代一致即同一任务纪元，无需再断言当前槽位状态（清槽是终态的
// 既有序列）。失败结果只记 debug 级结构化日志并跳过该台词。
func (m *companionManager) applyDialogueOutcome(outcome dialogueOutcome) {
	slot := m.slots[outcome.id]
	if slot == nil || !slot.dialogueInFlight {
		return
	}
	slot.dialogueInFlight = false
	if slot.queue.Generation() != outcome.generation {
		return
	}
	switch outcome.node.Kind {
	case companion.DialogueNodeStart, companion.DialogueNodeProgress:
		current, ok := slot.queue.Current()
		if !ok || current.State != companion.TaskRunning {
			return
		}
	case companion.DialogueNodeTerminal:
	default:
		return
	}
	if outcome.err != nil {
		// 失败只跳过台词：错误来自客户端的两类哨兵（传输层/输出非法），
		// 客户端已保证错误文本不含密钥与响应正文原文。
		slog.Debug("台词请求失败，跳过该台词",
			"companion", outcome.id, "node", uint8(outcome.node.Kind), "error", outcome.err)
		return
	}
	m.applyDialogueEffect(outcome.id, outcome.node, outcome.terminal, outcome.line, outcome.summary)
}

// applyDialogueEffect 把一条有效台词结果落到可观察行为。D5 版本只记 debug
// 级结构化日志（失败原因类）与测试观察哨兵（dialogueEffects 计数），不做任何
// 广播或持久化；D6 将把本方法替换为 CompanionSpeech ChatEvent 广播（全部在
// 线玩家）与最近对话摘要更新（仅 terminal 且标记存档 dirty）——接线点保持
// 方法签名不变，广播与持久化语义由 D6 的规格与测试锁定。
func (m *companionManager) applyDialogueEffect(
	id companion.ID,
	node companion.DialogueNode,
	terminal bool,
	line, summary string,
) {
	m.dialogueEffects++
	slog.Debug("伙伴台词生效（D5 仅记录，广播与摘要属 D6 接线）",
		"companion", id, "node", uint8(node.Kind), "terminal", terminal,
		"line_bytes", len(line), "summary_bytes", len(summary))
}

// buildDialogueEnvDigest 在 tick 边界构造一次台词请求的环境摘要：复用规划
// 观察的同一有界扫描（scanEnvObservation）与 BoundExposedBlocks 归一，保证
// Dialogue 与 Planner 看到的环境半部同构（DialogueEnvDigest 契约见
// dialogue_types.go）。返回切片是本次构造的独立副本，worker 在途期间只读。
func (m *companionManager) buildDialogueEnvDigest(body companion.Body) companion.DialogueEnvDigest {
	_, exposed, heights := m.scanEnvObservation(body)
	return companion.DialogueEnvDigest{
		ExposedBlocks: companion.BoundExposedBlocks(exposed),
		Heights:       heights,
	}
}
