## Why

Mornlea 当前只有程序化 16×16 材质，玩家无法在不改源码的情况下替换客户端视觉。远环 LOD 已合入并固定了新的视觉基线，现在可以在不改变材质语义与权威状态的前提下，引入可许可分发的默认材质与最小本地覆盖能力。

## What Changes

- 图形客户端内嵌经适配的 Pixel Perfection 子集作为默认材质；没有对应上游素材的 layer 继续使用现有程序化材质。
- 新增启动时读取的 Mornlea v1 目录材质包，用户可以按逻辑 layer 名逐层覆盖；缺失 layer 依次回退到内嵌默认与程序化基线。
- 配置 v1 新增可选顶层字段 `texturePackPath`，相对路径按配置文件所在目录解析；benchmark 与 capture 忽略本地用户值。
- 显式配置的材质包无效时，客户端在创建窗口、打开存储或建立网络连接之前启动失败。
- 发布物携带 Pixel Perfection 的不可变来源 pin、逐文件 provenance、署名、修改说明与 CC BY-SA 4.0 许可证。
- 专用服务端不加载或分发材质；协议 v23、存档 schema、engine ABI v6、client ABI v7 与 benchmark scenario v18 均不变化。

## Capabilities

### New Capabilities

- `texture-pack-loading`: 定义有界、启动时、逐层回退且不允许材质重映射的目录材质包加载行为。

### Modified Capabilities

- `tunable-constants`: 配置 v1 接受可选的客户端本地 `texturePackPath`，并定义路径解析及自动化隔离行为。
- `voxel-visual-presentation`: 客户端产品默认视觉改为内嵌材质子集，同时保留程序化最终回退与既有几何、UV、透明分类及上传契约。
- `visual-verification`: capture golden 固定使用内嵌默认材质，并保留远环场景顺序、现有比较阈值与 LOD 近环保护。

## Impact

影响 `internal/assets`、`internal/config`、`cmd/mornlea` 客户端启动路径、视觉 golden、发布打包与材质包文档。实现只使用 Go 标准库；不会改变服务端权威模拟、网络/存储契约、Rust ABI、shader、材质编号或运行时并发模型，正常帧循环也不新增文件 I/O。
