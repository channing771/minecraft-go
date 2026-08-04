## Context

见 `proposal.md`。现有 `perfcheck` 已接受显式 `--baseline` 路径，并在报告 `hardware` 不同时拒绝比较；`mcgo --benchmark` 已使用 `NewHeadlessDevice` 和离屏纹理，不创建窗口。当前 M2 基线必须保持不变。

## Goals / Non-Goals

**Goals:**

- 用现有命令建立可独立选择的 M5 scenario v6 Memory 基线。
- 保留完整 provenance、哈希和一次性正式执行结果。
- 让失败保持无副作用。

**Non-Goals:**

- 不修改比较器、报告 schema、阈值或 benchmark 场景。
- 不新增基线注册表、自动选择器或硬件归一化算法。
- 不把 M5 基线设为所有机器的默认基线。

## Decisions

### 1. 增加旁路文件，不重命名现有基线

新增 `docs/notes/perf-baseline-m5.json` 和 `docs/notes/perf-baseline-m5.md`。现有无后缀文件继续代表已经接受的 M2 基线，避免破坏历史命令、哈希和链接。

否决把所有硬件放入一个 JSON：现有比较器读取单份 `PerfReport`，容器格式会要求无必要的代码和 schema 变更。

### 2. 使用现有 headless scenario v6 生产者

Memory 与 TCP 各执行一次 `mcgo --benchmark`，两次均使用固定 2560x1440 离屏渲染。正式执行前确认 tracked state 干净、精确 HEAD、无 benchmark 进程、临时目标不存在，并冻结 M2 文件哈希。

Memory 报告先以自身作为两端执行一次 `perfcheck`，复用现有完整性和绝对门禁；随后以 Memory 为 baseline、TCP 为 current 执行跨 transport 比较。任何命令非零立即停止，不重跑。

否决新增“只检查绝对门禁”CLI：同份报告比较已经覆盖所需校验，新增模式只会扩大工具表面。

### 3. 只提升通过链的 Memory 原始字节

全部门禁通过后，把临时 Memory JSON 的精确内容写入 M5 基线，不手工改数值。中文文档记录硬件、OS、Go、commit、两份报告 SHA-256、命令、门禁输出和限制。

TCP 报告只用于建立时的 transport parity，不作为长期基线；后续 M5 回归显式传入 M5 Memory 文件。

## Risks / Trade-offs

- [单次报告包含环境噪声] → 固定场景、空闲 preflight 和现有 20% 门禁限制误判；后续回归积累证据，不在本变更中引入统计系统。
- [M5 文件不会自动选择] → 文档给出精确命令；自动选择只有在多硬件日常使用造成真实摩擦时再增加。
- [正式命令失败后没有基线] → 保留失败证据并停止，绝不以重跑或修改阈值掩盖失败。

## Migration Plan

1. 提交并校验本 OpenSpec，使 benchmark 对应精确、干净的 HEAD。
2. 执行一次 Memory、自检、一次 TCP 和跨 transport 比较。
3. 仅在全部成功后新增 M5 JSON 与中文文档，并验证 M2 哈希未变。
4. 回退时删除 M5 两个文件；M2 基线和比较器不受影响。
