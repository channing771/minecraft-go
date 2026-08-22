# Mornlea Bedrock 风格生存 HUD 设计

日期：2026-08-22
状态：已书面确认，待实施
前置：`mornlea-texture-pack` 完成 GPU visual update、终审与归档后再创建实现 change

## 背景

Mornlea 已有同一 HUD pass 内的九格快捷栏、权威生命值、权威氧气、工具耐久与权威采掘进度，但当前视觉由独立的深色矩形、左下角爱心和横向氧气条组成，信息层级不统一。下一里程碑先统一生存 HUD，不把背包、合成、箱子和熔炉同时卷入；这些容器界面在后续 change 复用本次建立的视觉语言。

本设计参考 Minecraft Bedrock 的现代桌面 HUD 布局与层级，但形成 Mornlea 自己的颜色、边框和像素图标。仓库不得复制、描摹或分发 Mojang 的 PNG、字体、截图切片或其他版权资源。

## 目标

- 用统一的原创像素语言呈现快捷栏、选中格、数量、耐久、生命、氧气和采掘进度。
- 把生存状态围绕底部居中的快捷栏排布：生命在左上，氧气在右上。
- 保持现有服务端权威、客户端只读呈现、固定容量和零每帧动态资源契约。
- 复用现有 Go HUD atlas、quad/glyph stream 和 Rust HUD pass，不改变 ABI 或上传格式。
- 在桌面键鼠布局下保留底部两角的响应式空间，方便未来单独设计触屏控件。

## 非目标

- 不在本 change 重做背包、合成、箱子或熔炉面板；只保证其当前打开状态不溢出或遮挡。
- 不修改准星、目标方块反馈、聊天、伙伴任务消息或调试面板。
- 不新增饥饿、护甲、经验、快捷栏名称弹窗等玩法或信息。
- 不实现触屏按钮、运行时主题、资源包 UI skin、连续 `uiScale` 或缩放预设。
- 不新增 PNG UI atlas、第三方依赖、Rust shader、GPU pass、协议、存档、配置或 benchmark 版本。

## 用户可见设计

### 快捷栏

九格快捷栏继续固定在 framebuffer 底部中央。视觉改为一块原创的深色半透明外框，内部有清晰槽位分隔和轻微内阴影。选中格同时使用高对比描边和小幅外扩，不能只靠颜色表达；数字键、滚轮、选中顺序、物品数量与权威确认逻辑完全不变。

物品继续采样当前有效材质 registry 的顶面；不可放置物品继续使用既有程序化色块。数量仍在右下角显示带阴影的最多两位数字。耐久条仍位于对应工具格内部，只在耐久未满且物品具有耐久上限时显示。

### 生命值

十颗心锚定到快捷栏左边沿上方，从左向右排列。每颗仍代表两点生命，支持空、半、满三种原创 16×16 像素图标。生命值只使用已确认的权威镜像；未确认时不显示，零血、奇数生命与满血均有确定布局。

### 氧气

氧气锚定到快捷栏右边沿上方，使用十个原创气泡槽位而不是横条。满氧时整个氧气元素不占用 quad；氧气未满时绘制十个空槽，再按 `ceil(oxygen * 10 / MaxOxygenTicks)` 覆盖相应数量的满气泡，因此正氧气至少显示一个满气泡，零氧显示零个。气泡从左向右填充、从右向左耗尽，呈现只读取已确认权威值。

### 采掘进度

采掘进度位于生命/氧气状态行上方并相对快捷栏居中，使用固定宽度的深色轨道和比例填充，因此不会与左右状态图标重叠。可采目标保留绿色语义并带亮色末端标记；不可采目标保留琥珀色语义并带固定警示缺口，确保两者不只靠颜色区分。进度、所需 tick 与可采状态仍完全来自权威采掘 overlay。

### 响应式布局

