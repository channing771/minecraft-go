// Companion 任务持久化的 server 侧接线测试：保存载荷归一（Planning/
// Validating 关服按 Queued + 原始指令、Running 精确保留计划/步骤索引/
// deadline、深拷不与调用方别名）、跨 Host 重启的当前任务与 FIFO 精确恢复、
// 恢复后的重新规划与 Running 恢复重验（路径不落盘，首个动作前按当前世界
// 重算，绝不盲走旧路径）。全部使用可控存档或 httptest 假模型，绝不访问
// 真实模型服务。
package server

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

func TestCompanionPersistenceSavePayloadNormalizesPlanningAndValidating(t *testing.T) {
	store := newControllableCompanionStore()
	p := newCompanionPersistence(store, storage.StoredCompanions{}, companionPersistenceTestConfig())
	t.Cleanup(p.Close)

	running := companion.TaskQueueState{
		ID:         companionBody(1, 10).ID,
		HasCurrent: true,
		Current: companion.Task{
			Command: "跑起来的任务",
			Plan: companion.Plan{Steps: []companion.PlanStep{
				{Kind: companion.PlanStepGoTo, X: 4, Y: 64, Z: 0},
				{Kind: companion.PlanStepGoTo, X: 8, Y: 64, Z: 0},
			}},
			StepIndex:     1,
			State:         companion.TaskRunning,
			StartTick:     12,
			DeadlineTicks: 1212,
		},
		Pending: []companion.TaskCommand{"跑步时排队"},
	}
	planning := companion.TaskQueueState{
		ID:         companionBody(2, 20).ID,
		HasCurrent: true,
		Current:    companion.Task{Command: "规划中的任务", State: companion.TaskPlanning},
	}
	validating := companion.TaskQueueState{
		ID:         companionBody(3, 30).ID,
		HasCurrent: true,
		Current:    companion.Task{Command: "校验中的任务", State: companion.TaskValidating},
	}
	states := []companion.TaskQueueState{running, planning, validating}
	p.Observe([]companion.Body{
		companionBody(1, 10), companionBody(2, 20), companionBody(3, 30),
	}, states, nil)
	// Observe 之后修改调用方切片：深拷贝必须保护载荷（含 Plan.Steps 与 FIFO）。
	states[0].Current.Plan.Steps[0].X = 999
	states[0].Pending[0] = "被篡改"

	if err := p.Poll(10); err != nil {
		t.Fatal(err)
	}
	save := receiveCompanionSave(t, store)
	want := []storage.StoredCompanionQueue{
		{
			ID:         running.ID,
			HasCurrent: true,
			Current: storage.StoredCompanionTask{
				Command: "跑起来的任务",
				PlanSteps: []companion.PlanStep{
					{Kind: companion.PlanStepGoTo, X: 4, Y: 64, Z: 0},
					{Kind: companion.PlanStepGoTo, X: 8, Y: 64, Z: 0},
				},
				StepIndex:     1,
				State:         companion.TaskRunning,
				StartTick:     12,
				DeadlineTicks: 1212,
			},
			Pending: []string{"跑步时排队"},
		},
		{
			ID:         planning.ID,
			HasCurrent: true,
			Current:    storage.StoredCompanionTask{Command: "规划中的任务", State: companion.TaskQueued},
		},
		{
			ID:         validating.ID,
			HasCurrent: true,
			Current:    storage.StoredCompanionTask{Command: "校验中的任务", State: companion.TaskQueued},
		},
	}
	if !reflect.DeepEqual(save.Queues, want) {
		t.Fatalf("保存载荷=%+v，想要 %+v", save.Queues, want)
	}
	store.complete(nil)
}

// restoredCompanionSeed 把一份 v2 载荷写入 MemoryStore，返回可传给
// mustNewHost 的存档，用于验证恢复接线（发令者事实不落盘，恢复使用合成
// 身份；本函数只构造身体与任务事实）。
func restoredCompanionSeed(
	t *testing.T,
	id companion.ID,
	position [3]float32,
	queue storage.StoredCompanionQueue,
) *hostTestStore {
	t.Helper()
	store := newHostTestStore()
	if err := store.SaveCompanions(context.Background(), storage.CompanionSave{
		Revision: 1,
		Records: []companion.Body{{
			ID:        id,
			Position:  position,
			Dimension: core.Overworld,
		}},
		Queues: []storage.StoredCompanionQueue{queue},
	}); err != nil {
		t.Fatalf("seed SaveCompanions: %v", err)
	}
	return store
}

