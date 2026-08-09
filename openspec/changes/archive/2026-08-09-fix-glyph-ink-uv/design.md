# 字形墨迹 UV 修正设计

## 1. 根因

`internal/render/font_atlas.go` 中两处对"字形占多大"的理解不一致：

- `opentypeGlyphRasterizer.Rasterize` 把墨迹画在 32×32 格内 `(glyphInkPadding, glyphInkPadding)` 处，返回的 `Glyph.Width/Height` 取自 `face.GlyphBounds` 的**墨迹包围盒**。
- `FlushUploads` 分配 slot 时把 UV 设为 `[x, x+glyphCellSize) × [y, y+glyphCellSize)`，即**整格**。

三个消费方（`hotbar.go`、`name_tag.go`、`debug_panel.go`）的写法一致：四边形取 `Width × Height`，UV 直接取 `Glyph` 的四个分量。于是采样区（32×32）被映射到四边形（墨迹尺寸），缩放比 `Width/32`。

缩放比与字宽成正比，因此缺陷表现为**窄字符成片丢失、宽字符只是偏细**——这与"字体没加载"或"图集未收敛"的表现完全不同，后两者会让所有字符同等受影响。收敛帧数从 32 提到 240 后画面逐像素一致，已排除异步收敛因素。

## 2. 方案

让 UV 只覆盖墨迹子矩形：

```
inkX = x + glyphInkPadding
inkY = y + glyphInkPadding
U0, V0 = inkX / atlasSize,              inkY / atlasSize
U1, V1 = (inkX + Width) / atlasSize,    (inkY + Height) / atlasSize
```

`glyphInkPadding` 由 `Rasterize` 内的局部常量提升为包级常量，光栅化定位与 UV 定位共用同一个值——两处若不一致，采样会整体偏移。

**被否决的替代方案**：把四边形改为整格尺寸（32×32）并相应调整 bearing。它同样能得到 1:1 映射，但会改变 `Glyph.Width/Height` 的语义，而 `name_tag.go:260` 用这两个字段计算名牌背景框的包围盒——改语义会连带把名牌背景撑大。修 UV 的方案让全部消费方代码保持不变。

## 3. 行距

`panelRowHeight` 原值 18 是在缺陷状态下调的：那时字被压扁，看起来比实际小得多。修正后字形按 24pt 原生尺寸绘制，18px 会让相邻行重叠。

字体在 24pt 下的标称行距为 35px（Ascent 28 + Descent 7），那是保证任何字形都不重叠的上界；但面板有 32 行，35px 会让总高达到约 1150px，1080p 屏放不下。实测面板实际用到的字形墨迹跨度不超过 25px（CJK 约 24，拉丁升部到降部合计约 25），故取 28px：留 3px 行间隙，总高约 920px，仍可容于 1080p。

## 4. 抓帧路径构造面板

`debug-panel` 场景要拍的就是面板本身，而基线重生成与 CI 调用 capture 时都不会带 `--dev`。因此抓帧路径无条件构造面板渲染器，benchmark 路径仍然不构造——后者的产出要与性能基线比对，面板不应占用 GPU 资源或让结果随旗标变化。

面板默认隐藏，只有 `debug-panel` 场景的 `Apply` 打开它；`Prepare(visible=false)` 是清空布局后立即返回的零分配路径，因此其余场景的画面与基线不受影响。

## 5. 基线影响与验证

重新录制 `hud-hotbar-health`、`avatar-nametag`、`inventory-crafting` 三张，新增 `debug-panel` 一张，均逐张人眼确认。`terrain-noon` 不含文本，其重录结果与原基线的差异（7/230400 像素、最大通道差 1）纯属采样噪声且在阈值内，因此保留原基线不动，使本次基线变更只包含真正受影响的图。

回归保障是 `TestGlyphUVCoversInkNotWholeCell`：它断言每个字形的 UV 覆盖区与四边形在 1px 以内等尺寸。探针字符刻意混合最窄与较宽字形——只取宽字形时偏差会落在容差内而漏掉缺陷。

## 6. 已知局限

抓帧分辨率固定为 640×360，而面板 32 行约 920px 高，因此 `debug-panel` 基线只覆盖顶部读数区与 `physics` 分组的前几行。`sim`/`render` 两个分组的段头、以及 `render.viewDistance` 那条始终只读的行不在覆盖内。缺陷类别（字形丢失、行距重叠、标签与数值列冲突、只读与可编辑的颜色对比）在已覆盖区域内均可暴露。
