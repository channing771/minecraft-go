## Context

见 `proposal.md`。现有天空已经在单个 fullscreen draw 中从 `render.Camera` 和 `DayNightAt` 上传的 sky uniform 绘制渐变、星空、月亮和太阳；该 pass 不写深度，terrain 随后使用既有深度测试覆盖天空。云只需要消费同一帧的相机世界位置和已确认世界时间。

数据所有权仍在客户端呈现侧：权威服务端只提供既有世界时间，`render` 不向 `sim`、`world` 或 `network` 新增依赖。uniform 在调用渲染器的 goroutine 中写入，shader 读取后即随命令提交，不新增跨 goroutine 可变状态。

## Goals / Non-Goals

**Goals:**

- 用唯一的固定算法生成有世界坐标视差的方块云。
- 保留现有天空的单 pass、单 draw、资源上限和地形覆盖顺序。

**Non-Goals:**

- 不提供云高度、形态、速度、颜色或覆盖率的调节配置。
- 不改变天体方向、星图、权威时间、存档、协议或视觉阈值，也不接受非天空内容漂移。

## Decisions

### 固定云平面和唯一覆盖算法

云平面固定为 Y=`192`，cell 固定为 `16` block，时间偏移固定为 `worldTime / 80`，因此随世界时间每 `80` tick 向东移动 `1` block。为保持完整 `uint64` 时间范围的连续性，上传值拆为 `0..63` block 的局部 `f32` 偏移和按生产 `u32` hash 输入自然取模的 macro X 偏移；局部值仍在 `camera_cloud.w`，macro 偏移复用 `star_visibility` 后的现有 padding word。每个可见像素只在相机位于平面下方且视线能求出稳定正交交点时计算；接近地平线以固定 fade 淡出。

shader 必须使用下列唯一算法；不提供调节配置或替代噪声：

```text
intersection = camera.xz + direction.xz * ((192-camera.y)/direction.y)
localCell     = floor(vec2(intersection.x-localOffset, intersection.y) / 16)
macro         = floor(localCell / 4)
hashMacroX    = bitcast<u32>(macro.x) - macroOffset
active        = hash(hashMacroX, bitcast<u32>(macro.y), 0) & 3 != 0
center        = (1 + bit2(hash), 1 + bit3(hash))
filled        = active && manhattan(localCell-macro*4, center) <= 1
```

`hash` 复用 `sky.wgsl` 现有 `hash_cell`；负 `vec2i` 分量通过 `bitcast<u32>` 进入哈希，且仅 macro X 在 hash 前扣除 macro 偏移，保证 4×4 macro 内 cell 坐标不受影响。该选择保留负世界坐标的确定性且不增加函数、资源或 CPU 工作。被否决的替代方案是纹理噪声、CPU 网格和可配参数：它们会增加资源、热路径工作或不必要的维护面。

为使该二维调用可直接复现，`hash(macro)` 固定表示为 `hash_cell(vec3u(bitcast<u32>(macro.x), bitcast<u32>(macro.y), 0u))`。回归固定使用以原点为中心的宏格矩形 `macro.x, macro.y ∈ [-8, 7]`，即含正负坐标的 `16×16=256` 个 macro；按当前固定 `hash_cell` 其低两位 `0/1/2/3` 的计数必须为 `72/69/62/53`，所以 `hash & 3 != 0` 的活动 macro 为 `184` 个。每个活动 macro 固定填充五个 cell，故该样本在 `256×16=4096` 个 cell 中必须填充 `920` 个，实际覆盖率为 `920/4096=22.4609375%`；这与理论 `15/64=23.4375%` 同属规定的 `20%..30%` 区间。实现测试必须直接执行嵌入的生产 WGSL `hash_cell` 与十字形并 readback 断言矩形、四个低两位计数、活动数、填充数和覆盖率，避免 Go 镜像与事后选择样本；改变生产 hash、输入或掩码时这些断言必须失败。

### 复用既有 uniform 与天空合成顺序

sky uniform 只追加 shader 必需的相机世界坐标、局部云时间偏移和复用 padding 的 macro X 偏移，仍由既有 `render.Camera` 和 `DayNightAt` 填充；总长保持 `112` bytes，不建立第二个 uniform 或 draw。颜色在现有星/月/日绘制后以固定 alpha `0.82` 和地平线 fade 混合，使云遮挡天体；terrain pass 的深度覆盖顺序不变。

被否决的替代方案是在天体之前合成云或另开云 pass：前者不能满足遮挡，后者会增加 draw 和资源状态。

## Risks / Trade-offs

- [近平行视线导致交点数值不稳定] → 无正交交点时不绘制，并以固定地平线 fade 抑制接近地平线的放大。
- [负坐标哈希平台不一致] → 对每个负 `vec2i` 分量明确使用 `bitcast<u32>`，并以无窗口像素测试覆盖。
- [uniform 变更破坏布局或稳定帧成本] → 保留单一固定大小 uniform，测试其字节布局、一次上传、一次 draw 和零 Go 堆分配。
- [云错误覆盖地形或不遮挡天体] → 在无窗口渲染测试中同时验证云/天体/terrain 的覆盖关系。

## Migration Plan

发布不需要协议或存档迁移：云只消费既有权威世界时间与本地相机，不改变 metadata、玩家或区块 schema，也不读写持久化数据。storage、oak、light 均合入后的最终 `main` 必须统一刷新受天空影响的 golden 并逐张人工确认；视觉阈值保持不变，任何非天空内容漂移均不可接受。若出现渲染回归，回退 sky uniform/shader 的同一提交即可恢复原天空，既有世界不受影响。

验证包括 `internal/render` 的无窗口像素、uniform、draw 顺序和零分配测试，以及 `go test ./... -race`、`go vet ./...`、`gofmt -l .`、架构检查和 OpenSpec 严格校验；不得启动或聚焦前台窗口。