标准桌面宽度下，生命与氧气分别占据快捷栏上方左右区域，中间留出视觉间隔。现有自动 `hudScale` 扩展为以整个生存 HUD 边界计算适配比例；宽度不足时整体等比缩小，不重排元素。普通桌面尺寸不放大超过设计尺寸，底部左右角不放置新元素，也不创建隐藏触摸命中区。

零尺寸 framebuffer 继续产生空布局。背包或容器打开时，本 change 不改其格子、面板或命中几何；生命与耗损氧气改为放在快捷栏下方的既有底部留白内，分别对齐快捷栏左右边沿，使它们继续可见且不覆盖当前可交互格子。完整容器视觉在下一 change 重排。

## 实现架构

### 所有权与依赖

`internal/render/hud` 继续独占 HUD 的视觉 token、atlas 像素、布局、固定容量与编码。`cmd/mornlea` 只把既有权威镜像和 framebuffer 尺寸交给 `HotbarRenderer.Prepare`。Rust `mornlea_client` 继续消费相同的 viewport/quad/glyph 字节流并执行现有 HUD pass。

数据流保持：

```text
权威消息 → client mirror/predictor confirmed state
         → HotbarRenderer.Prepare
         → 固定容量 layout
         → 现有 quad/glyph 编码
         → Rust HUD pass
```

服务端、协议、存档和世界模拟不依赖任何 HUD 类型。

### Atlas 与视觉 token

在现有 `buildHotbarTextureAtlas` 中用 Go 代码生成空/半/满心与空/满气泡，随后仍按稳定 `ItemID` 列追加物品纹理。所有 UI 图标保持 16×16、二值 alpha 和确定字节；不新增二进制美术文件或运行时文件读取。

调色板、外框、阴影、槽位、选中描边、间距和安全边距保持为 `hud` 包内固定常量。它们是本次单一视觉产品的 token，不抽象成主题接口或配置对象。

### 布局与固定容量

继续用 `hotbarLayout` 的预分配 `quads`/`glyphs`。快捷栏的嵌套边框、十颗空心加十颗覆盖心、十个空气泡加最多十个覆盖气泡、耐久条、采掘轨道与两种非颜色标记都计入显式最坏情况公式。容量测试必须构造合法最大组合并证明实例数不超过固定上限；不得通过运行时扩容掩盖估算错误。

现有 item UV、HUD 实例 48-byte 编码、viewport 布局、Rust FFI 与 shader 均保持不变。

## 预计修改范围

- `internal/render/hud/atlas.go`：原创心/气泡 atlas cells 与稳定 UV。
- `internal/render/hud/layout.go`：快捷栏框架、选中格、耐久、采掘进度、共享 token 和响应式边界。
- `internal/render/hud/health.go`：快捷栏左上锚点与空/半/满心。
- `internal/render/hud/oxygen.go`：快捷栏右上锚点与十段气泡。
- 对应 `internal/render/hud/*_test.go`：atlas、布局、权威状态、容量与分辨率门禁。
- `cmd/mornlea/capture*.go` 与 capture tests：保留 `hud-hotbar-health`，新增 `hud-survival-feedback` 场景。
- `cmd/mornlea/testdata/golden/`：只更新受影响 HUD 场景并加入新场景。

预计不修改 `engine/`、`internal/network`、`internal/sim`、`internal/storage` 或 `cmd/mornlea-server`。

## 测试与验收

### 单元门禁

- atlas 中五类新图标尺寸正确、二值 alpha、UV 不跨 cell，构建结果逐字节确定。
- 生命值覆盖未确认、0、奇数、满值及越界钳制；每种值对应准确的空/半/满心数量。
- 氧气覆盖未确认、满值隐藏、0、1 tick、分段边界及接近满值；气泡数量遵守固定公式。
- 快捷栏覆盖九格、选中外扩、数量阴影、耐久显示条件与 registry 物品采样。
- 采掘进度覆盖 inactive、零 required、0%、中间值、100%、可采和不可采的颜色与形状差异。
- 布局覆盖 1280×720、640×360、窄窗口、极宽窗口、零尺寸及容器打开；所有实例保持屏内且不覆盖既有可交互格。
- 最大合法 HUD 状态恰当见证固定 quad/glyph 上限，编码长度和 Rust 上传格式不变。

