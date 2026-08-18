//go:build darwin && race

package main

// raceEnabled 在 -race 构建下为 true，与 benchmark_server_norace_test.go 中
// 的 false 常量配对。服务端 benchmark 的 darwin-only 测试用它区分构建形态：
// TestScenarioV7 的 50ms 实时调度门禁在 race 开销（5-20x）+ 全仓并行争核下
// 测到的是机器负载而非产品行为（docs/superpowers/specs/2026-08-07-ci-stability-merge-gate-design.md §4
// 已实测四次假失败纯由调度延迟），因此该测试在 race 构建下跳过；非 race
// 路径的门禁原样保留。必须与 norace 文件成对存在，任一构建形态下恰好一个
// 定义生效。
const raceEnabled = true
