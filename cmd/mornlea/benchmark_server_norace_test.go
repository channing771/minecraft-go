//go:build darwin && !race

package main

// raceEnabled 在非 -race 构建下为 false，与 benchmark_server_race_test.go 中
// 的 true 常量配对（配对原因见该文件注释）。darwin 平台下恰好一个定义生效，
// 供 TestScenarioV7 等实时门禁测试判断是否跳过。
const raceEnabled = false
