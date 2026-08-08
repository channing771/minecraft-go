## Context

见 [proposal.md](proposal.md) 的动机。现有客户端以权威方块镜像、固定 Mesher worker、不可变 `3×3×3` 邻域快照和 revision stamp 生成 `Quad.Light`；天空光高四位目前只有直射 `15` 与遮挡 `0`。M4M 不改变服务端权威状态或渲染 shader 管线。

## Goals / Non-Goals

**Goals:**

- 在一次网格任务中从固定邻域输入派生连续的 `0..15` 天空光，并在方块、加载和遗忘变化后有界地异步收敛。
- 用正常镜像和渲染链路无窗口验证洞口到深处的亮度梯度。
- 以 scenario v14 与唯一 `13:14` 迁移保持性能数据可解释，并通过 M5 正式链更新基线。

**Non-Goals:**

- 不引入方块光、火把、透明方块、服务端光照、长期缓存、新 worker pool、goroutine 或第三方依赖。
- 不改变协议 v13、玩家 schema v5、区块 schema v6、metadata v2、shader、pipeline、bind group 或既有门禁阈值。

## Decisions

### 派生数据只属于一次 Mesher job

`client.Mirror` 继续保存只读权威区块镜像；`internal/mesh` 从不可变 `3×3×3` 快照派生光并写入最终 `Quad.Light`。`internal/world` 仅扩展邻域采样范围，不反向依赖 `mesh`、`client` 或渲染包。传播数组不进入 `world.Section`、服务端、协议或存档。

固定输入域为每轴 `[-16,31]`，即 `48³ = 110592` 单元；它覆盖中心区段可见面、亮度 `1` 的最远传播距离和零亮度封闭边界。普通任务在固定顺序内以多源 BFS 填充直射 `15` 和递减 `1` 的侧向光；不透明、缺失和域外输入均为阻断。替代的 shader 近似会穿墙，二值泛光无法表达 `0..15`，均不采用。

### scratch 与并发边界固定

每个既有 Mesher worker 创建并复用一份仅含亮度数组和 `uint32` 队列的 scratch；队列容量精确为 `48³`，任务开始时原地清空。任务显式接收 scratch，避免包级缓存与逐任务大对象分配；其数量只受既有 worker 数限制。队列溢出是内部不变式破坏，应由既有 panic 隔离拒绝任务并继续服务，不能静默截断。

替代的长期光照缓存需要新的所有权、代次和内存上限；独立 light worker pool 重复既有 worker 的限流、关闭和隔离生命周期，均不采用。

### 失效范围复用现有 stamp 与调度

普通方块变化按每轴扩展 `16` 格，最多影响 `3×3×3 = 27` 个已加载区段。列顶变化按新旧列顶跨度上下各扩展 `16` 格、水平扩展 `16` 格并裁剪世界高度，最多影响 `3×3×24 = 216` 个唯一区段。加载和遗忘沿用现有自身加已加载水平邻区的标脏规则。九份 ChunkStamp 覆盖任务全部可变输入；stamp 不匹配的结果被拒绝并重新排队。

### 视觉与性能沿用现有门禁

`skylight-tunnel` 追加在 capture 清单末尾，经 `Mirror.Apply(network.ChunkSnapshot)` 装入固定 `3×3` 夹具，并在固定正午、相机与正常 dirty/Schedule/Drain/upload 收敛后抓取。预热未收敛即报错，不写 golden；既有阈值不变。

传播改变 workload，故 producer 升至 scenario v14。`perfcheck` 唯一接受 `13:14`，该跨 workload 迁移只跑完整性、硬件一致性和绝对门禁；同 v14 才做相对回归。M5 必须在候选提交与静稳预检后，经授权在新路径各执行一次 Memory/TCP；Memory 成功后才替换 M5。M2 不动。

## Risks / Trade-offs

- [固定 `48³` BFS 增加每个网格任务的 CPU 与约 540 KiB scratch] → worker 数和 scratch 容量固定，微基准与现有 backlog、场景绝对门禁验证。
- [未知邻区暂时变暗] → 加载标脏和 stamp 重算收敛，不在 worker 等待网络。
- [不同亮度可减少贪心合并] → scenario v14 隔离 workload，并禁止跨 scenario 相对比较。
- [视觉基线可能因合法传播改变] → 只由 M5 无窗口抓帧生成，并人工复核全部四张场景图。

## Migration Plan

1. 先实现与测试邻域、传播 scratch、网格接入及有界失效；不迁移存档或线上数据。
2. 完成无窗口 `skylight-tunnel` 与现有场景人工复核；保留既有视觉阈值。
3. 将 producer 升至 scenario v14，保留 v6–v13 可读性，仅允许 `13:14`。
4. 所有门禁通过后，对精确候选完成静稳预检并获得一次性授权，以全新路径执行 M5 Memory/TCP 正式链；成功时替换 M5，失败时冻结证据。
5. 回退时移除本 change 的生产实现并继续使用现有 v13/M5 基线；协议 v13、玩家 schema v5、区块 schema v6 和 metadata v2 无迁移，因此没有数据回滚步骤。
