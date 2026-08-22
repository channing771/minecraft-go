## Context

当前 `internal/assets` 生成完整、确定性的 39 层 16×16 RGBA 程序化注册表，并沿固定 layer 顺序生成 atlas/mip 后交给 Rust `mornlea_client`。方块面映射、透明类别、水与植物几何、远环 material ID 采样以及 atlas 上传形状均已被既有 Go/Rust 契约锁定。合并后的基线为协议 v23、engine ABI v6、client ABI v7、benchmark scenario v18；本变更不推进这些版本。

本变更跨 `internal/assets`、`internal/config`、`cmd/mornlea`、发布打包与 capture golden，但材质只属于客户端表达平面；专用服务端、权威模拟、网络消息和存档没有材质所有权。

## Goals / Non-Goals

**Goals:**

- 在现有程序化注册表上应用一个内嵌 Pixel Perfection 适配子集，再应用可选用户目录，保持完整逐层回退。
- 对 manifest、文件大小、图像尺寸与解码结果设固定边界，并保证用户包全有或全无地生效。
- 在任何窗口、存储、host 或网络副作用之前完成材质加载。
- 固定上游来源并随源码和客户端发布物满足 CC BY-SA 4.0 的署名、许可证、修改标记与 ShareAlike 义务。

**Non-Goals:**

- 不兼容 Minetest/Luanti、Minecraft Java 或 Bedrock 的目录与 manifest。
- 不支持 ZIP、网络下载、包列表 UI、热重载、动画、PBR、多分辨率或缩放。
- 不允许包定义映射、重排 layer、改变面映射、透明类别、材质 ID、shader 或 Rust 入口。
- 不为一次性上游导入新增下载器、转换框架、文件系统接口、缓存或 goroutine。

## Decisions

### 1. 复用程序化注册表并按三层固定顺序覆盖

`internal/assets` 继续拥有逻辑 layer、面映射、透明分类与 mip。产品构造路径严格执行：

1. 构造现有完整程序化注册表；
2. 从仓库拥有的内嵌文件系统应用 Pixel Perfection 子集；
3. `texturePackPath` 非空时，从该本地目录应用用户包；
4. 复用现有 atlas、mip、HUD 缩略图和 Rust 上传路径。

loader 接受标准库文件系统抽象，不新增自有接口。正常帧循环不保存可变 loader 状态，也不再访问文件。内嵌包无效是仓库构建缺陷并由测试阻止；用户包错误返回启动错误。

备选的“直接解析 Minetest 包”会把 mod 命名与覆盖规则引入产品；“兼容 Minecraft Java 包”还需要版本、动画和复杂映射，并增加误用未授权资产的风险，因此拒绝。运行时格式只表达 Mornlea 当前固定 layer 的像素替换。

### 2. v1 目录格式与固定边界

目录根必须含 `pack.json`，可选材质位于 `textures/<logical-layer>.png`。manifest 最大 `4 KiB`，必须是有效 UTF-8 JSON，且包含整数 `format: 1` 与 trim 后非空、UTF-8 长度不超过 `128 bytes` 的 `name`。未知 manifest 字段告警后忽略。

每张 PNG 最大 `64 KiB`，必须恰好 `16×16` 像素。加载先在有界读取内检查 PNG 尺寸，再完整解码并规范化为 `16×16×4` 的 8-bit RGBA。文件名大小写敏感。loader 不遍历目录，只按固定逻辑名表尝试文件，因此未知文件不会被打开。

固定逻辑名为：

```text
stone dirt grass_top grass_side bedrock stone_brick coal_ore iron_ore
furnace iron_block chest light_block leaves glass cobblestone smooth_stone
sand gravel oak_log_side oak_log_top oak_planks brick white_wool roof_tile
clay snow_top snow_side mossy_cobblestone water farmland_dry farmland_wet
wheat_0 wheat_1 wheat_2 wheat_3 wheat_4 wheat_5 wheat_6 wheat_7
```

manifest 只描述格式与人类可读名称；它不提供材质映射。未来新增 layer 时追加一个可选逻辑名，旧包自然回退；只有目录或字段语义发生不兼容变化时才提升格式版本。

### 3. 缺失回退，存在但无效则原子失败

已知 PNG 不存在是正常的逐层回退，不告警；manifest 不存在或无效、已知 PNG 存在但不可读、非普通文件、超限、损坏或尺寸错误均使整个包失败，错误携带包名与逻辑名上下文。显式配置失败绝不静默回退。

所有待替换 layer 先解码到临时固定集合；只有整个包验证成功后才修改目标注册表，防止坏文件留下半应用状态。v1 的大小与格式边界用于可靠性，不承诺把本地目录当作抵御恶意 symlink 的安全沙箱。

备选的“遇错保留单层旧值”会让拼写和损坏伪装成成功配置，也无法保证可复现启动，因此拒绝。

### 4. Pixel Perfection 只做不可变来源的直接复制映射

入库前必须把上游 `https://github.com/minetest-texture-packs/Pixel-Perfection` 的 `master` 解析为一个 40 位完整 commit SHA，之后所有读取、哈希与 provenance 都引用该不可变 commit，不引用浮动分支、搜索结果、发布 ZIP 或 Minecraft 发行内容。实际 SHA 在 Task 3 获取并写入资产元数据后成为发布 source pin。

允许的直接复制/重命名映射如下，不做缩放、重色、合成或动画帧抽取：

