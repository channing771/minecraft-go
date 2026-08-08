package server_test

import "time"

// 活性等待期限，与 deadline_test.go 中的定义逐字相同。
//
// internal/server 的测试跨 package server 与 package server_test
// 两个包，未导出标识符无法跨包共享；为三个常量新建 internal 包并到
// internal/archcheck/deps_test.go 登记依赖，机械成本高于它解决的问题，
// 因此选择重复定义。两份必须同步改动。
//
// 完整的分类说明与禁改区定义见 deadline_test.go。
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
