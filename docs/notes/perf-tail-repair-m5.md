# M5 飞行尾延迟修复验证

## 结论

2026-08-03 在 `Apple M5 / 24GiB` 上完成一次 scenario v7 Memory 无窗口诊断。flying p99 为 `6.387ms`，通过既有 `< 12ms` 绝对门禁；`perfcheck` 自比较同时通过报告完整性与全部绝对门禁。测试没有修改阈值，也没有创建前台窗口。

## 修复与兼容性

- 修复提交：`538c3642bcb51f4aded98d65d6b07e9034d70954`。
- 根因：benchmark 原来把消息排空上限 `4096` 同时当作单帧 mesher 调度、回收上限；M5 的更多 worker 容易把完成结果集中到一个被计时帧。
- 修复：载入阶段保持 `4096/4096`，预热和正式计时分别使用消息上限 `4096`、网格上限 `64`，与交互客户端一致。
- workload 语义变化，因此报告从 scenario v6 升为 v7；现有 M2 v6 基线保持冻结，不与 v7 静默混比。

## 单次无窗口诊断

执行前确认 tracked state 干净、精确 HEAD、全新报告路径且没有遗留 `mcgo`、`mcgod` 或 benchmark 进程。执行命令：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-tail-repair-538c3642bcb5-memory.json'
```

环境与报告身份：

- scenario：`7`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`
- Go：`go1.26.0 darwin/arm64`
- JSON SHA-256：`ec2c1afde444209421da6c9b072d29dee96d9b1678bf54d724088e5a33125032`

计时结果：

| 阶段 | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| still | 284.0 | 3.411ms | 3.898ms | 4.872ms | 25.702ms | 996.8MiB |
| flying | 572.1 | 1.257ms | 3.850ms | 6.387ms | 101.636ms | 1005.0MiB |

修复前的 v6 诊断曾得到 flying p99 `14.149ms` 并失败。两次报告属于不同 workload 版本，只能用于说明绝对门禁问题已消失，不能计算同场景相对提升。

自比较命令：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-tail-repair-538c3642bcb5-memory.json --current /tmp/mcgo-tail-repair-538c3642bcb5-memory.json --max-regression 0.20'
```

输出：

```text
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
```

## 正式 M5 基线恢复条件

本次报告只验证修复，不是正式 M5 基线，也没有运行 TCP 报告或写入 `docs/notes/perf-baseline-m5.json`。恢复 `apple-m5-performance-baseline` 前必须：

1. 先把该 change 的 proposal、spec、design、tasks 从 scenario v6 更新到 v7，并重新严格校验。
2. 在包含本验证文档的干净精确 HEAD 上重新冻结目标路径、进程状态和 M2 文件哈希。
3. 重新取得一次性正式执行授权，再各运行一次全新的 Memory 与 TCP 报告链；任一步失败即停止且不得自动重跑。
4. 只有 Memory 自检和同硬件 TCP 比较全部通过后，才提升 Memory 原始字节为独立 M5 基线；M2 v6 文件继续保持不变。