| Mornlea 逻辑名 | 固定 commit 内的上游路径 |
|---|---|
| `stone` | `default/default_stone.png` |
| `dirt` | `default/default_dirt.png` |
| `grass_top` | `default/default_grass.png` |
| `grass_side` | `default/default_grass_side.png` |
| `bedrock` | `bedrock/bedrock.png` |
| `stone_brick` | `default/default_stone_brick.png` |
| `furnace` | `default/default_furnace_front.png` |
| `iron_block` | `default/default_steel_block.png` |
| `chest` | `default/default_chest_front.png` |
| `leaves` | `default/default_leaves.png` |
| `glass` | `default/default_glass.png` |
| `cobblestone` | `default/default_cobble.png` |
| `smooth_stone` | `default/default_stone_block.png` |
| `sand` | `default/default_sand.png` |
| `gravel` | `default/default_gravel.png` |
| `oak_log_side` | `default/default_tree.png` |
| `oak_log_top` | `default/default_tree_top.png` |
| `oak_planks` | `default/default_wood.png` |
| `brick` | `default/default_brick.png` |
| `white_wool` | `wool/wool_white.png` |
| `clay` | `default/default_clay.png` |
| `snow_top` | `default/default_snow.png` |
| `snow_side` | `default/default_snow.png` |
| `mossy_cobblestone` | `default/default_mossycobble.png` |
| `farmland_dry` | `farming/farming_soil.png` |
| `farmland_wet` | `farming/farming_soil_wet.png` |
| `wheat_0` … `wheat_7` | `farming/farming_wheat_1.png` … `farming/farming_wheat_8.png` |

`coal_ore`、`iron_ore`、`light_block`、`roof_tile` 与 `water` 保持程序化，因为直接对应需要合成、动画抽帧或非等价替代。任何上游路径变化都必须先更新设计并重新批准，不能临时改用近似素材。

资产目录必须包含 `pack.json`、完整 `LICENSE.txt`、`ATTRIBUTION.md` 与逐文件 `PROVENANCE.json`。provenance 为每个目标 PNG 记录上游路径、固定 commit、vendored bytes 的 SHA-256 与“只选择并重命名、无像素变换”的修改说明；`snow_top` 与 `snow_side` 分别记录。署名包含 Hugh “XSSheep” Rutland、上游列出的贡献者、仓库链接、commit、CC BY-SA 4.0 链接和 Mornlea 修改说明。源码内资产及客户端发布物携带相同许可证、署名和 provenance；第三方资产许可证不扩张为 Mornlea 代码许可证。专用服务端发布单元不携带这些客户端资产。

备选的转换脚本或通用 importer 在只有一次固定导入时增加维护面，故暂不创建；若第二次升级上游再基于实际重复步骤评估。

### 5. 配置与启动副作用顺序

配置 v1 新增可选顶层原文 `texturePackPath` 和不参与序列化的解析后路径。空值禁用覆盖；绝对路径清理后使用；相对路径以实际配置文件目录为基准解析为绝对路径，保存时只写原文。它不进入数值字段、调试面板或权威参数。

客户端在依赖默认值补齐后立即构造材质注册表，顺序必须早于创建 context 后的存储、host、网络、窗口或 offscreen renderer 副作用。失败直接带配置路径上下文返回。正常本地模式与远程连接模式均使用客户端本地解析路径；benchmark/capture 继续由既有默认配置隔离规则得到空路径，capture 使用内嵌默认。专用服务端可以解析通用配置，但不打开路径、不依赖客户端资产。

### 6. 呈现与视觉门禁不迁移

只替换像素，不改变世界坐标 UV、cutout 分类、水与植物几何、atlas/mip 形状、material ID 或 Rust 上传 ABI。`far-horizon` 保持场景表倒数第二，`water-underwater` 保持唯一末场景；更新默认材质 golden 时保留所有现有双阈值和 LOD 近环 golden guard。benchmark 报告结构与 scenario v18 不变。

## Risks / Trade-offs

- [第三方上游内容或许可记录不完整] → 入库前核验固定 commit、完整许可证、逐文件哈希和无额外 PNG 的测试，发布时字节复制 notices。
- [损坏用户包导致客户端不可启动] → 这是显式配置的可诊断策略；清空 `texturePackPath` 即可恢复内嵌默认。
- [新默认像素暴露 cutout、水、HUD 或远环回归] → 先保留五层程序化回退，再更新全部受影响 golden，并人工检查材料、HUD、农业、水下和远环场景。
- [配置路径带来运行时 I/O] → 只在启动时有界读取，帧循环不保留文件系统访问或并发状态。

## Migration Plan

1. 在合并后的 LOD 基线上先实现并验证有界 loader 与配置字段。
2. 从固定上游 commit 直接复制批准子集，加入许可证、署名与 provenance，再内嵌为产品默认。
3. 在所有客户端启动模式接线，并验证无效配置早于外部副作用失败、专用服务端依赖闭包不含资产。
4. 更新文档、客户端发布 notices 与受影响 golden；保持 `far-horizon`/`water-underwater` 顺序、阈值和近环保护。
5. 运行 Rust、全量 Go、构建、视觉及 OpenSpec 门禁后再申请归档。

回退时整支 revert 即恢复程序化产品默认；用户也可清空 `texturePackPath` 回到内嵌默认，或删除单个用户 PNG 让该 layer 回退。本变更不需要协议、存档、配置 schema、ABI 或 benchmark 数据迁移。
