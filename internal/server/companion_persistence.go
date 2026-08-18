package server

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sort"
	"sync"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/storage"
)

type companionPersistence struct {
	store        storage.CompanionStore
	config       Config
	mu           sync.Mutex
	completionMu sync.Mutex
	records      []companion.Body
	// tasks 是最近一次 Observe 的任务域观察输入；任务状态变化即令存档
	// dirty。latestJobLocked 在投递保存前把它转换为 storage 载荷（含
	// Planning/Validating→Queued 的保存侧归一）。
	tasks []companion.TaskQueueState
	// summaries 是最近一次 Observe 的最近对话摘要观察输入（D6）：终态
	// Dialogue 响应写入 manager 后随 Observe 进入这里，摘要变化即令存档
	// dirty，落盘时并入 StoredCompanionQueue.Summary（含 summary-only 条目）。
	summaries []companionSummaryState
	// loadedQueues 是启动加载时存档携带的任务域载荷，构造后不变；newWorld
	// 在构造 Companion Manager 后读取一次用于恢复接线。
	loadedQueues []storage.StoredCompanionQueue
	persisted    uint64
	dirty        bool
	inFlight     bool
	inFlightJob  companionSaveJob
	retry        *companionSaveJob
	jobs         chan companionSaveJob
	completions  chan companionSaveCompletion
	ctx          context.Context
	cancel       context.CancelFunc
	waitGroup    sync.WaitGroup
	closed       bool
	closeOnce    sync.Once
}

type companionSaveJob struct {
	Save     storage.CompanionSave
	Attempt  uint32
	NextTick uint64
}

type companionSaveCompletion struct {
	Job companionSaveJob
	Err error
}

func newCompanionPersistence(
	store storage.CompanionStore,
	loaded storage.StoredCompanions,
	config Config,
) *companionPersistence {
	ctx, cancel := context.WithCancel(context.Background())
	persistence := &companionPersistence{
		store:        store,
		config:       config,
		records:      cloneAndSortCompanionBodies(loaded.Records),
		loadedQueues: cloneStoredQueues(loaded.Queues),
		persisted:    loaded.Revision,
		jobs:         make(chan companionSaveJob, 1),
		completions:  make(chan companionSaveCompletion, 1),
		ctx:          ctx,
		cancel:       cancel,
	}
	persistence.waitGroup.Add(1)
	go persistence.worker()
	return persistence
}

// companionSummaryState 是一个 active 伙伴的最近对话摘要观察输入：manager
// 在 tick 边界产出（按 ID 字节序），Observe 参与dirty 判定，保存时并入
// StoredCompanionQueue.Summary。空摘要不出现在观察输入中（等价于无摘要）。
type companionSummaryState struct {
	ID      companion.ID
	Summary string
}

// Observe 合并权威身体、任务域与摘要观察输入：任一变化即标记存档 dirty。
// tasks、summaries 与身体同批观察保证冻结快照（关服最终保存）里身体、任务
// 状态与摘要属于同一权威 tick。
func (p *companionPersistence) Observe(
	active []companion.Body,
	tasks []companion.TaskQueueState,
	summaries []companionSummaryState,
) {
	p.mu.Lock()
	defer p.mu.Unlock()
	byID := make(map[companion.ID]companion.Body, len(p.records)+len(active))
	for _, body := range p.records {
		byID[body.ID] = body
	}
	for _, body := range active {
		byID[body.ID] = body
	}
	if len(byID) > companion.MaxStored {
		panic("server: companion persistence exceeds stored record limit")
	}
	records := make([]companion.Body, 0, len(byID))
	for _, body := range byID {
		records = append(records, body)
	}
	sortCompanionBodies(records)
	tasksChanged := !equalTaskQueueStates(tasks, p.tasks)
	summariesChanged := !equalCompanionSummaries(summaries, p.summaries)
	if slices.Equal(records, p.records) && !tasksChanged && !summariesChanged {
		return
	}
	p.records = records
	p.tasks = cloneTaskQueueStates(tasks)
	p.summaries = cloneCompanionSummaries(summaries)
	p.dirty = true
}

// equalCompanionSummaries 比较两份摘要观察输入是否逐条相等（ID 与文本）。
func equalCompanionSummaries(left, right []companionSummaryState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// cloneCompanionSummaries 深拷贝摘要观察输入，Observe 之后的任何调用方修改
// 都不影响已冻结的快照。
func cloneCompanionSummaries(summaries []companionSummaryState) []companionSummaryState {
	cloned := make([]companionSummaryState, len(summaries))
	copy(cloned, summaries)
	return cloned
}

// equalTaskQueueStates 比较两份任务域观察输入是否逐字段相等（含计划步骤）。
func equalTaskQueueStates(left, right []companion.TaskQueueState) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !equalTaskQueueState(left[index], right[index]) {
			return false
		}
	}
	return true
}

