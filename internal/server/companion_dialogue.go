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

// dialogueOutcome 是一次台词请求的结果，携带伙伴 ID、任务世代、节点身份、
// 发令者身份快照与台词文本供过期判定与广播组装：世代或任务状态不符即过时
// 丢弃（spec：companion-dialogue「并发受限且失败只跳过台词」）。发令者身份
// 在派发时冻结——结果应用时槽位的 currentIssuer 理论上仍属同一任务纪元
// （世代一致未被 BeginHead 提升），快照消除对槽位残留状态的任何依赖。
type dialogueOutcome struct {
	id         companion.ID
	generation uint64
	node       companion.DialogueNode
	issuer     companionTaskIssuer
	line       string
	summary    string
	err        error
}

// requestDialogue 是台词派发在 tick 边界的唯一入口（调用方必须持有 stepMu）。
// 守卫顺序：未知槽位/inactive 伙伴/任务预算耗尽跳过；每伙伴在途跳过（不取消
// 在途）；共享槽 try-acquire 失败跳过（不排队、不重试）。成功则置在途标记、
// 计入每任务预算并 spawn worker。terminal 标志由 node.Kind 派生（结构性事实，
// 不接受调用方另行声明——D5 评审 Minor-1 的冗余自由度在此收紧为零）。
//
// 触发时机契约（D6 接线，companion_manager.go 的 dispatchDialogueNode 与
// advanceFollowRunner）：任务进入 Running、被选中的计划步骤完成、持续跟随
// 首次到达跟随距离、任务进入四种终态之一。时序约束：终止节点的派发必须
// 发生在终态迁移的同一 tick、且在 FIFO 提升（BeginHead）之前——FIFO 在同一
// tick 的 dispatchPlanning 阶段才推进，本方法在迁移点同步调用即天然满足；
// 迟于 BeginHead 的派发会携带新世代，结果按过时丢弃（D5 评审 Minor-2）。
//
// m.cancel 的调用契约：只在关服序列（beginShutdown/close）被调用，tick 路径
// 绝不调用——dialogueInFlight 的清除只能来自 tick 边界的结果应用
// （applyDialogueOutcome）或进程退出，关服 cancel 令在途 worker 经 ctx.Done
// 放弃结果并释放共享槽（D5 评审 Minor-5 的显式化）。
func (m *companionManager) requestDialogue(id companion.ID, node companion.DialogueNode) {
	slot := m.slots[id]
	if slot == nil {
		// 与 enqueueCommand/stopCompanion 的防御一致：配置缺陷按跳过处理并
		// 保留可诊断日志，绝不伪装成派发成功。
		slog.Error("台词派发找不到伙伴槽位", "companion", id)
		return
	}
	if slot.dialogueRequests >= companion.MaxDialogueRequestsPerTask {
		// 每任务预算（本进程计数，不持久化——design.md 裁决）：结构上
		// 1+≤6+1 封顶，计数只防御未来接线缺陷，不参与正常路径。
		return
	}
	if slot.dialogueInFlight {
		// 每伙伴最多一个在途台词请求：新节点到来时仍有在途即跳过，不取消、
		// 不替换在途请求（spec：「在途请求存在时新节点被跳过」）。跳过即
		// 放弃该节点，绝不补发。
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
	// 人设来自配置解析的生效值（ResolvedPersona，D2）；摘要是 manager 持有的
	// 最近对话摘要（终态响应写入、重启经 restoreQueue 恢复），只进 Dialogue
	// 请求、绝不进入 Planner 输入。
	request, err := companion.NewDialogueRequest(
		slot.definition.ResolvedPersona, slot.summary, node, m.buildDialogueEnvDigest(body))
	if err != nil {
		// 防御路径：环境扫描与配置人设都已在各自边界校验，这里失败只可能是
		// 服务端缺陷。归还刚占用的槽位并跳过该台词，绝不影响任务平面。
		<-m.semaphore
		slog.Error("构造台词请求失败", "companion", id, "error", err)
		return
	}
	slot.dialogueInFlight = true
	slot.dialogueRequests++
	m.waitGroup.Add(1)
	go m.dialogueWorker(id, slot.queue.Generation(), node, slot.currentIssuer, request)
}

// dialogueWorker 在 worker goroutine 上调用模型：只读不可变请求值，结果经
// 有界 channel 回 tick 边界。ctx 取消（仅关服序列调用 m.cancel）时放弃结果；
// 共享槽在模型调用返回后、结果发送前释放（时序论证见函数内注释）——HTTP
// 调用直接使用 m.ctx，beginShutdown 的 cancel 同时取消在途模型请求；tick
// 路径没有任何取消路径（见 requestDialogue 的契约注释）。
func (m *companionManager) dialogueWorker(
	id companion.ID,
	generation uint64,
	node companion.DialogueNode,
	issuer companionTaskIssuer,
	request companion.DialogueRequest,
) {
	defer m.waitGroup.Done()
	terminal := node.Kind == companion.DialogueNodeTerminal
	line, summary, err := m.dialogue.Do(m.ctx, request, terminal)
	// 释放先于发送：`m.semaphore` 约束的是在途模型调用数，`m.dialogue.Do` 返回
	// 即调用结束、名额自此可复用，结果投递只是队列簿记。若先发送再经 defer
	// 释放，两者之间没有屏障，tick 线程 try-acquire 的成败便依赖 goroutine
	// 调度（ns 级残余窗口，M5E 递延 8 记录的成因）；前移后「任何观察者看到
	// 结果之前名额已归还」成为严格事实，ctx 取消路径行为不变（取消时同样
	// 先释放、发送走 `<-m.ctx.Done()` 分支放弃结果）。
	<-m.semaphore
	outcome := dialogueOutcome{
		id: id, generation: generation, node: node, issuer: issuer,
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
// 或队首已提升）直接丢弃；开始/进展/首次到达节点的任务必须仍在 Running
// （任务已终态即过时，防止「我出发了」出现在任务结束之后）。终态节点在任务
// 离开当前槽位时触发，世代一致即同一任务纪元，无需再断言当前槽位状态（清槽
// 是终态的既有序列）。失败结果只记 debug 级结构化日志并跳过该台词。
func (m *companionManager) applyDialogueOutcome(outcome dialogueOutcome) {
	slot := m.slots[outcome.id]
	if slot == nil || !slot.dialogueInFlight {
		return
	}
	slot.dialogueInFlight = false
	if slot.queue.Generation() != outcome.generation {
		if outcome.err != nil {
			// 「过时 + 失败」组合的显式记录（D5 评审 Minor-3）：失败结果本身
			// 只跳过台词，叠加过时判定后连日志都不留会让「为什么既没有台词
			// 也没有错误」不可归因。仍然只记 debug 级，不改变任何行为。
			slog.Debug("过时的失败台词结果一并丢弃",
				"companion", outcome.id, "node", uint8(outcome.node.Kind), "error", outcome.err)
		}
		return
	}
	switch outcome.node.Kind {
	case companion.DialogueNodeStart, companion.DialogueNodeProgress, companion.DialogueNodeFirstArrival:
		current, ok := slot.queue.Current()
		if !ok || current.State != companion.TaskRunning {
			return
		}
	case companion.DialogueNodeTerminal:
	default:
		return
	}
	if outcome.err != nil {
		// 失败只跳过台词：错误来自客户端的三类哨兵（传输层/请求构造/输出
		// 解码，F-3 拆分后请求与输出各有独立哨兵），客户端已保证错误文本
		// 不含密钥与响应正文原文。
		slog.Debug("台词请求失败，跳过该台词",
			"companion", outcome.id, "node", uint8(outcome.node.Kind), "error", outcome.err)
		return
	}
	m.applyDialogueEffect(outcome.id, outcome.node, outcome.issuer, outcome.line, outcome.summary)
}

// applyDialogueEffect 把一条有效台词结果落到可观察行为（D6 真身）：
//   - 构造 CompanionSpeech 广播事实（kind ChatEventCompanionSpeech、伙伴身份
//     与台词、reason None），经本 tick 的 takeEventFacts → taskEventDeliveries
//     管道广播给全部在线玩家，EventID 沿全服聊天计数器严格递增；
//   - 终态节点的 summary 写入 manager 持有的每伙伴摘要状态
//     （slot.summary），随下一次 Observe 标记 AI 存档 dirty 落盘。
//
// 表达平面纪律：本方法绝不改变任务状态、FIFO、路径或任何世界事实；摘要只
// 作为后续 Dialogue 请求输入，绝不进入 Planner（dialogueEffects 计数是 D5
// 遗留的测试观察哨兵，保留供既有测试断言）。
func (m *companionManager) applyDialogueEffect(
	id companion.ID,
	node companion.DialogueNode,
	issuer companionTaskIssuer,
	line, summary string,
) {
	m.dialogueEffects++
	slot := m.slots[id]
	if slot == nil {
		// 结果在途期间槽位被移除只可能来自配置热变更——当前版本配置只在
		// 启动时加载，这里只是防御。
		return
	}
	m.events = append(m.events, taskEventFact{
		issuer:     issuer,
		definition: slot.definition,
		speech:     line,
	})
	if node.Kind == companion.DialogueNodeTerminal {
		// 摘要只在终态更新（spec：「最近对话摘要 SHALL 只由终态 Dialogue
		// 响应的 summary 字段更新」）；空串等价于清空记忆，同样落状态。
		slot.summary = summary
	}
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