func restoredCompanionHost(
	t *testing.T,
	definitions []companion.Definition,
	model *fakeCompanionModel,
	store storage.WorldStore,
) *Host {
	t.Helper()
	config := hostTestConfig()
	config.Companions = append([]companion.Definition(nil), definitions...)
	config.MaxPlayers = 2
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	if model != nil {
		config.AIModel.Endpoint = model.server.URL + "/v1"
	}
	host := mustNewHost(t, config, flatTestGenerator{}, store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host.Shutdown: %v", err)
		}
	})
	return host
}

// stepCollectingRestoreEvents 推进并收集聊天事件直到 stop 返回 true 或
// 超时。每次 tick 后留 1ms 墙钟，保证异步区块生成与伙伴出生扫描有机会
// 完成（与 stepUntilCompanionManagerReady 同一纪律——恢复用例从冷启动
// 推进，激活依赖真实的墙钟时间）。
func stepCollectingRestoreEvents(
	t *testing.T,
	host *Host,
	client network.ClientEndpoint,
	stop func(events []network.ChatEvent) bool,
) []network.ChatEvent {
	t.Helper()
	var collected []network.ChatEvent
	deadline := time.Now().Add(longWaitDeadline)
	for time.Now().Before(deadline) {
		result := host.world.StepForTest()
		collected = append(collected,
			companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
		if stop != nil && stop(collected) {
			return collected
		}
		time.Sleep(time.Millisecond)
	}
	return collected
}

// restoredCompanionSlot 在 stepMu 下读取槽位的当前任务与 FIFO 快照。
func restoredCompanionSlot(
	t *testing.T,
	host *Host,
	id companion.ID,
) (current companion.Task, hasCurrent bool, pendingLen int, hasPath bool) {
	t.Helper()
	host.world.stepMu.Lock()
	defer host.world.stepMu.Unlock()
	slot := host.world.companionManager.slots[id]
	if slot == nil {
		t.Fatalf("伙伴 %s 未注册槽位", id)
	}
	current, hasCurrent = slot.queue.Current()
	return current, hasCurrent, slot.queue.Len(), slot.path != nil
}

func TestCompanionManagerRestoresRunningTaskAndFIFOExactly(t *testing.T) {
	id := chatTestCompanionID(1)
	queue := storage.StoredCompanionQueue{
		ID:         id,
		HasCurrent: true,
		Current: storage.StoredCompanionTask{
			Command: "重启后继续走",
			PlanSteps: []companion.PlanStep{
				{Kind: companion.PlanStepGoTo, X: 4, Y: 1, Z: 2},
				{Kind: companion.PlanStepGoTo, X: 8, Y: 1, Z: 2},
			},
			StepIndex:     1,
			State:         companion.TaskRunning,
			StartTick:     33,
			DeadlineTicks: 1233,
		},
		Pending: []string{"第二条指令", "第三条指令"},
	}
	model := newFakeCompanionModel(t, [3]int32{16, 1, 2})
	host := restoredCompanionHost(
		t,
		[]companion.Definition{{ID: id, Name: "阿木"}},
		model,
		restoredCompanionSeed(t, id, [3]float32{0.5, 1, 0.5}, queue),
	)

	// 恢复立即可见：当前任务逐字段精确、FIFO 顺序保持、路径不落盘
	//（旧路径点绝不带过重启，首个动作前必须按当前世界重算）。
	current, hasCurrent, pendingLen, hasPath := restoredCompanionSlot(t, host, id)
	if !hasCurrent || pendingLen != 2 || hasPath {
		t.Fatalf(
			"恢复后槽位 hasCurrent=%v pending=%d hasPath=%v，想要当前任务+2 条 FIFO+无路径",
			hasCurrent, pendingLen, hasPath,
		)
	}
	if current.Command != "重启后继续走" || current.State != companion.TaskRunning ||
		current.StepIndex != 1 || current.StartTick != 33 || current.DeadlineTicks != 1233 ||
		len(current.Plan.Steps) != 2 ||
		current.Plan.Steps[1] != (companion.PlanStep{Kind: companion.PlanStepGoTo, X: 8, Y: 1, Z: 2}) {
		t.Fatalf("恢复的当前任务=%+v，想要精确保留持久化字段", current)
	}

	// 继续运行：从步骤索引 1 恢复执行，FIFO 在当前任务之后按序执行。
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x71, "发令者"))
	events := stepCollectingRestoreEvents(t, host, client, func(events []network.ChatEvent) bool {
		started := 0
		for _, event := range events {
			if event.Kind == network.ChatEventTaskStarted {
				started++
			}
		}
		return started >= 2
	})
	var started []string
	for _, event := range events {
		if event.Kind == network.ChatEventTaskStarted {
			started = append(started, event.Command)
		}
	}
	if len(started) != 2 || started[0] != "第二条指令" || started[1] != "第三条指令" {
		t.Fatalf("FIFO 恢复顺序=%v，想要 [第二条指令 第三条指令]", started)
	}
}

