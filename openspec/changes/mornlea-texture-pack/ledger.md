# mornlea-texture-pack 执行台账

| task | implementer | spec review | quality review | iteration | ruling |
|---|---|---|---|---:|---|
| 1 | `/root/texture_task1_openspec` | PASS/CLEAN（round 4） | PASS/CLEAN（round 4） | 4 | 五项 P1 均关闭：用完整 MODIFIED 对账 voxel/HUD 与 visual-verification 主规格；用户 RGBA 与默认资产义务分域；golden 更新用两个 disposable control 加一个 fresh 正式 application，写盘前实际执行 near-band guard。 |
| 2 | `/root/texture_task2_loader` | PASS/CLEAN（round 2） | PASS/CLEAN（round 2） | 2 | 关闭 1×P1：只有 `root.Open` 阶段的 NotExist 可按缺失 layer 回退；成功打开后的 Stat/Read 错误一律带 pack/layer 上下文原子失败。 |
| 3 | `/root/texture_task3_embed` | PASS/CLEAN（round 1） | PASS/CLEAN（round 1） | 1 | 独立复核固定 commit、31 个唯一源 Blob 与 32 个目的 PNG 逐字节一致，许可证、署名、provenance、二值 alpha 与构造器语义全部通过；`smooth_stone`、`chest` 保持程序回退，`leaves` 使用已裁决的 `default_leaves_simple.png`。 |
| 4 | `/root/texture_task4_config` | PASS/CLEAN（round 2） | PASS/CLEAN（round 2） | 2 | 关闭 1×P1：`texturePackPath` 经 nullable string 解码，JSON null 与其他非字符串均带字段上下文拒绝；真实空字符串继续禁用覆盖。 |
| 5 | `/root/texture_task5_startup` | PASS/CLEAN（round 2） | PASS/CLEAN（round 2） | 2 | 关闭 1×P2：副作用顺序测试改为本地交互、远程连接与无头 benchmark 三行表驱动，并用临时顺序 mutation 证明分别能抓到 `openStore`、`dialTCP` 与 `newOffscreenRenderer` 的提前调用。 |
| 6 | `/root/texture_task6_docs_ci` | PASS/CLEAN（round 1） | PASS/CLEAN（round 1） | 1 | `CARGO ?= rustup run 1.97.1 cargo` 修复精简 PATH 下的固定工具链入口并保留显式覆盖；客户端 build 只复制三份 byte-identical notice，39 名文档与运行时映射一致，Linux 专服依赖/发布单元保持 asset-free。 |
| 7 | pending | pending | pending | 0 | pending |
| 8 | pending | pending | pending | 0 | pending |