### 视觉门禁

- 更新 `hud-hotbar-health`，证明正常九格、选中格、数量和生命布局。
- 新增 `hud-survival-feedback`，同一帧固定展示低血、耗氧、磨损工具和不可采目标的中段采掘进度；可采样式由单元测试锁定。
- 既有其他场景若显示生存 HUD，其底部 HUD 像素会同步变化；逐图复核时只接受由本次 HUD 改版解释的区域，世界、实体和光照区域不得无因变化。
- 新场景排在普通 HUD 场景附近；既有 `far-horizon` 必须继续倒数第二，`water-underwater` 必须继续最后。
- 运行完整 offscreen `visual-check`，不调整像素/感知阈值，不启动前台窗口。

### 工程门禁

执行 focused race、`internal/archcheck`、`make rust`、`make rust-check`、`go test ./... -race`、`go vet ./...`、`gofmt -l .` 与 OpenSpec strict validation。视觉数值变化只更新接受过的 HUD golden，不改变 benchmark 或性能门禁退出语义。

## OpenSpec 与执行顺序

当前 `mornlea-texture-pack` 仍等待可用 Metal 环境完成 14 张 golden 更新、逐图验收与归档。两个变更都会修改 capture 场景和 golden，因此本设计不得在该 change 上并行实现。

前置 change 归档后，从最新 `main` 创建独立 `bedrock-survival-hud` change：

- 新增 `survival-hud-presentation` capability，描述快捷栏、生命、氧气、耐久、采掘进度和响应式布局的可观察契约。
- 修改 `visual-verification` capability，加入新 HUD 场景并保留末尾场景顺序和现有阈值。
- 既有 `authoritative-hotbar`、`authoritative-health`、`fluid-survival` 与 `tool-durability` 的权威事实不变；只有出现措辞冲突时才以完整 MODIFIED requirement 对账，不能为样式重写权威规则。

实现按以下里程碑顺序拆分：

1. OpenSpec、视觉 token 与 atlas 图标测试。
2. 快捷栏框架、选中格、数量和耐久。
3. 生命、氧气与采掘进度布局。
4. 响应式/固定容量/编码回归测试。
5. capture 场景、golden、全量验证与独立终审。

容器 UI 另建后续 change，复用本次已经稳定的视觉 token，不在本计划预留接口或空实现。

## 风险与回退

- 固定容量估算遗漏会造成上传区越界：用合法最大组合测试和编码长度门禁阻止。
- 分数缩放可能导致像素边缘不均：布局使用现有 nearest-sampled atlas，并把最终矩形边界钳到 framebuffer；不为极小窗口引入第二套布局。
- 新 HUD 可能与容器界面重叠：本 change 只允许不改变命中几何的最小避让，完整容器重排留给后续 change。
- 视觉参考可能滑向版权素材复制：只保留抽象布局和层级，所有图标由 Mornlea 代码原创生成。
- 若验收失败，整支 revert 即恢复现有 HUD；没有数据、协议、存档或配置迁移。

## 已否决方案

- 内嵌原创 PNG atlas：当前五类小图标用代码生成更易审计，不值得增加二进制资源与缩放维护。
- Rust shader 绘制 UI：会把纯表达改动扩散到 GPU 端和 ABI 邻域，现有 quad/atlas 已足够。
- 一次同时重做 HUD 与全部容器：评审面和 golden 冲突过大，无法形成可独立回退的最小闭环。
- 提前实现触屏控件或 UI scale 配置：当前没有触屏输入闭环，新增配置也不是本轮视觉一致性的必要条件。
