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

loader 只校验 manifest、读取上限、PNG 可解码性与尺寸，不按逻辑 layer 检查像素结构、alpha 值或许可证。程序化 registry 与内嵌默认包由仓库测试保证稳定结构；其中树叶/玻璃基础层保持 binary alpha，内嵌资产另受来源与许可证门禁约束。用户 override 可以提供任意合法 16×16 RGBA（包括中间 alpha），但只能替换像素，不能改变 render classification、几何、layer ID、逻辑名映射或面映射。

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

上游 `https://github.com/minetest-texture-packs/Pixel-Perfection` 的发布 source pin 固定为 `7935d064fc6f993d1b5038ed5ec17a615600cf0a`。shell DNS 无法 clone 时，允许 GitHub 官方 connector 以该完整 ref 逐文件读取并记录 Git blob SHA；所有读取、哈希与 provenance 都必须引用该不可变 commit，不得省略 ref 或改用浮动分支、搜索结果、发布 ZIP、第三方镜像或 Minecraft 发行内容。

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
| `leaves` | `default/default_leaves.png` |
| `glass` | `default/default_glass.png` |
| `cobblestone` | `default/default_cobble.png` |
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

`coal_ore`、`iron_ore`、`light_block`、`roof_tile`、`water`、`smooth_stone` 与 `chest` 保持程序化：前五项的直接对应需要合成、动画抽帧或非等价替代；固定 commit 中不存在原计划的 `default/default_stone_block.png`，且没有其他直接等价的 smooth stone 素材；`default/default_chest_front.png` 原图是 14×14，不能在禁止缩放、padding 与合成的 16×16 契约下直接使用。任何上游路径变化都必须先更新设计并重新批准，不能临时改用近似素材。

资产目录必须包含 `pack.json`、完整 `LICENSE.txt`、`ATTRIBUTION.md` 与逐文件 `PROVENANCE.json`。provenance 为每个目标 PNG 记录上游路径、固定 commit、vendored bytes 的 SHA-256 与“只选择并重命名、无像素变换”的修改说明；`snow_top` 与 `snow_side` 分别记录。署名包含 Hugh “XSSheep” Rutland、上游列出的贡献者、仓库链接、commit、CC BY-SA 4.0 链接和 Mornlea 修改说明。源码内资产及客户端发布物携带相同许可证、署名和 provenance；第三方资产许可证不扩张为 Mornlea 代码许可证。专用服务端发布单元不携带这些客户端资产。

备选的转换脚本或通用 importer 在只有一次固定导入时增加维护面，故暂不创建；若第二次升级上游再基于实际重复步骤评估。

### 5. 配置与启动副作用顺序

配置 v1 新增可选顶层原文 `texturePackPath` 和不参与序列化的解析后路径。空值禁用覆盖；绝对路径清理后使用；相对路径以实际配置文件目录为基准解析为绝对路径，保存时只写原文。它不进入数值字段、调试面板或权威参数。

客户端在依赖默认值补齐后立即构造材质注册表，顺序必须早于创建 context 后的存储、host、网络、窗口或 offscreen renderer 副作用。失败直接带配置路径上下文返回。正常本地模式与远程连接模式均使用客户端本地解析路径；benchmark/capture 继续由既有默认配置隔离规则得到空路径，capture 使用内嵌默认。专用服务端可以解析通用配置，但不打开路径、不依赖客户端资产。

### 6. 呈现与视觉门禁不迁移

只替换像素，不改变世界坐标 UV、cutout 分类、水与植物几何、atlas/mip 形状、material ID 或 Rust 上传 ABI。`far-horizon` 保持场景表倒数第二，`water-underwater` 保持唯一末场景；更新默认材质 golden 时保留所有现有双阈值。benchmark 报告结构与 scenario v18 不变。

现有 `nearBandGuard` 在 update 模式下把新帧的受保护行与旧 golden 逐字节比较，且旧图不存在时跳过。新默认材质会有意改变近环颜色，因此旧图比较无法区分“像素来源改变”与“LOD 误伤近环”；移动 golden 目录又会令 guard 根本不执行。材质更新改用同一当前 registry 的 LOD on/off 成对 control：

1. `--update-golden` 路径创建 disposable LOD-on control application，再以相同内嵌默认 registry、世界种子、固定场景与 render 配置创建 disposable LOD-off control application；两者只有 `LodEnabled` 不同；
2. 在写任何 golden 前，两端只渲染同一个 `far-horizon` scene，并保留两张诊断图；
3. 用 disposable LOD-on control 的相机与壳半径构造现有 `nearBandGuard`，调用同一个 `assertUnchanged` 比较 LOD-off 与 LOD-on 当前帧的顶部/底部受保护行；
4. 无论 control 成功、第二个 application 构造失败还是 guard 失败，都关闭每个已经构造的 control application；失败时整次更新返回错误且不覆盖任何 golden；
5. 只有 guard 通过且两个 control application 均关闭后，才构造第三个 fresh LOD-on application，并只把该 application 交给正式 `runCapture` 按普通完整场景顺序生成 baseline；正式 application 未执行过 control scene，并在成功、构造后的 capture 失败或正常完成路径关闭。

该比较直接验证 LOD 开关不改变同一材质下的近环，且不依赖历史像素或旧文件存在性。实现只把 `assertUnchanged` 的调用从逐场景写盘分支前移到整次更新的 preflight，不删除或放宽几何行带、fail-closed 规则与逐像素比较。三个 application 只在显式 baseline update 中按上述受控生命周期存在；普通 capture 与游戏仍只构造一个 application。

被否决的替代方案：移走全部或单张旧 golden 会触发现有 `os.IsNotExist` 跳过分支；复用已跑过 `far-horizon` control 的 LOD-on application 会污染依赖 fresh 初态的正式场景顺序；先把整套场景缓存到内存再统一写盘会增加无必要的图像缓存与写盘事务；只靠人工看图不可自动阻止近环回归；继续比较旧 RGB 会永久阻止任何有意的默认材质变化。

## Risks / Trade-offs

- [第三方上游内容或许可记录不完整] → 入库前核验固定 commit、完整许可证、逐文件哈希和无额外 PNG 的测试，发布时字节复制 notices。
- [损坏用户包导致客户端不可启动] → 这是显式配置的可诊断策略；清空 `texturePackPath` 即可恢复内嵌默认。
- [新默认像素暴露 cutout、水、HUD 或远环回归] → 先保留七层程序化回退，golden 写盘前运行同 registry 的 LOD on/off 近环 control，再更新并人工检查材料、HUD、农业、水下和远环场景。
- [配置路径带来运行时 I/O] → 只在启动时有界读取，帧循环不保留文件系统访问或并发状态。

## Migration Plan

1. 在合并后的 LOD 基线上先实现并验证有界 loader 与配置字段。
2. 从固定上游 commit 直接复制批准子集，加入许可证、署名与 provenance，再内嵌为产品默认。
3. 在所有客户端启动模式接线，并验证无效配置早于外部副作用失败、专用服务端依赖闭包不含资产。
4. 更新文档与客户端发布 notices；先以同一内嵌默认 registry 的 LOD on/off `far-horizon` control 通过近环保护，再更新受影响 golden，并保持 `far-horizon`/`water-underwater` 顺序与阈值。
5. 运行 Rust、全量 Go、构建、视觉及 OpenSpec 门禁后再申请归档。

回退时整支 revert 即恢复程序化产品默认；用户也可清空 `texturePackPath` 回到内嵌默认，或删除单个用户 PNG 让该 layer 回退。本变更不需要协议、存档、配置 schema、ABI 或 benchmark 数据迁移。
