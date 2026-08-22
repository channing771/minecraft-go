# mornlea-texture-pack 执行台账

| task | implementer | spec review | quality review | iteration | ruling |
|---|---|---|---|---:|---|
| 1 | `/root/texture_task1_openspec` | `/root/texture_task1_review`: PASS/CLEAN（round 4） | `/root/texture_task1_review`: PASS/CLEAN（round 4） | 4 | 五项 P1 均关闭：用完整 MODIFIED 对账 voxel/HUD 与 visual-verification 主规格；用户 RGBA 与默认资产义务分域；golden 更新用两个 disposable control 加一个 fresh 正式 application，写盘前实际执行 near-band guard。 |
| 2 | `/root/texture_task2_loader` | `/root/texture_task2_review`: PASS/CLEAN（round 2） | `/root/texture_task2_review`: PASS/CLEAN（round 2） | 2 | 关闭 1×P1：只有 `root.Open` 阶段的 NotExist 可按缺失 layer 回退；成功打开后的 Stat/Read 错误一律带 pack/layer 上下文原子失败。 |
| 3 | `/root/texture_task3_embed` | `/root/texture_task3_review`: PASS/CLEAN（round 1） | `/root/texture_task3_review`: PASS/CLEAN（round 1） | 1 | 独立复核固定 commit、31 个唯一源 Blob 与 32 个目的 PNG 逐字节一致，许可证、署名、provenance、二值 alpha 与构造器语义全部通过；`smooth_stone`、`chest` 保持程序回退，`leaves` 使用已裁决的 `default_leaves_simple.png`。 |
| 4 | `/root/texture_task4_config` | `/root/texture_task4_review`: PASS/CLEAN（round 2） | `/root/texture_task4_review`: PASS/CLEAN（round 2） | 2 | 关闭 1×P1：`texturePackPath` 经 nullable string 解码，JSON null 与其他非字符串均带字段上下文拒绝；真实空字符串继续禁用覆盖。 |
| 5 | `/root/texture_task5_startup` | `/root/texture_task5_review`: PASS/CLEAN（round 2） | `/root/texture_task5_review`: PASS/CLEAN（round 2） | 2 | 关闭 1×P2：副作用顺序测试改为本地交互、远程连接与无头 benchmark 三行表驱动，并用临时顺序 mutation 证明分别能抓到 `openStore`、`dialTCP` 与 `newOffscreenRenderer` 的提前调用。 |
| 6 | `/root/texture_task6_docs_ci` | `/root/texture_task6_review`: PASS/CLEAN（round 1） | `/root/texture_task6_review`: PASS/CLEAN（round 1） | 1 | `CARGO ?= rustup run 1.97.1 cargo` 修复精简 PATH 下的固定工具链入口并保留显式覆盖；客户端 build 只复制三份 byte-identical notice，39 名文档与运行时映射一致，Linux 专服依赖/发布单元保持 asset-free。 |
| 7 | `/root/texture_task7_visual`、`/root/texture_task7_visual_resume` | `/root/texture_task7_review`: PASS/CLEAN（round 3） | `/root/texture_task7_review`: PASS/CLEAN（round 3） | 3 | 关闭 1×P3 后，真实 GPU 恢复暴露双 control 顺序加载导致未消费方 `Receiver` 溢出；以同 goroutine 固定 on/off 交错 drain 的最小修复关闭根因，不扩 inbox、不并发 renderer、不改 guard/threshold/正式顺序。14 场景逐图接受，final visual-check 全部 0/230400 差异，完整 `cmd/mornlea -race` 通过。 |
| 8 | `/root/texture_task8_baseline` | `/root/texture_task8_review`: PASS/CLEAN（round 2）；Task 7 增量由 `/root/texture_task7_review`: PASS/CLEAN/visual PASS（round 3） | `/root/texture_task8_review`: PASS/CLEAN（round 2）；Task 7 增量由 `/root/texture_task7_review`: PASS/CLEAN（round 3） | 2 | 关闭 1×P2：Task 1–8 review 列补全 reviewer 身份并保留历史裁决。Task 8 round 2 覆盖完整非视觉分支，Task 7 round 3 独立覆盖后续背压修复、14 张 golden 与视觉证据；最终合并态 exact focused/full race、Rust、vet、build、notices、专服隔离、fresh 14 场景 visual-check 与 OpenSpec 全部通过。 |

## 2026-08-22 暂停点

