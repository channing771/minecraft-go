package server

import "time"

// 活性等待期限：轮询直到条件成立、到点判失败的那一类等待用的期限。
//
// 这类期限是"放弃期限"而不是 sleep——条件一成立循环立刻返回，
// 因此在快机器上取大值零成本，唯一代价是真挂死时报错慢一些。
// 取值按等待角色分三档并保持数量级区分，使"哪一类等待挂了"
// 可以直接从报错耗时上读出来。
//
// 不适用于三类禁改站点：缺席断言（等一小段确认什么都没发生）、
// 超时触发断言（故意给极短期限、断言超时确实发生）、性能门禁
// （测耗时并断言小于上限）。抬高前两类只会拖慢测试，抬高第三类
// 等于放宽门禁。前两类的判据是断言期望超时发生，可用
// context.DeadlineExceeded 这个标识符 grep 出来。
//
// internal/server 的测试跨 package server 与 package server_test
// 两个包，未导出标识符无法共享，因此本组常量在
// deadline_external_test.go 中另有一份逐字相同的定义。改动时必须同步。
const (
	// shortWaitDeadline 用于单次保存启动等亚秒本机事件（原 100ms–500ms）。
	shortWaitDeadline = 5 * time.Second
	// waitDeadline 用于登录 ready、收到某条消息、库存达到某状态（原 1s–5s）。
	//
	// 1 秒档归这里而不是 shortWaitDeadline：它有 95 处、是本包最紧
	// 也最密集的一档，只抬到 5s 仅 5× 余量，覆盖不住共享 runner 的减速。
	waitDeadline = 30 * time.Second
	// longWaitDeadline 用于关服屏障、磁盘重启、八人会话等复合等待（原 10s–30s）。
	longWaitDeadline = 60 * time.Second
)
