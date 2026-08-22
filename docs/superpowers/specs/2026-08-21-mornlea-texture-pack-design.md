# Mornlea 材质包与 Pixel Perfection 默认包设计

日期：2026-08-21

## 背景

Mornlea 当前由 `internal/assets` 在 Go 中确定性生成 16×16 RGBA 材质层，随后
构建固定 layer 顺序的 mip 字节流并交给 Rust `mornlea_client` 上传。方块到材质
层的面映射、植物层连续区间、水层分流等语义已经被 Go/Rust 测试锁定；材质包只应
替换像素，不能取得这些语义的所有权。

本轮接入 Mornlea 原生最小材质包格式，并把
[Pixel Perfection](https://github.com/minetest-texture-packs/Pixel-Perfection)
的可对应子集适配为内置默认包。上游是面向 Minetest/Luanti 的 16px 包，不直接
支持其目录作为运行时输入；适配发生在入库时，运行时只认识 Mornlea 格式。上游
声明 CC BY-SA 4.0，分发时必须遵守署名、许可证链接、修改标记与相同方式共享要求。

`authoritative-farming` 已归档，当前材质层包含干湿耕地和八个连续小麦阶段。
`rust-engine-lod-shell` 已归档并合入 main，当前基线为协议 v23、engine ABI v6、
client ABI v7、benchmark scenario v18。本材质变更直接建立在该 LOD 基线上，且不
推进这些契约版本。

## 目标

- 定义一个离线、目录式、启动时加载的 Mornlea 材质包 v1。
- 发布包内置 Pixel Perfection 适配子集，并把它设为产品默认视觉。
- 允许本地用户包逐层覆盖；缺失层逐层回退，不要求用户复制完整包。
- 保持现有 layer 编号、方块面映射、atlas/mip 形状和 Rust 上传接口不变。
- 把第三方资产来源、修改与许可证随仓库和发布物完整分发。

## 非目标

- 不直接兼容 Minetest、Minecraft Java 或 Minecraft Bedrock 资源包目录。
- 不支持 ZIP、网络下载、资源包列表 UI、热重载、动画、PBR 或多分辨率。
- 不允许材质包重排 layer、改变方块面映射或新增材质类别。
- 不改协议、存档、世界生成、服务端模拟、Rust ABI 或 shader。
- 不为一次性导入新增下载器或通用转换框架；第二次需要升级上游时再评估脚本。

## 方案选择

采用 Mornlea 原生最小格式，而不是直接解析 Minetest 或 Minecraft Java 布局。

直接支持 Minetest 的优点是能原样使用本次上游，但会把 Minetest mod 名称、
`override.txt` 和游戏依赖规则带进 Mornlea，且不能自然表达 Mornlea 自有材质。
直接支持 Minecraft Java 的生态更大，但需要 `pack.mcmeta`、版本差异、动画与大量
名称映射，同时扩大接触 Mojang 资产的误用风险。Mornlea v1 只需要替换现有 39 个
逻辑层；固定文件名是最小且稳定的边界。

## 架构与所有权

`internal/assets` 继续拥有材质 layer、方块面映射、透明类别与 mip 生成。新增的
loader 只把 16×16 图像规范化为现有 RGBA 字节，并覆盖 `Registry.layers`；它不读取
方块 ID，不修改 `mesh.RegistrySnapshot`，也不向 Rust 暴露新数据。

现有 `assets.NewRegistry()` 保持为程序化基线构造器，继续服务 mesh、光照和
oracle 测试。产品入口新增默认构造路径：

1. 调用 `NewRegistry()` 得到完整程序化基线；
2. 从 `embed.FS` 应用内置 Pixel Perfection 子集；
3. `texturePackPath` 非空时，从 `os.DirFS` 应用用户目录；
4. 沿用现有 `AtlasPixels()` 与 mip 生成，把同一份字节交给 Rust renderer 和 HUD。

loader 接收标准库 `fs.FS`，不定义自有文件系统接口。内置包损坏属于构建不变量，
由测试阻止入库；用户包错误作为普通启动错误返回。正常帧循环中不再读取文件，
因此不增加渲染热路径 I/O、分配或并发状态。

远环 LOD 必须继续按现有 material ID 采样同一个 atlas。由于本变更不改 material
编号和 Rust 入口，LOD 不需要适配代码；实现排在 LOD 后只为避免 renderer/capture
文件和 golden 的并行冲突。

## 材质包 v1 格式

目录必须包含：

```text
pack.json
textures/<logical-layer>.png
```

`pack.json` 最大 4 KiB，形状为：

```json
{
  "format": 1,
  "name": "Pixel Perfection for Mornlea"
}
```

`format` 和 `name` 必填；`format` 只能是整数 1，`name` 必须是非空 UTF-8 文本且
不超过 128 bytes。未知 manifest 字段告警后忽略，为同一格式的小幅元数据扩展保留
余地；不支持的 `format` 直接拒绝。

每张材质文件最大 64 KiB。loader 先读取 PNG header，必须得到恰好 16×16 像素，
随后解码并规范化为 8-bit RGBA；上游文件可以使用 PNG 自身支持的 RGB、RGBA 或
索引色编码，进入 atlas 后的结果始终是 RGBA。文件名大小写敏感。

当前稳定逻辑名为：

```text
stone dirt grass_top grass_side bedrock stone_brick coal_ore iron_ore
furnace iron_block chest light_block leaves glass cobblestone smooth_stone
sand gravel oak_log_side oak_log_top oak_planks brick white_wool roof_tile
clay snow_top snow_side mossy_cobblestone water farmland_dry farmland_wet
wheat_0 wheat_1 wheat_2 wheat_3 wheat_4 wheat_5 wheat_6 wheat_7
```

这些名字只映射现有 layer，不携带编号。未来新增 layer 时追加一个可选逻辑名；旧包
自然回退，不需要提升 `format`。只有目录结构或现有字段语义发生不兼容变化时才提升
格式版本。

loader 不遍历目录，只尝试已知文件名。因此未知文件天然忽略，避免把任意目录内容
读入内存，也允许较新包在较旧客户端上使用。

## 覆盖与错误语义

覆盖顺序固定为程序化基线、内置默认包、用户包，后者只替换实际存在且有效的层。

- 已知 PNG 不存在：保留下一层结果，不告警；这是部分材质包的正常用法。
- PNG 存在但读取失败、超限、损坏或尺寸错误：启动失败，错误包含包名与逻辑文件名。
- `pack.json` 不存在、损坏、超限或版本不支持：启动失败。
- 未配置用户包：只使用内置默认包，不访问用户文件系统。
- 内置默认包没有明确的一对一上游素材：保留程序化层；不得用 Mojang 资产补洞。

显式配置的包出错时不能静默退回内置包，否则拼写错误和损坏会伪装成“配置已生效”。
本地目录是用户主动指定的可信输入；v1 做大小和格式边界，不承诺把 `os.DirFS` 当作
抵御本机恶意 symlink 的安全沙箱。

材质 alpha 不改变渲染分类：水仍走 water pass，树叶/玻璃/小麦仍走既有 cutout
规则，其他层仍走 terrain pass。默认适配资产必须通过水透明、cutout 可见性与 HUD
可辨识度测试；用户包可以自行选择像素 alpha 效果。

## 配置与启动

`config.Config` 新增可选顶层字符串 `texturePackPath`，编译默认值为空。它不进入
数值调试面板，也不由权威服务端消费。配置文件版本保持 v1：旧文件缺字段时得到默认
值，新程序保存后可带出新字段，旧程序按既有未知字段纪律忽略它。

绝对路径原样使用；相对路径按实际配置文件所在目录解析。内置默认配置不存在时，
空路径仍能直接启动。图形客户端在创建 `assets.Registry` 时加载用户包；专用服务端
即使解析到该字段也不打开材质目录、不引入图形依赖。TCP 客户端使用自己的本地包，
服务器不下发、校验或持久化视觉资产。

benchmark 与 capture 已经强制使用 `config.Defaults()`，不得读取本机配置，因此两者
固定使用内置默认包。benchmark 的报告结构和 scenario v18 不变；capture 视觉基线
在 LOD 合入后统一重录。

## 默认资产与许可证

内置子集放在 `internal/assets/packs/pixel_perfection/` 并由 `go:embed` 编入客户端。
导入前必须把上游 `master` 解析为不可变的完整 commit SHA；provenance 对每个入库 PNG
记录上游路径、commit、原文件 SHA-256、Mornlea 逻辑名与像素修改说明。运行时和构建
均不访问网络，也不跟随浮动分支。

资产目录同时提交：

- `pack.json`；
- `ATTRIBUTION.md`：作者 Hugh “XSSheep” Rutland、上游列出的贡献者、来源链接、
  commit、CC BY-SA 4.0 链接和 Mornlea 修改说明；
- `LICENSE.txt`：CC BY-SA 4.0 完整文本；
- `PROVENANCE.json`：逐文件来源与哈希。

入库的 Pixel Perfection 原图及其适配版本按 CC BY-SA 4.0 分发；Mornlea 源代码继续
使用仓库现有许可证，文件边界明确，不把第三方资产许可证错误扩写成代码许可证。
发布产物必须携带 attribution、许可证文本和 provenance。用户本地包不由 Mornlea
再分发，loader 不强制其声明许可证。

## 测试与验收

### `internal/assets`

- 锁定程序化 → 内置 → 用户三层优先级和逐层缺失回退；
- 拒绝缺 manifest、错误格式版本、超限文件、损坏 PNG 与非 16×16 图像；
- 断言每个内置 PNG 与 provenance 哈希一致，manifest/署名/许可证文件齐全；
- 锁定默认 registry 的 layer 数、atlas 总长度、mip 确定性和植物连续区间不变；
- 保留现有程序化测试，只新增针对产品默认构造路径的测试。

### `internal/config` 与 `cmd/mornlea`

- 覆盖字段缺失默认、Save/Load 往返、类型错误与相对路径解析；
- 断言客户端启动使用内置默认包，显式用户目录只覆盖已提供层；
- 断言 benchmark/capture 不读取本机 `texturePackPath`；
- 断言专用服务端不会打开或验证材质目录。

### 视觉与全量门禁

LOD 合入后更新 `materials-showcase`、农业、水下与其他受默认材质影响的 capture
golden；视觉评审重点是世界 UV 连续、far LOD 与近环使用同图、玻璃/树叶/小麦
cutout、水透明和 HUD 物品图标。最终运行 `make rust`、受影响 Go 包 race、
`go test ./... -race`、`go vet ./...`、`gofmt -l .`、archcheck 与
`openspec validate --all --strict --no-interactive`。

默认材质会有意改变近环 RGB，不能再把新图与旧程序化 golden 的受保护行直接
比较，也不能通过移走旧 golden 触发缺失分支跳过门禁。材质 baseline 更新必须在
写入任何 golden 前，用同一生效 registry、种子、相机与场景分别抓取 LOD on/off
的 `far-horizon`；两次运行只允许 `lodEnabled` 不同，并复用既有几何行带和
`nearBandGuard.assertUnchanged` 对两张当前帧执行逐像素近环比较。受保护行不同则
整次更新失败且旧 golden 全部不变；只有 control 通过后才写入并人工复核新默认图。
自动测试必须覆盖成对 application 的唯一配置差异、guard 在旧 golden 缺失时仍
执行、近环差异先于任何写盘失败，以及纯远景带差异允许继续。

## OpenSpec 与执行顺序

这是跨 `internal/assets`、`internal/config`、客户端启动、第三方资产和视觉 golden 的
新能力，必须建立 active OpenSpec change。顺序固定为：

1. 确认 `rust-engine-lod-shell` 已归档并合入协议 v23、engine ABI v6、client ABI v7、
   benchmark scenario v18 的当前 main；
2. 从最新 main 创建 `mornlea-texture-pack` OpenSpec change，把本设计转为 proposal、
   行为 spec、design 与可验证 tasks；
3. 先交付 loader/config 与纯测试，再导入带完整 provenance 的默认资产；
4. 最后接线产品入口并统一更新 capture golden；
5. 完成全量门禁、独立任务评审和整分支终审后归档 OpenSpec。

若 LOD 最终放弃或长期暂停，可以从当前 authoritative-farming 基线实现，但必须先由
用户明确裁决，并接受未来 LOD 合并时再次处理 capture 冲突的成本。

## 回退

用户清空 `texturePackPath` 可立即回到内置默认包；删除某个用户 PNG 可让单层回退。
默认 Pixel Perfection 本身出现问题时，整支 revert 恢复程序化产品视觉，因为本变更
不迁移协议、存档、配置版本或 Rust ABI。保留 `NewRegistry()` 程序化构造器也让回退
后的测试 oracle 不依赖第三方二进制资产。
