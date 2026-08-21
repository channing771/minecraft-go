# 远环 LOD 专项性能数值记录

> **声明:本文件数值只记录,不作为任何门禁。** 按 `rust-engine-lod-shell`
> 变更的「配置与基准可比性」裁决:benchmark producer 默认 `lodEnabled=false`
> 且 scenario 保持 v17,远环不进入基准;本文件单独记录远环 LOD 的专项数值,
> 供后续调参与回归对比参考。性能数值永不改变测试退出状态;报告完整性、
> 真实 overflow 与数据丢失门禁不受本文件影响。

## 采集环境

- 日期:2026-08-21;分支 `agent/rust-engine-lod-shell`(变基整合波 K 之后,
  HEAD d38182f,任务 5.4)。此前 2026-08-16(b76a091 基线)草稿因 Ruling 22
  海平面壳语义作废重采:海盆窗钳到水面取水材质、水下不发裙边,默认带上传
  字节从 ≈15.3 MiB 降到 ≈7.8 MiB(海洋列不再产出深水裙边)。
- 机器:darwin/arm64(Apple Silicon),macOS 26.5;wgpu(Metal)。
- engine ABI v6 `mornlea_lod_shell`(`make rust` 构建),client ABI v7 远环
  pass(数值采集只用 CPU 生成路径,不依赖渲染器)。
- Go 1.26;世界种子 20260726(benchmark 同款固定种子;LOD 输出对该种子
  确定,数值随地形起伏在小范围内波动,量级代表默认地形)。
- 采集方式:临时一次性 harness(跑完即删,不入库),镜像 `cmd/mornlea`
  生产接线(`attachLodScheduler` 同参数:`lod.NewScheduler(sink, worldgen
  header, step, applicationUploadPerFrame=4 MiB)`,登录播种带状 `QueueRing`),
  以 60fps 帧节奏泵帧至 `PendingUploads()==0 && Busy()==0` 收敛;跨 tile
  增量只统计 pending 集合差。

## 全带播种与收敛(初始登录播种)

| 配置(viewDistance×farMultiplier,step) | 带内半径 [inner,outer] | 带 tile 数 | 上传总字节 | 单 tile 字节 min/avg/max | 收敛帧数(60fps) | 收敛墙钟 |
|---|---|---|---|---|---|---|
| 默认 32×3,step 4 | [9, 24] | 2 112 | 8 130 780(≈7.8 MiB) | 20 / 3 850 / 10 660 | 265 帧(≈4.42 s) | ≈4.7 s |
| 最大合法 64×8,step 4 | [17, 128] | 64 960 | 251 345 520(≈239.7 MiB) | 20 / 3 869 / 11 940 | 8 121 帧(≈135.35 s) | ≈185 s |

- 空 shell tile 数为 0:该种子下带内每个 tile 至少产出一个 quad(最小
  20 B = 单 quad 的深海/深洋水面壳)。
- 单帧预算 4 MiB(`applicationUploadPerFrame`,与近环 mesh 上传同量级),
  派发按步长静态上界计费:step 4 上界 16 000 B/tile
  (3N²+2N 个 quad × 20 B,N = 64/step = 16)→ 预算口径每帧最多派发 ≈262
  tile;实际瓶颈是单 worker 逐 tile 生成(60fps 帧间隔内 worker 完成的
  tile 数),265 帧 ≈ 2 112/8 即每帧约 8 tile,预算并未打满,最大合法配置
  同理为 worker 吞吐受限。
- 实际上传字节(≈3.9 KB/tile 均值)显著低于静态上界(16 KB),上界只是
  预算计费口径,不是实际内存占用。

## 跨 tile 边界增量(移动时)

| 配置 | 带 [inner,outer] | 初始 pending | 跨 1 tile(+x)新增 pending | 增量收敛 |
|---|---|---|---|---|
| 默认 32×3,step 4 | [9, 24] | 2 112 | +66 | 10 帧(≈0.17 s),墙钟 ≈0.2 s |
| 最大合法 64×8,step 4 | [17, 128] | 64 960 | +290 | 38 帧(≈0.63 s),墙钟 ≈0.7 s |

- 跨界增量 ≈ 2×outer+1 + 2×(outer−inner) 量级(新进入带的一列/一排),
  默认几何下 66 tile ≈ 0.25 MiB 实际字节,一两帧预算即可派发完;移动方向
  的反侧由 `DropOutside` 同帧释放(近环内缘也释放,保持近环零重叠)。

## 附:雾距离锚点(契约值,非测量)

- farRadiusBlocks = farMultiplier × viewDistance × 16;起雾 0.5×far、
  全雾 0.75×far。
- 默认 32×3:fog 768/1152 block;最大合法 64×8:fog 4096/6144 block
  (client ABI v7 `render_set_lod_fog` 下发)。
