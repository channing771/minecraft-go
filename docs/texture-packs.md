# Mornlea 材质包

Mornlea 图形客户端支持启动时从本地目录逐层覆盖方块材质。当前唯一支持的格式是 v1 目录：

```text
my-pack/
├── pack.json
└── textures/
    ├── stone.png
    └── wheat_7.png
```

`pack.json` 必须包含整数 `format: 1` 和非空名称：

```json
{
  "format": 1,
  "name": "My Pack"
}
```

名称会去除首尾空白，结果必须非空且不超过 128 个 UTF-8 字节。manifest 最大 4 KiB；未知字段会被忽略并记录警告。

## 材质文件

`textures/` 中的文件均可选。每个已提供的文件必须是恰好 16×16 像素、最大 64 KiB 的 PNG；客户端加载时会把 PNG 支持的颜色模型统一转换为 8-bit RGBA。文件名区分大小写。

v1 的完整稳定逻辑名如下，文件路径为 `textures/<逻辑名>.png`：

```text
stone
dirt
grass_top
grass_side
bedrock
stone_brick
coal_ore
iron_ore
furnace
iron_block
chest
light_block
leaves
glass
cobblestone
smooth_stone
sand
gravel
oak_log_side
oak_log_top
oak_planks
brick
white_wool
roof_tile
clay
snow_top
snow_side
mossy_cobblestone
water
farmland_dry
farmland_wet
wheat_0
wheat_1
wheat_2
wheat_3
wheat_4
wheat_5
wheat_6
wheat_7
```

缺失的已知文件先回退到客户端内嵌默认材质；内嵌包也未提供时，再回退到程序化材质。未知文件不会创建新材质或改变既有映射。

显式配置的目录、manifest 或已知材质文件不可读、无效、超限或尺寸错误时，客户端会直接启动失败，不会静默忽略整个用户包。清空 `texturePackPath` 可恢复内嵌默认与程序化回退。

## 配置与限制

在客户端 JSON 配置的顶层设置 `texturePackPath`：

```json
{
  "version": 1,
  "texturePackPath": "packs/my-pack"
}
```

相对路径以配置文件所在目录为基准；绝对路径直接使用。材质包只支持目录，且只在客户端启动时读取：运行中修改文件不会热重载。专用服务端不读取或分发材质包。

此格式不兼容 Minetest/Luanti 或 Minecraft Java/Bedrock 的 manifest 和目录。v1 不支持 ZIP、热重载、动画、PBR，也不允许材质或方块面的重映射。

## Pixel Perfection 许可与来源

内嵌默认材质使用经许可的 Pixel Perfection 子集。源码中的许可、署名与逐文件来源记录位于：

- `internal/assets/packs/pixel_perfection/LICENSE.txt`
- `internal/assets/packs/pixel_perfection/ATTRIBUTION.md`
- `internal/assets/packs/pixel_perfection/PROVENANCE.json`

macOS 客户端执行 `make build` 后，同一组文件会随发布输出复制到 `bin/third-party/pixel-perfection/`。专用 Linux 服务端发布单元不包含这些客户端 notices。
