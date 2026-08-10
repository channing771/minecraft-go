# 权威受伤红屏反馈设计

## 1. 背景

当前 M4M 已有服务端权威生命值、摔落伤害、回复、死亡与重生，客户端也只用
`Predictor.Health()` 的确认值绘制生命 HUD。但生命值下降除了爱心数量变化外没有即时反馈，
玩家在移动中很难察觉刚刚受伤。

本变更增加一个很小的客户端呈现闭环：确认生命值下降时，世界画面边缘短暂变红并淡出。
它与正在隔离工作树中执行的 M4N 常见块状材料任务并行；两者不共享生产代码文件。

## 2. 目标与非目标

### 2.1 目标

- 只在客户端收到的权威生命值下降时显示反馈。
- 反馈立即出现、短暂淡出，并在连续受伤时重新开始。
- HUD 和调试面板始终保持清晰可读。
- 非激活路径不提交额外 render pass、不产生每帧分配。
- 不改变协议、存档、模拟、权限边界或性能契约。
- 作为独立 OpenSpec change 评审、验证和回退。

### 2.2 非目标

- 不预测伤害，也不根据本地碰撞直接触发反馈。
- 不增加音效、镜头晃动、粒子、死亡界面或重生等待。
- 不增加配置项、颜色主题或通用屏幕特效框架。
- 不修改生命 HUD、terrain shader、材质 atlas、mesher 或视觉抓帧 golden。
- 不增加新的伤害来源。

## 3. 用户可观察行为

### 3.1 触发规则

客户端每个正常帧在处理完服务端消息后读取一次 `Predictor.Health()`：

- Predictor 首次进入 ready 时，只建立生命值基线，不显示反馈。
- 已有基线且新确认值小于旧值时，反馈立即回到峰值。
- 生命值不变或增加时不触发新反馈；已经存在的反馈继续正常淡出。
- Predictor 未就绪或客户端会话被清理时，生命值基线和反馈立即清零。
- 因此自动回复与满血重生不会误触发，断线后的旧反馈也不会泄漏到新会话。

### 3.2 时序与外观

- 固定持续时间为 `180ms`，不提供配置项。
- 新伤害发生的当前帧显示峰值；从下一帧开始按剩余时间线性淡出。
- `elapsed <= 0` 不衰减；`elapsed` 大于等于剩余时间时直接归零。
- shader 输出颜色固定为线性空间 `vec3f(0.65, 0.0, 0.0)`，峰值透明度为 `0.30`。
- fragment shader 令 `edgeDistance` 为归一化 UV 到最近屏幕边缘的距离，并计算
  `edgeFactor = 1 - smoothstep(0, 0.35, edgeDistance)`；最终 alpha 为
  `0.30 * strength * edgeFactor`，因此边缘最强、中心严格透明。
- 连续受伤无论旧效果剩余多少，都重新开始完整的 `180ms`。

遮罩的绘制顺序固定为：

1. terrain、天空、远端玩家、掉落物与 name tag；
2. 受伤遮罩；
3. 快捷栏、背包、生命值与容器 HUD；
4. 调试面板。

这样反馈会覆盖世界画面，但不会染红或遮挡需要读取的 HUD 文本与图标。

## 4. 架构与数据所有权

### 4.1 客户端呈现状态

新增 `cmd/mcgo/damage_feedback.go`，放置一个只在渲染 goroutine 使用的值类型。它持有：

- 是否已有权威生命值基线；
- 上一次确认生命值；
- 剩余显示时间。

该类型接收 `health`、`ready` 和本帧 `elapsed`，返回钳制到 `0..1` 的归一化强度。
更新顺序必须先处理 ready/reset，再比较新旧生命值；检测到新伤害时先恢复峰值，不在同一帧扣除
`elapsed`。其余帧才衰减旧效果。

状态不进入 `internal/client.Predictor`：Predictor 只负责权威镜像与移动预测，受伤红屏是
`cmd/mcgo` 的本地呈现策略。它也不进入 `internal/render`：渲染器只消费调用方算好的强度，
不知道生命值语义。

### 4.2 独立渲染器

新增：

- `internal/render/damage_overlay.go`
- `internal/render/shader/damage_overlay.wgsl`
- 对应测试文件

`DamageOverlayRenderer` 只拥有固定的 uniform buffer、bind group 和 alpha-blend pipeline。
shader 使用一个全屏三角形，不需要顶点缓冲、深度缓冲或纹理。uniform 固定占用 16 字节，
renderer 复用预分配上传字节，不产生每帧堆分配。

强度为 `0` 时 `Render` 立即返回，不写 uniform、不创建 pass、不提交 draw。强度大于 `0` 时，
只写一次固定 uniform，开启一个保留现有颜色目标的透明 pass，并调用一次三顶点 draw。
`Release` 必须幂等。

不抽取通用 overlay、post-processing、effect manager 或计时 interface；当前只有一个固定效果，
这些抽象没有第二个消费者。

### 4.3 application 接线

`cmd/mcgo/app.go` 增加：

- 一个 `damageFeedback` 值；
- 一个 `*render.DamageOverlayRenderer`；
- renderer 构造依赖、创建、逆序清理和幂等释放；
- `frame` 在 drain 后、`renderFrame` 前更新反馈；
- 客户端会话清理路径主动 reset 反馈，不依赖后续帧；
- `renderFrame` 在 name tag 后、HUD 前调用 overlay renderer。

