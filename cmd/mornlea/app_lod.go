//go:build darwin

package main

import (
	"fmt"
	"math"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/lod"
	"github.com/channing771/mornlea/internal/worldgen"
)

// lodTileChunks 是每个远环 tile 覆盖的 chunk 数(4×4 chunk = 64×64 列)。
// tile 坐标按西缘对齐:tile_x 覆盖 chunk [tile_x×4, tile_x×4+4),即
// block 西缘 = tile_x×64。chunk→tile 的换算集中在本文件的
// lodTileFromChunk/lodFarTileRadius 两处,不散落第二套推导。
const lodTileChunks = 4

// lodMaxTiles 镜像 Rust client 渲染器的 tile 表容量上界 MAX_LOD_TILES
// (engine/crates/mornlea_client/src/render/lod.rs;client ABI 没有导出该
// 常量,只能镜像)。锚定要求:最大合法配置 viewDistance=64 ×
// lodFarMultiplier=8 → tile 半径 128 → 全方环 (2×128+1)² = 66049,容量
// 131072(2^17)留约 2 倍余量。同步断言见
// TestLodMaxTilesMirrorsRustConstant——Rust 侧改动而本镜像未跟会直接
// 测试失败,防止容量论证双源漂移。
const lodMaxTiles = 131072

// lodWiringEnabled 报告本次运行是否接线远环 LOD:配置 lodEnabled 关闭
// 或 benchmark 观察者路径(其 viewDistance 路径无远环需求,5.4 再显式
// 关闭)都保持零参与——不建 Scheduler、不消费登录种子、渲染器远环 pass
// 空转即零成本。
func lodWiringEnabled(render config.Render, benchmark bool) bool {
	return render.LodEnabled && !benchmark
}

// lodTileFromChunk 把 chunk 坐标换算成 tile 坐标(floor 语义:算术右移对
// 负数同样向负无穷取整,tile 覆盖 [tile×4, tile×4+4) chunk)。右移只缩小
// 数值幅度,int32 输入不可能因此溢出;极端半径输入的饱和钳制见
// lodFarTileRadius。
func lodTileFromChunk(chunk core.ChunkPos) lod.TilePos {
	const shift = 2 // log2(lodTileChunks)
	return lod.TilePos{X: chunk.X >> shift, Z: chunk.Z >> shift}
}

// lodFarTileRadius 从近环视距(viewDistance, chunk)与远环倍率推导远环
// tile 半径(切比雪夫):ceil(farMultiplier × viewDistance / 4)。向上取整
// 保证全雾距离(farRadiusBlocks 的 0.75×far 之内)始终被已上传 tile 覆盖,
// 非整除配置下外缘不露天空缝。乘法在 int64 上完成并饱和钳制到
// math.MaxInt32——防御直接构造 Render 的调用方传入极端值时 int32 溢出
// 回绕出负半径(控制器裁决 1,顺带消化任务 E 评审②);经 config.Load 的
// 正常路径输入恒在 [2,64]×[2,8] 内,钳制分支不可达。
func lodFarTileRadius(viewDistance, farMultiplier int) int {
	if viewDistance < 0 {
		viewDistance = 0
	}
	if farMultiplier < 0 {
		farMultiplier = 0
	}
	chunks := int64(viewDistance) * int64(farMultiplier)
	radius := (chunks + lodTileChunks - 1) / lodTileChunks
	if radius > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(radius)
}

// lodRingTileCount 返回切比雪夫半径 radius 的全方环 tile 数 (2r+1)²。
// 这是 QueueRing 播种的域规模,也是容量论证(Ruling 18)的峰值上传数。
func lodRingTileCount(radius int) int {
	if radius < 0 {
		return 0
	}
	sides := 2*int64(radius) + 1
	return int(sides * sides)
}

// lodFogDistances 按 lodFarMultiplier 推导远环距离雾锚点(block):
// farRadiusBlocks = lodFarMultiplier × viewDistance × 16,起雾
// start = 0.5×far、全雾 full = 0.75×far。保持 0.5/0.75 半径锚点使
// 非默认倍率下外缘全雾仍成立(全雾带恒为远环最外 25%);默认几何
// (32,3) 精确落在渲染器编译期默认 768/1152 上。调用方必须先经
// config.Render.NormalizeLOD 保证倍率在 [2,8] 内——越界推导会违反
// 渲染器 start>0 且 full>start 的契约并 panic。
func lodFogDistances(viewDistance, farMultiplier int) (start, full float32) {
	far := float32(viewDistance) * float32(farMultiplier) * 16
	return far * 0.5, far * 0.75
}

