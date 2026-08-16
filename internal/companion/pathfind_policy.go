// 本文件实现路径点重验与重算策略：寻路结果携带区块 revision，Task Runner
// 在提交每个路径点前重验；失效按固定冷却重算，同一任务连续三次失败终止。
// 策略是纯值逻辑（无锁、无 goroutine），由 Task 6 的 tick 边界编排调用——
// 权威 tick 是唯一写者，策略本身不需要并发防护。
package companion

// PathReplanCooldownTicks 是路径失效后的固定重算冷却（tick）。冷却存在的
// 理由：世界在快速变化（玩家正在挖/放）时立即重算大概率再次失败，固定冷却
// 把重算频率与失败解耦，也让"连续三次失败"有明确的观察窗口。
const PathReplanCooldownTicks = 20

// MaxConsecutiveReplans 是同一任务允许的连续重算失败上限：第三次失败即令
// 任务以路径不可达终止，绝不无限重算（M5 设计 §9.2 硬门禁）。
const MaxConsecutiveReplans = 3

// PathPolicy 保存一个任务的路径重算状态。零值可用：没有失败记录、路径点
// 判定纯读。ShouldUse 是值接收者的纯读方法，不推进失败计数——判定与记录
// 分离让"检查一次、提交一次"的调用模式不会意外累积失败。
type PathPolicy struct {
	consecutiveFailures int
}

// ShouldUse 判定路径点是否可提交：索引必须在 [0, len(Waypoints))，且结果
// 携带的每个区块 revision 都与当前权威状态一致（当前列表是超集不影响——
// 路径只关心自己踩过的区块；缺失或失配都拒绝）。本方法为纯读，不改变任何
// 策略状态。
func (p PathPolicy) ShouldUse(result PathResult, waypointIndex int, current []ChunkRevision) bool {
	if waypointIndex < 0 || waypointIndex >= len(result.Waypoints) {
		return false
	}
	for _, want := range result.Revisions {
		found := false
		for _, have := range current {
			if have.Chunk == want.Chunk {
				if have.Revision != want.Revision {
					return false
				}
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// ReplanAfter 返回失效后允许再次重算的 tick（当前 tick + 固定冷却）。
func (p PathPolicy) ReplanAfter(nowTick uint64) uint64 {
	return nowTick + PathReplanCooldownTicks
}

// RecordFailure 记录一次重算失败并返回是否达到终止阈值：第三次（含）之后
// 恒为 true——终止态保持，直到 RecordSuccess 清零。
func (p *PathPolicy) RecordFailure() bool {
	p.consecutiveFailures++
	return p.consecutiveFailures >= MaxConsecutiveReplans
}

// RecordSuccess 记录一次成功的路径点使用并清零失败计数。
func (p *PathPolicy) RecordSuccess() {
	p.consecutiveFailures = 0
}