- 分支 `codex/mornlea-texture-pack` 当前 HEAD 为 `f5742ed`，工作树 clean；Task 1–6 已完成双评审，Task 7 三 application preflight 与 Task 8 非视觉整分支终审均为 PASS/CLEAN。
- 唯一产品验收阻塞是当前沙箱没有 GPU adapter：14 张 tracked golden 尚未更新，7.1–7.7 与 8.1–8.7 保持未勾，change 不可归档。listener/httptest 的 exact full race 也须在允许本机 bind 的正常环境复跑。
- 恢复顺序固定为：在可用 Metal 环境运行 `make visual-update VISUAL_OUT=build/visual-texture-pack-update`，逐图验收 14 场景，再运行 final `visual-check` 与 exact full race，完成 Task 7/8 复审、勾选与归档前批准；不得删除旧 golden、放宽阈值或绕过 near-band control。

## 2026-08-22 GPU 恢复与 Task 7 修复裁决

- 宿主 Metal 已可用；补齐 GVM Go 路径后，真实 `visual-update` 越过 LOD-on control，但 LOD-off control 在加载阶段以 `client: server message consumer is too slow` 失败，14 张 tracked golden 仍未改变。
- Ruling: 两个 control application 已按规格同时构造，各自 Host 会立即发送约 4,489 个初始区块，而 `runGoldenUpdateControl` 顺序加载使等待侧容量 256 的 `Receiver` 必然溢出。修复应在同一 goroutine 交错推进两个 control 的既有加载与收敛状态，使两个 bounded consumer 持续被 drain；不得扩容 inbox、并发调用两个 renderer、放宽 guard/threshold 或改正式 capture 顺序。Cost if wrong: 交错加载若与单 application 收敛判据漂移，会让 control 抓取半成品或改变确定性；真实 `visual-update`、focused race 与 final `visual-check` 必须共同证明两侧收敛后才比较。

## 2026-08-22 Task 7 完成裁决

- Resume implementer: `/root/texture_task7_visual_resume`; commit `a4a265f`。回归测试先 RED，再证明已先收敛的一方仍持续被 drain；focused suite 与完整 `go test ./cmd/mornlea -race -count=1`（425.458s）通过。
- 视觉裁决：真实 `visual-update` 的 LOD on/off current-frame near-band control 通过，fresh formal application 更新全部 14 张 golden。逐图接受 `terrain-noon`、`hud-hotbar-health`、`avatar-nametag`、`inventory-crafting`、`debug-panel`、`skylight-tunnel`、`block-light-room`、`materials-showcase`、`target-block-feedback`、`oak-grove`、`ai-companion`、`water-surface-slope`、`far-horizon` 与 `water-underwater`；默认 atlas、cutout、HUD/背包、水透明排序、水下 tint 与 LOD 接缝均无异常。当前场景表没有独立农业图，农业材质由映射/资产测试覆盖。
- Iteration 3 reviewer: `/root/texture_task7_review`; spec PASS/CLEAN, quality PASS/CLEAN, visual evidence PASS; 0 findings。独立 focused race 与 fresh final visual-check 复核为 14/14 场景、每场 0/230400 差异。Task 7 complete。

## 2026-08-22 Task 8 完成裁决

- Controller 将 Task 7 的最终 bookkeeping 移交给 `/root/texture_task8_baseline` 纳入收口；7.1–7.7 的勾选与 round 3 PASS/CLEAN/visual PASS 记录已逐项核对，无丢失或改写视觉裁决。
- 最终 full archcheck 先 RED：`a4a265f` 新增测试注释把局部变量 firstCalls 放入反引号，而既有门禁明确不收集函数参数与局部变量。最小修复只去掉该注释反引号，不改测试、背压逻辑、inbox、renderer、guard、threshold 或 golden；focused comment gate 与 full archcheck 随后 GREEN。
- 使用 Go 1.26.0、独立 `/tmp` GOCACHE 与 Rust 1.97.1 执行 exact focused race、`make rust`、`make rust-check`、exact full Go race、vet、build、notice/专服隔离、fresh `visual-check` 与 OpenSpec strict，全部通过；fresh 视觉结果为 14/14 场景、每场 0/230400 差异。`AGENTS.md`/`CLAUDE.md` byte-identical，协议 v23、engine ABI v6、client ABI v7、benchmark scenario v18 均未推进。
- Task 8 的独立非视觉 round 2 与 Task 7 的独立增量/视觉 round 3 合并覆盖最终产品 diff，8.1–8.7 complete。Change 保持 active；在 controller 明确批准并另行执行 archive workflow 前不得归档。