func equalTaskQueueState(left, right companion.TaskQueueState) bool {
	if left.ID != right.ID || left.HasCurrent != right.HasCurrent ||
		!slices.Equal(left.Pending, right.Pending) {
		return false
	}
	if !left.HasCurrent {
		return true
	}
	return equalTask(left.Current, right.Current)
}

func equalTask(left, right companion.Task) bool {
	return left.Generation == right.Generation &&
		left.Command == right.Command &&
		left.StepIndex == right.StepIndex &&
		left.State == right.State &&
		left.StartTick == right.StartTick &&
		left.DeadlineTicks == right.DeadlineTicks &&
		left.FailReason == right.FailReason &&
		equalPlan(left.Plan, right.Plan)
}

func equalPlan(left, right companion.Plan) bool {
	if left.Summary != right.Summary || len(left.Steps) != len(right.Steps) {
		return false
	}
	for index := range left.Steps {
		if left.Steps[index] != right.Steps[index] {
			return false
		}
	}
	return true
}

// cloneTaskQueueStates 深拷贝任务域观察输入：Pending 切片与当前任务的计划
// 步骤都独立于调用方，Observe 之后的任何调用方修改都不影响已冻结的快照。
func cloneTaskQueueStates(states []companion.TaskQueueState) []companion.TaskQueueState {
	cloned := make([]companion.TaskQueueState, len(states))
	for index := range states {
		cloned[index] = states[index]
		cloned[index].Pending = slices.Clone(states[index].Pending)
		cloned[index].Current.Plan.Steps = slices.Clone(states[index].Current.Plan.Steps)
	}
	return cloned
}

func (p *companionPersistence) Poll(tick uint64) error {
	p.completionMu.Lock()
	defer p.completionMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()

	var result error
	for {
		select {
		case completion := <-p.completions:
			if err := p.applyCompletionLocked(completion, tick); err != nil {
				result = errors.Join(result, err)
			}
		default:
			goto drained
		}
	}

drained:
	if p.inFlight || p.closed {
		return result
	}
	if p.retry != nil {
		if p.retry.NextTick <= tick {
			job := cloneCompanionSaveJob(*p.retry)
			if p.dispatchLocked(job) {
				p.retry = nil
			}
		}
		return result
	}
	if p.dirty && tick%p.config.AutosaveTicks == 0 {
		p.dispatchLocked(p.latestJobLocked())
	}
	return result
}

func (p *companionPersistence) Flush(ctx context.Context) error {
	if ctx == nil {
		panic("server: nil companion flush context")
	}
	p.completionMu.Lock()
	defer p.completionMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}

	p.mu.Lock()
	inherited := p.inFlight
	hasRetry := !inherited && p.retry != nil
	hasDirty := p.dirty
	p.mu.Unlock()

	switch {
	case inherited:
		if err := p.waitForInflight(ctx); err != nil {
			return err
		}
	case hasRetry:
		if err := p.dispatchAndWait(ctx, true); err != nil {
			return err
		}
	case hasDirty:
		if err := p.dispatchAndWait(ctx, false); err != nil {
			return err
		}
	default:
		return nil
	}

	p.mu.Lock()
	dirty := p.dirty
	p.mu.Unlock()
	if !dirty {
		return nil
	}
	return p.dispatchAndWait(ctx, false)
}

func (p *companionPersistence) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		p.mu.Unlock()
		p.cancel()
		p.waitGroup.Wait()
	})
}

func (p *companionPersistence) worker() {
	defer p.waitGroup.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.jobs:
			err := p.store.SaveCompanions(p.ctx, cloneCompanionSave(job.Save))
			select {
			case p.completions <- companionSaveCompletion{Job: job, Err: err}:
			case <-p.ctx.Done():
				return
			}
		}
	}
}

func (p *companionPersistence) dispatchAndWait(ctx context.Context, retry bool) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return p.ctx.Err()
	}
	var job companionSaveJob
	if retry {
		if p.retry == nil {
			p.mu.Unlock()
			return nil
		}
		job = cloneCompanionSaveJob(*p.retry)
	} else {
		if !p.dirty {
			p.mu.Unlock()
			return nil
		}
		job = p.latestJobLocked()
	}
	if !p.dispatchLocked(job) {
		p.mu.Unlock()
		return nil
	}
	if retry {
		p.retry = nil
	}
	p.mu.Unlock()
	return p.waitForInflight(ctx)
}

