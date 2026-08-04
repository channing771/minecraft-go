## Context

见 `proposal.md`。现有 `perfcheck` 已接受显式 `--baseline` 路径，并在报告 `hardware` 不同时拒绝比较；`mcgo --benchmark` 已使用 `NewHeadlessDevice` 和离屏纹理，不创建窗口。此前 M5 scenario v6 正式 Memory 执行得到 flying p99 `14.149ms` 后失败且未写出 JSON；`reduce-flying-tail-latency` 随后把 workload 升级为 v7，一次非正式无窗口诊断已通过，但没有 TCP 配对，因此不得直接提升。当前 M2 v6 基线必须保持不变。

## Goals / Non-Goals

**Goals:**

- 用现有命令建立可独立选择的 M5 scenario v7 Memory 基线。
- 保留完整 provenance、哈希和一次性正式执行结果。
- 让失败保持无副作用。

**Non-Goals:**

- 不修改比较器、报告 schema、阈值、scenario v7 生产者或其他生产代码。
- 不新增基线注册表、自动选择器或硬件归一化算法。
- 不把 M5 基线设为所有机器的默认基线。

## Decisions

### 1. 增加旁路文件，不重命名现有基线

新增 `docs/notes/perf-baseline-m5.json` 和 `docs/notes/perf-baseline-m5.md`。现有无后缀文件继续代表已经接受的 M2 基线，避免破坏历史命令、哈希和链接。

否决把所有硬件放入一个 JSON：现有比较器读取单份 `PerfReport`，容器格式会要求无必要的代码和 schema 变更。

### 2. 使用现有 headless scenario v7 生产者启动全新正式链

更新后的规划先单独提交；随后记录精确、干净的 HEAD，冻结 M2 文件哈希，确认没有 benchmark 进程且全新的 v7 临时目标不存在。当前机器无法保证理想空闲，因此 preflight 只记录电源、硬件和可见系统负载，不把人工清理环境作为可伪造的通过条件。取得针对该 HEAD 与路径的一次性正式授权后，Memory 与 TCP 各执行一次 `mcgo --benchmark`，两次均使用固定 2560x1440 离屏渲染。

Memory 报告先以自身作为两端执行一次 `perfcheck`，复用现有完整性和绝对门禁；随后以 Memory 为 baseline、TCP 为 current 执行跨 transport 比较。任何命令非零立即停止，不重跑。

旧 v6 失败执行没有可提升的 JSON；修复提交上的 v7 诊断只证明绝对门禁已恢复，不满足正式授权、精确最终 HEAD 与 TCP 配对条件，也不得复用。否决沿用旧路径或把诊断报告改名为基线：两者都会破坏一次性执行的 provenance。

否决新增“只检查绝对门禁”CLI：同份报告比较已经覆盖所需校验，新增模式只会扩大工具表面。

### 3. 只提升通过链的 Memory 原始字节

全部门禁通过后，把临时 Memory JSON 的精确内容写入 M5 基线，不手工改数值。中文文档记录硬件、OS、Go、commit、两份报告 SHA-256、命令、门禁输出和限制。

TCP 报告只用于建立时的 transport parity，不作为长期基线；后续 M5 回归显式传入 M5 Memory 文件。

## Risks / Trade-offs

- [单次报告包含现实环境噪声] → 记录 preflight、固定 workload 与分辨率，并使用现有绝对门禁及同次 TCP 20% 门禁；后续回归积累证据，不假装能清空系统，也不在本变更中引入统计系统。
- [M5 文件不会自动选择] → 文档给出精确命令；自动选择只有在多硬件日常使用造成真实摩擦时再增加。
- [正式命令失败后没有基线] → 保留失败证据并停止，绝不以重跑或修改阈值掩盖失败。

## Migration Plan

1. 提交并校验更新后的 OpenSpec，重新记录精确、干净的 HEAD、M2 哈希和全新 v7 路径。
2. 取得针对该 HEAD 的一次性正式授权，执行一次 Memory、自检、一次 TCP 和跨 transport 比较。
3. 仅在全部成功后新增 M5 JSON 与中文文档，并验证 M2 哈希未变。
4. 回退时删除 M5 两个文件；M2 v6 基线、比较器和 scenario v7 生产代码不受影响。
