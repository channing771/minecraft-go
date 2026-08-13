# project-identity Specification

## Purpose

统一当前产品、Go module、命令、native ABI、构建与开发入口的 Mornlea 身份，并以冻结 artifact 证明纯改名没有改变既有可观察行为。

## Requirements

### Requirement: 当前项目身份统一为 Mornlea
系统 MUST 以 `Mornlea` 作为当前产品名，以 `github.com/channing771/mornlea` 作为 Go module，以 `mornlea` 和 `mornlea-server` 作为客户端与专用服务端命令。

#### Scenario: clean checkout 构建新入口
- **WHEN** 从 clean checkout 执行 canonical build
- **THEN** MUST 生成 `bin/mornlea` 与同目录 `libmornlea_mesh.dylib`
- **AND** `cmd/mornlea-server` MUST 继续可在 Linux 无 CGO 下独立构建

#### Scenario: 旧入口不再发布
- **WHEN** 枚举当前 module、命令、native ABI、构建和 Hook 身份
- **THEN** MUST 不存在 `mcgo`/`mcgod` wrapper、旧 C symbol、旧 dylib 或旧环境变量 fallback

### Requirement: 改名保持固定行为与 artifact
系统 MUST 保持协议 v15、区块 schema v8、玩家 schema v6、metadata v2、benchmark scenario v15、ABI version/status、fixture、golden 与性能 baseline 不变。

#### Scenario: 改名前后不变量逐字节一致
- **GIVEN** 已在合并主线后的统一基线冻结固定 artifact
- **WHEN** 完成身份切换
- **THEN** 所有静态 fixture/baseline hash 与按 basename 比较的 10 张 golden MUST 完全一致

#### Scenario: Apple M2 已批准的同环境视觉基线不掩盖改名漂移
- **GIVEN** Apple M2/macOS 上的原始 Task 1 `origin/main` 仅有 `materials-showcase` 和 `oak-grove` 两个精确已知失败
- **WHEN** 原始主线与 Mornlea 分支在同一隔离 HOME 下运行非更新 capture
- **THEN** 两边 10 个场景 PNG 与两个失败的 actual/diff MUST 逐字节一致
- **AND** 失败摘要 MUST 精确保持 `materials-showcase` 最大差 1/26 像素/0.0113% 与 `oak-grove` 最大差 47/10 像素/0.0043%
- **AND** 其余 8 个场景 MUST 通过 tracked golden，不得修改 golden、阈值或 capture 代码

#### Scenario: 非 Apple M2 的同环境视觉基线不掩盖改名漂移
- **GIVEN** `system_profiler SPHardwareDataType` 的 Chip 不是 `Apple M2`
- **WHEN** 原始主线与 Mornlea 分支在同一隔离 HOME 下运行非更新 capture
- **THEN** 两边 10 个场景 PNG MUST 逐字节一致，且两次 `visual-check` MUST 退出 0
- **AND** 两边都 MUST 不产生 `*-actual.png` 或 `*-diff.png`，不得修改 golden、阈值或 capture 代码
