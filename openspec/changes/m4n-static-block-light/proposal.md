## Why

M4M 只派生天空光，`Quad.Light` 低四位仍固定为零，导致午夜与封闭空间没有可放置的静态光源。M4N 需要在不改变服务端权威边界的前提下补齐最小、可持久化且可验证的方块光闭环。

## What Changes

- 新增稳定的完整发光块和对应物品；物品可堆叠、普通整格放置，并可按石砖同档用石镐或铁镐挖回，但没有配方、初始发放、世界生成或管理命令等正常获取入口。
- 客户端从已接受的权威方块镜像确定性派生静态方块光；与天空光共用固定 scratch，高四位保存天空光、低四位保存方块光，并在既有有界 mesher 路径内收敛。
- terrain shader 按天空光与方块光的最大值合光，使方块光不受昼夜变化影响。
- **BREAKING**：线上协议从 v13 升为 v14，区块 schema 从 v6 升为 v7；packet 布局不变，v6→v7 为 no-op 迁移，玩家 schema 保持 v5，世界 metadata 保持 v2。
- benchmark workload 升为 scenario v15；当前 M5 基线使用完整 Memory v15 报告，TCP v15 独立记录，唯一显式迁移为 `14:15`，M2 v6 基线不变。
- 无窗口视觉基线末尾新增 `block-light-room`，验证午夜封闭房间内的衰减与边界不漏光。
- 非目标：真实火把模型；透明、半透明、彩色、可调或动态光；燃烧熔炉发光；新增正常获取入口；服务端计算、存储或传输光照状态；任何新 packet。

## Capabilities

### New Capabilities

- `static-block-light`: 发光块资源、客户端派生静态方块光、有界收敛、packed shader 合光以及版本兼容行为。

### Modified Capabilities

- `authoritative-daylight`: 升级协议和区块 schema，并把 packed 光照定义为高四位天空光、低四位方块光。
- `authoritative-inventory`: 升级背包协议版本，并把发光块物品纳入有效完整物品与普通整格放置，同时保持六条固定配方。
- `authoritative-mining`: 为发光块固定无正确镐、石镐和铁镐的时长与掉落规则。
- `bounded-benchmark-workload`: 把当前 workload 升为 v15，并把唯一显式迁移改为 `14:15`。
- `hardware-performance-baselines`: 用完整 M5 Memory v15 报告提升当前基线，并独立记录 TCP v15。
- `visual-verification`: 在末尾新增无窗口 `block-light-room` 场景及其收敛和漏光门禁。

## Impact

- 资源与权威玩法：`internal/core`、`internal/assets`、`internal/sim`、`internal/server`。
- 客户端派生与呈现：`internal/mesh`、`internal/client`、`internal/render`、`cmd/mcgo`。
- 兼容与持久化：`internal/network`、`internal/storage`；协议 v14 拒绝旧客户端，区块 v6 读取后按原 payload 语义迁移为 v7，降级必须停服并恢复升级前备份。
- 性能与文档：`cmd/perfcheck`、`docs/notes`、README、LAN 文档及项目现状说明；固定扫描和有界 BFS 改变 mesher workload，但不增加服务端 tick 工作、goroutine、新依赖、网络消息或磁盘光照数组。
