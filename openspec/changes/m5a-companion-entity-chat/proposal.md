## Why

Mornlea 已有多人权威世界、持久化和远端玩家呈现，但没有独立于玩家会话的具名伙伴实体，也没有从游戏内文本确定性寻址伙伴的闭环。M5A 先建立可配置、可持久、可见但保持静止的伙伴基础，让后续自然语言规划建立在稳定身份、权威状态和明确协议之上。

## What Changes

- 增加可选的 `ai.companions` 静态配置，支持 `0..4` 个具有独立 16-byte `CompanionID` 和大小写敏感唯一名称的伙伴；缺少或为空时保持 AI 关闭。
- 在权威服务端创建最多四个静态 idle 伙伴，保持每个伙伴固定 `3×3` 区块兴趣，同时维持最多八名玩家的既有容量。
- **BREAKING**：线上协议从 v15 升到 v16；保留全部既有 message ID，并追加固定的 Chat/Companion message ID。v15 与 v16 不跨版本互通。
- 增加 `@名称 指令` 的确定性 tick 边界寻址和事实事件广播；M5A 只确认寻址，不规划、不排队、不执行世界动作。
- 增加 `companions.ai` schema v1，持久化最多 64 条 active+inactive 伙伴身体记录；损坏或未来版本在 AI 启用时拒绝启动，保存失败保留旧文件和 dirty 状态重试。
- 客户端增加独立只读伙伴镜像、插值、统一 Avatar/NameTag 呈现、固定容量聊天输入与 HUD；断线时清空伙伴和聊天状态。
- 在 `oak-grove` 后追加唯一末场景 `ai-companion`，只在人工确认后新增一张 golden，旧场景顺序和既有 golden 保持不变。
- benchmark scenario 升到 v16，记录 Avatar、NameTag 与 Hotbar HUD 固定上传布局变化；当前只接受显式 `15:16` 迁移，v6..v15 历史报告继续可读，M2 v15 与 M5 v14 基线字节不变，性能数值继续只记录。
- 不引入 Planner、HTTP 模型调用、FIFO、路径移动、采掘、放置、跟随、停止旁路、persona、近期摘要、通用 `actorState` 或第二套 renderer。

## Capabilities

### New Capabilities

- `companion-identity-configuration`: 定义伙伴独立身份、静态名称配置、数量与规范化边界。
- `authoritative-companion-entities`: 定义服务端权威静态伙伴、独立玩家容量、固定兴趣与确定性状态。
- `companion-chat-protocol`: 定义协议 v16 的伙伴/聊天消息和只寻址不执行的 `@名称 指令` 语义。
- `companion-persistence`: 定义 `companions.ai` schema v1、64 条上限、inactive 保留与可靠保存/恢复。
- `companion-client-presentation`: 定义客户端只读镜像、统一实体渲染、聊天交互和断线清理。

### Modified Capabilities

- `visual-verification`: 保持全部旧场景顺序，在 `oak-grove` 后追加唯一末场景 `ai-companion`，并只新增一张人工确认的 golden。
- `bounded-benchmark-workload`: 因固定 Avatar、NameTag 与 Hotbar HUD 上传布局变化升级 scenario v16，并把当前唯一迁移改为 `15:16`。
- `hardware-performance-baselines`: 保持 M2 v15/M5 v14 基线不变，以 v16 Memory/TCP 报告作为 record-only 证据并保留 v6..v15 历史可读性。

## Impact

- 新增 `internal/companion`；修改 `internal/config`、`network`、`storage`、`sim`、`server`、`client`、`render`、`internal/archcheck`、`cmd/mornlea`、`cmd/mornlea-server` 与 `cmd/perfcheck`。
- 配置 schema 保持 v1；只识别 `ai.companions[].id/name`，后续 AI 字段仍按未知字段纪律告警并忽略。
- 伙伴存档独立于玩家 schema v6、区块 schema v8 和世界 metadata v2；配置清空时不读取或改写已有 `companions.ai`。
- Memory 与 TCP 复用相同服务端权威语义；伙伴状态由 tick 唯一写入，磁盘 I/O 在有界 worker 中执行，不阻塞 tick。
- 不新增第三方依赖，不加入二进制版权材质；TCP 仍只面向可信局域网且不增加认证或加密承诺。