func (p *companionPersistence) waitForInflight(ctx context.Context) error {
	for {
		select {
		case completion := <-p.completions:
			p.mu.Lock()
			matched := p.inFlight &&
				p.inFlightJob.Save.Revision == completion.Job.Save.Revision
			err := p.applyCompletionLocked(completion, 0)
			p.mu.Unlock()
			if matched {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-p.ctx.Done():
			return p.ctx.Err()
		}
	}
}

func (p *companionPersistence) dispatchLocked(job companionSaveJob) bool {
	if p.closed || p.inFlight {
		return false
	}
	queued := cloneCompanionSaveJob(job)
	select {
	case p.jobs <- queued:
		p.inFlight = true
		p.inFlightJob = cloneCompanionSaveJob(job)
		return true
	default:
		return false
	}
}

func (p *companionPersistence) applyCompletionLocked(
	completion companionSaveCompletion,
	tick uint64,
) error {
	if !p.inFlight || p.inFlightJob.Save.Revision != completion.Job.Save.Revision {
		return nil
	}
	p.inFlight = false
	p.inFlightJob = companionSaveJob{}
	if completion.Err != nil {
		retry := cloneCompanionSaveJob(completion.Job)
		attempt := retry.Attempt
		if attempt == 0 {
			attempt = 1
		}
		retry.NextTick = saturatingAddUint64(
			tick,
			retryDelay(p.config.RetryBaseTicks, p.config.RetryMaxTicks, attempt),
		)
		if attempt < ^uint32(0) {
			retry.Attempt = attempt + 1
		}
		p.retry = &retry
		p.dirty = true
		return completion.Err
	}
	p.persisted = completion.Job.Save.Revision
	p.retry = nil
	// 任务域与摘要的 dirty 重判使用「未激活丢弃」口径：身体记录尚未出现的
	// 队列无法落盘（编码要求队列关联记录），保持 dirty 让激活后的首次保存
	// 补上完整载荷，窗口内的排队指令与摘要不会静默丢失。
	currentQueues, droppedPending := companionQueuesForSave(p.tasks, p.records, p.summaries)
	p.dirty = !slices.Equal(p.records, completion.Job.Save.Records) ||
		droppedPending ||
		!equalStoredQueues(currentQueues, completion.Job.Save.Queues)
	return nil
}

func (p *companionPersistence) latestJobLocked() companionSaveJob {
	queues, _ := companionQueuesForSave(p.tasks, p.records, p.summaries)
	return companionSaveJob{
		Save: storage.CompanionSave{
			Revision: p.persisted + 1,
			Records:  slices.Clone(p.records),
			Queues:   queues,
		},
		Attempt: 1,
	}
}

func cloneCompanionSaveJob(job companionSaveJob) companionSaveJob {
	job.Save = cloneCompanionSave(job.Save)
	return job
}

func cloneCompanionSave(save storage.CompanionSave) storage.CompanionSave {
	save.Records = slices.Clone(save.Records)
	save.Queues = cloneStoredQueues(save.Queues)
	return save
}

// cloneStoredQueues 深拷贝任务域载荷（计划步骤与 FIFO），返回值与输入切片
// 完全独立。
func cloneStoredQueues(queues []storage.StoredCompanionQueue) []storage.StoredCompanionQueue {
	if queues == nil {
		return nil
	}
	cloned := make([]storage.StoredCompanionQueue, len(queues))
	for index := range queues {
		cloned[index] = queues[index]
		cloned[index].Current.PlanSteps = slices.Clone(queues[index].Current.PlanSteps)
		cloned[index].Pending = slices.Clone(queues[index].Pending)
	}
	return cloned
}

// companionQueuesForSave 把任务域观察输入转换为存档载荷并执行保存侧归一：
// Planning/Validating 尚未通过验证，按 Queued + 原始指令落盘（spec：模型
// 计划只在 Validating 成功后落盘）；Running 精确保留计划、步骤索引与
// deadline；终态快照（防御路径，正常快照不会出现）不落当前任务。records
// 是当前已知身体记录：队列必须关联记录才能编码，身体尚未激活（出生扫描
// 在途）的伙伴的队列被丢弃并经 dropped 报告——调用方保持 dirty，激活后的
// 首次保存补上完整载荷。summaries 是 manager 的最近对话摘要观察输入：逐条
// 并入对应伙伴的队列载荷；只持有摘要而无任务无 FIFO 的 active 伙伴也产出
// summary-only 条目（守卫不得丢弃摘要），inactive 伙伴不提供队列（含摘要），
// 去激活由此天然丢弃摘要（D4 语义）。返回值深拷贝自输入，与调用方切片完全
// 独立。
func companionQueuesForSave(
	states []companion.TaskQueueState,
	records []companion.Body,
	summaries []companionSummaryState,
) (queues []storage.StoredCompanionQueue, dropped bool) {
	known := make(map[companion.ID]struct{}, len(records))
	for _, body := range records {
		known[body.ID] = struct{}{}
	}
	summaryByID := make(map[companion.ID]string, len(summaries))
	for _, summary := range summaries {
		summaryByID[summary.ID] = summary.Summary
	}
	queues = make([]storage.StoredCompanionQueue, 0, len(states)+len(summaries))
	inStates := make(map[companion.ID]struct{}, len(states))
	for _, state := range states {
		if _, exists := known[state.ID]; !exists {
			dropped = true
			continue
		}
		inStates[state.ID] = struct{}{}
		queue := storage.StoredCompanionQueue{ID: state.ID, Summary: summaryByID[state.ID]}
		if state.HasCurrent {
			current := state.Current
			switch {
			case current.State.Terminal():
				// 终态任务在快照瞬间已被清出当前槽；这里只是防御，
				// 不落任何任务事实。
			case current.State == companion.TaskPlanning ||
				current.State == companion.TaskValidating:
				queue.HasCurrent = true
				queue.Current = storage.StoredCompanionTask{
					Command: string(current.Command),
					State:   companion.TaskQueued,
				}
			default:
				queue.HasCurrent = true
				queue.Current = storage.StoredCompanionTask{
					Command:       string(current.Command),
					PlanSteps:     slices.Clone(current.Plan.Steps),
					StepIndex:     current.StepIndex,
					State:         current.State,
					StartTick:     current.StartTick,
					DeadlineTicks: current.DeadlineTicks,
				}
			}
		}
		if len(state.Pending) != 0 {
			queue.Pending = make([]string, len(state.Pending))
			for index, command := range state.Pending {
				queue.Pending[index] = string(command)
			}
		}
		// 守卫含非空摘要：无任务无 FIFO 但有摘要的 active 伙伴不能丢摘要
		//（spec：终态摘要持久，重新激活前不能凭空消失）。
		if queue.HasCurrent || len(queue.Pending) != 0 || queue.Summary != "" {
			queues = append(queues, queue)
		}
	}
	// summary-only 条目：持有摘要但没有任何任务事实（states 不含）的 active
	// 伙伴。states 与 summaries 各自按 ID 字节序构造，两组拼接不保证全局
	// 有序（编码器按记录 ID 关联队列，只要求队列 ID 唯一）。
	for _, summary := range summaries {
		if _, exists := inStates[summary.ID]; exists {
			continue
		}
		if _, knownBody := known[summary.ID]; !knownBody {
			dropped = true
			continue
		}
		queues = append(queues, storage.StoredCompanionQueue{
			ID:      summary.ID,
			Summary: summary.Summary,
		})
	}
	return queues, dropped
}

// equalStoredQueues 逐字段比较两份存档载荷（含计划步骤、FIFO 顺序与最近
// 对话摘要——摘要差异不得被判 clean，否则摘要变化后的保存会被跳过），供
// 保存完成后的 dirty 重判使用。
func equalStoredQueues(left, right []storage.StoredCompanionQueue) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		a, b := left[index], right[index]
		if a.ID != b.ID || a.HasCurrent != b.HasCurrent || a.Summary != b.Summary ||
			!slices.Equal(a.Pending, b.Pending) {
			return false
		}
		if a.HasCurrent && !equalStoredCompanionTask(a.Current, b.Current) {
			return false
		}
	}
	return true
}

func equalStoredCompanionTask(a, b storage.StoredCompanionTask) bool {
	return a.Command == b.Command &&
		a.StepIndex == b.StepIndex &&
		a.State == b.State &&
		a.StartTick == b.StartTick &&
		a.DeadlineTicks == b.DeadlineTicks &&
		a.FailReason == b.FailReason &&
		slices.Equal(a.PlanSteps, b.PlanSteps)
}

func cloneAndSortCompanionBodies(records []companion.Body) []companion.Body {
	clone := slices.Clone(records)
	sortCompanionBodies(clone)
	return clone
}

func sortCompanionBodies(records []companion.Body) {
	sort.Slice(records, func(left, right int) bool {
		return bytes.Compare(records[left].ID[:], records[right].ID[:]) < 0
	})
}
