# hardware-performance-baselines Specification

## Purpose

为不同 Apple Silicon 硬件保存彼此独立、来源可审计的性能比较起点，避免把芯片和内存差异误判为代码性能变化。

## Requirements

### Requirement: 不同硬件使用独立基线
项目 SHALL 为硬件标识不同的正式报告保存独立基线，并 MUST 保留已经接受的其他硬件基线不变。

#### Scenario: 为 M5 建立基线
- **WHEN** 当前报告的硬件标识为 `Apple M5 / 24GiB`，而现有基线标识为 `Apple M2 / 16GiB`
- **THEN** 项目新增 M5 专用基线文件，并保持 M2 基线内容和路径不变

#### Scenario: 拒绝跨硬件比较
- **WHEN** 使用 M2 基线比较 M5 当前报告
- **THEN** 性能比较 MUST 拒绝该组合，且任何基线文件均不得被覆盖

### Requirement: 新硬件基线只能由通过门禁的无窗口报告建立
新硬件基线 MUST 来自固定 scenario v7、Memory transport、2560x1440 的无窗口正式报告；该报告 MUST 通过现有完整性和绝对门禁，并 MUST 与同一硬件、同一场景的一次 TCP 报告通过现有跨 transport 比较后才能接受。

#### Scenario: 正式链全部通过
- **WHEN** M5 Memory 报告通过完整性和绝对门禁，且同次 M5 TCP 报告相对该 Memory 报告通过跨 transport 比较
- **THEN** 项目接受该 Memory 报告的精确字节作为 M5 基线，并记录硬件、提交、命令和报告哈希

#### Scenario: 正式链任一步失败
- **WHEN** 无窗口报告生成、完整性门禁或跨 transport 比较任一步失败
- **THEN** 项目 MUST 停止正式链，不得重跑失败步骤、放宽阈值或创建和覆盖正式基线

#### Scenario: 工作负载修复后重新开始
- **WHEN** 旧场景的正式链已经失败，且 benchmark 工作负载修复后升级为新场景
- **THEN** 项目 MUST 提交更新后的计划、重新取得一次性正式授权并使用全新路径执行完整报告链，且不得提升旧场景输出或修复诊断报告

### Requirement: 调用者明确选择匹配硬件的基线
性能比较 SHALL 继续通过显式基线路径选择硬件专用文件，不引入自动硬件探测或跨硬件归一化。

#### Scenario: M5 后续回归比较
- **WHEN** 调用者在 M5 上生成后续 scenario v7 报告
- **THEN** 调用者可显式传入 M5 基线文件，并由现有同硬件规则执行比较