// rendererLodSink 把远环 tile 的上传/释放适配到 Rust 渲染器出口
// (client ABI v6 的 render_upload_lod_tile / render_drop_lod_tile),
// 是 internal/lod.TileSink 在 cmd/mornlea 的实现。
type rendererLodSink struct{ renderer *client.Renderer }

// UploadLodTile 整体上传/替换一个远环 tile 的壳 quad 流。
func (sink rendererLodSink) UploadLodTile(x, z int32, quads []byte) {
	sink.renderer.UploadLodTile(x, z, quads)
}

// DropLodTile 释放一个已上传 tile 的 GPU 资源(幂等)。
func (sink rendererLodSink) DropLodTile(x, z int32) {
	sink.renderer.DropLodTile(x, z)
}

// attachLodScheduler 在登录成功取得权威世界种子后接线远环 LOD(设计
// 「Go 编排」的入队时机裁决):以登录种子构造与近环逐字节一致的
// worldgen `MGW1` header,建 Scheduler 并立即以初始 tile 中心播种全环;
// 同时按 lodFarMultiplier 推导雾距离并调用渲染器的雾设置出口(一次性
// 渲染器状态,不进帧循环)。种子是运行时事实而非配置,只在参数间传递、
// 不落 config。禁用路径零参与:直接返回,不建 Scheduler、不消费种子。
// benchmark 观察者由调用方传入 benchmark=true 显式关闭。
func (a *application) attachLodScheduler(worldSeed uint64, benchmark bool) error {
	if !lodWiringEnabled(a.render, benchmark) {
		return nil
	}
	// 防御归一:直接构造 Render 的调用方(测试/未来入口)可能给出零值或
	// 越界;NormalizeLOD 与 config.Load 共用同一份合法域,保证雾推导与
	// 半径推导的输入恒在 [2,8]/[2,64] 内。归一结果写回 a.render,让此后
	// 每帧 pumpLodFrame 的半径推导与装配时的播种半径永远同源。
	render := a.render.NormalizeLOD()
	a.render = render
	generator := worldgen.New(int64(worldSeed))
	scheduler, err := lod.NewScheduler(
		rendererLodSink{renderer: a.renderer},
		generator.Header(),
		uint32(render.LodStep),
		// LOD 独立帧预算,默认与近环 mesh 上传同量级(数值只记录不门禁)。
		applicationUploadPerFrame,
	)
	if err != nil {
		return fmt.Errorf("lod: 构造远环调度器: %w", err)
	}
	a.lodScheduler = scheduler
	a.lodTileCenter = lodTileFromChunk(a.center)
	scheduler.QueueRing(a.lodTileCenter, lodFarTileRadius(render.ViewDistance, render.LodFarMultiplier))
	fogStart, fogFull := lodFogDistances(render.ViewDistance, render.LodFarMultiplier)
	a.renderer.SetLodFog(fogStart, fogFull)
	return nil
}

// pumpLodFrame 是帧循环的远环半部(每帧一次,全部非阻塞):玩家跨 tile
// 边界时增量入队新进入范围的 tile(QueueRing 幂等,只补差额),随后
// BeginFrame 重置 LOD 帧预算、FlushUploads 冲刷就绪结果并按预算派发
// pending、DropOutside 释放远环半径外的 pending 与已上传 tile。挂在
// renderFrame 的近环 DropOutside 之后,镜像 SectionScheduler 的帧序。
// 禁用时 lodScheduler 为 nil,本方法是纯 nil 检查即返回——帧循环零新增
// 阻塞与分配。
func (a *application) pumpLodFrame() {
	if a.lodScheduler == nil {
		return
	}
	radius := lodFarTileRadius(a.render.ViewDistance, a.render.LodFarMultiplier)
	tile := lodTileFromChunk(a.center)
	if tile != a.lodTileCenter {
		a.lodTileCenter = tile
		a.lodScheduler.QueueRing(tile, radius)
	}
	a.lodScheduler.BeginFrame()
	a.lodScheduler.FlushUploads(tile)
	a.lodScheduler.DropOutside(tile, radius)
}