func TestCompanionManagerRestoresPlanningTaskAsQueuedAndReplans(t *testing.T) {
	id := chatTestCompanionID(2)
	// 存档忠实保存了 Planning 状态（模型计划尚未落盘）：恢复侧必须归一为
	// Queued 并重新发起规划。
	queue := storage.StoredCompanionQueue{
		ID:         id,
		HasCurrent: true,
		Current: storage.StoredCompanionTask{
			Command: "重启后重规划",
			State:   companion.TaskPlanning,
		},
	}
	model := newFakeCompanionModel(t, [3]int32{4, 1, 0})
	model.holdRequests()
	host := restoredCompanionHost(
		t,
		[]companion.Definition{{ID: id, Name: "阿木"}},
		model,
		restoredCompanionSeed(t, id, [3]float32{0.5, 1, 0.5}, queue),
	)

	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x72, "发令者"))
	// 区块就绪与出生扫描是异步的：逐 tick 推进（同步消费客户端消息保持
	// 流一致），直到规划请求抵达假模型。
	deadline := time.Now().Add(longWaitDeadline)
	for time.Now().Before(deadline) {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, client, result.Tick)
		if requests, _, _, _ := model.snapshotCounts(); requests >= 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("恢复的 Planning 任务没有在重启后重新发起规划")
}

func TestCompanionManagerRestoredRunningTaskRevalidatesAndDoesNotBlindWalk(t *testing.T) {
	id := chatTestCompanionID(3)
	// 恢复的 Running 任务目标在寻路窗口之外（近似“世界已变化”：旧路径
	// 不可信）：首个动作前必须按当前权威世界重算，重算不可达即按既有
	// 三次失败语义终止，全程不得产生任何位移。
	queue := storage.StoredCompanionQueue{
		ID:         id,
		HasCurrent: true,
		Current: storage.StoredCompanionTask{
			Command: "去远方",
			PlanSteps: []companion.PlanStep{
				{Kind: companion.PlanStepGoTo, X: 1000, Y: 1, Z: 0},
			},
			StepIndex:     0,
			State:         companion.TaskRunning,
			StartTick:     1,
			DeadlineTicks: 7201,
		},
	}
	host := restoredCompanionHost(
		t,
		[]companion.Definition{{ID: id, Name: "阿木"}},
		nil,
		restoredCompanionSeed(t, id, [3]float32{0.5, 1, 0.5}, queue),
	)

	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0x73, "发令者"))
	events := stepCollectingRestoreEvents(t, host, client, func(events []network.ChatEvent) bool {
		for _, event := range events {
			if event.Kind == network.ChatEventTaskFailed {
				return true
			}
		}
		return false
	})
	failed := eventsWithKind(events, network.ChatEventTaskFailed)
	if len(failed) != 1 ||
		network.TaskFailReason(failed[0].RejectReason) != network.TaskFailPathUnreachable {
		t.Fatalf("TaskFailed=%d，想要 1 次 PathUnreachable（事件=%v）",
			len(failed), chatEventKinds(events))
	}
	final := currentCompanionBody(t, host, id)
	if final.Position[0] != 0.5 || final.Position[2] != 0.5 {
		t.Fatalf("不可达恢复任务产生了位移：%v", final.Position)
	}
}