构造依赖沿用现有 renderer 注入方式，使无窗口测试能验证构造失败清理；不新增 factory 或
应用级接口。`frame` 已持有 `elapsed`，无需增加时钟依赖。

## 5. 并行与集成边界

M4N 计划修改 core、sim、network、world、storage、server、assets、mesh、terrain shader、
hotbar 和 `cmd/mcgo/capture.go`。本变更的生产代码只修改 `cmd/mcgo/app.go` 并新增独立文件，
不触碰上述 M4N 热点文件。

唯一预期的共享文档是 `README.md`：当前状态明确写着尚无伤害动画/反馈，实现后必须把该句更新为
“已有确认受伤红屏反馈，尚无音效或专门死亡界面”。M4N 收尾也会更新 README 的材料基线。
两边可独立开发和测试；合并时后合入的一方重放这一句即可，不存在行为取舍或生产代码冲突。

本变更不提升协议版本、玩家 schema、区块 schema、世界 metadata 或性能 scenario 版本，
也不单独宣称新的 M4 字母基线。

## 6. 错误处理与生命周期

- Predictor 不 ready 时使用 reset 路径，不保留旧基线或剩余时间。
- 超大或负 `elapsed` 不允许产生负强度、NaN 或延长效果。
- renderer 对输入强度执行最终 `0..1` 钳制；零尺寸 framebuffer 继续由 application 现有路径跳过。
- renderer 构造失败时，application 沿用现有逆序资源清理并保留错误上下文。
- GPU 资源释放保持幂等；不增加 goroutine、channel、锁或异步 I/O。

## 7. 测试与验证

### 7.1 状态机测试

`cmd/mcgo/damage_feedback_test.go` 至少覆盖：

- 首次 ready 只建立基线；
- 确认生命值下降立即输出峰值；
- 不变与恢复不触发新效果；
- 连续下降重置完整持续时间；
- `elapsed` 为零、负值、部分持续时间和超过剩余时间；
- not-ready 清除后再次 ready 仍只建立新基线。

### 7.2 renderer 测试

`internal/render/damage_overlay_test.go` 复用现有 fake gfx 模式，验证：

- pipeline 使用 alpha blend、无 depth，只有一个 16 字节 uniform；
- 零强度没有 uniform 写入、render pass 或 draw；
- 非零强度被钳制后只写一次并恰好 draw 三个顶点；
- `Release` 调用两次时每个资源只释放一次；
- hidden-path benchmark 为零分配并记录耗时。

另用已有 headless gfx 测试方式渲染到小尺寸离屏纹理，抽样断言边缘红色增量明显高于中心，
且强度为零时不改变底图。只有“无可用 adapter”可跳过；其他设备或 shader 错误必须失败。
该测试不启动、聚焦或操作前台窗口。

### 7.3 application 集成测试

在 `cmd/mcgo` 通过既有 fake device/predictor 路径验证：

- 只有确认生命值下降会把非零强度交给 renderer；
- 首次状态、回复和断线不会误触发；
- overlay draw 位于 name tag 后、HUD 与调试面板前；
- overlay 构造失败时已创建资源会被释放。

### 7.4 验证命令

```bash
go test ./cmd/mcgo ./internal/render -race -count=1
go test ./internal/archcheck -count=1
go test ./internal/render -run '^$' -bench '^BenchmarkDamageOverlayHidden$' -benchmem -count=5
go test ./... -race
go vet ./...
gofmt -l .
openspec validate --all --strict --no-interactive
```

`gofmt -l .` 必须无输出。benchmark 数值只记录，不改变退出状态；真实错误、资源泄漏和
测试失败仍是门禁。

## 8. 实施拆分

实现计划保持两个生产代码任务：

1. **确认生命值反馈状态机**：先写失败测试，再实现最小值类型与 application 状态更新。
2. **独立 overlay renderer 与接线**：先写 fake/headless/integration 失败测试，再实现 renderer、
   shader、application 生命周期、绘制顺序和 README 更新。

编码前先建立完整 OpenSpec change，冻结 proposal、行为规格、设计与上述两组 tasks；实现完成后
严格验证、同步主规格并归档。每组任务独立提交，最后提交只包含规格归档和文档同步。

## 9. 被否决方案

### 9.1 让生命爱心闪烁

代码更少，但必须修改 M4N 正在修改的 `internal/render/hotbar.go`，并且快速移动时不如屏幕边缘
反馈明显，因此不适合作为当前并行任务。

### 9.2 通用 post-processing/effect 系统

当前没有第二个效果需要排序、组合或配置。新增 manager、interface、effect 列表或第二张中间纹理
只会扩大代码、显存和测试面，按 YAGNI 拒绝。

### 9.3 客户端预测受伤

本地根据落差或碰撞提前触发会在服务端拒绝、回滚或网络抖动时产生假反馈，破坏“服务端唯一权威”
边界，因此只消费确认生命值。

### 9.4 修改共享 capture golden

M4N 正在扩展同一抓帧场景文件和 golden。独立 headless 像素测试已经能判定 shader 的边缘渐变，
本变更不为一次短暂效果制造共享文件冲突；以后若建立统一时序化特效抓帧，再单独增加场景。

## 10. 回退

回退只需移除新增状态、renderer、shader、application 接线和 README 一句，不涉及协议协商、存档
迁移或世界数据恢复。旧客户端、旧服务端和已有世界始终保持兼容。
