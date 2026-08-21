# Tasks: rust-engine-lod-shell

前置约束:协议段重编为 v23——变基后基线为 main v21,v22 已被在飞的
authoritative-farming 占用,本变更排其后;若顺序对调先互换版本号再动工。

## 1. engine 壳生成核心(Rust)

- [x] 1.1 `engine/crates/mornlea_engine/src/lod.rs`:窗高取 max、材质取
      最高列表层材质、同材质等高顶面贪心合并、断差侧裙、着色权重;单测
      覆盖空 tile、单一材质、断差闭合与边界窗;
      验证:`cd engine && cargo test -p mornlea_engine`。
- [x] 1.2 确定性 golden:固定 seed/tile/step 的输出字节回归测试;
      验证:`cargo test -p mornlea_engine`。

## 2. engine ABI v6 与 Go 绑定(变基重编,原编号 v4)

- [x] 2.1 `ffi.rs` 出口 `mornlea_lod_shell`(入口校验、两段式 overflow、
      panic catch),`engine/include/mornlea_engine.h` 同步并升
      `MORNLEA_ENGINE_ABI_VERSION` 到 6(变基重编:main 的 fluid 系列已占用
      v4/v5);版本拒绝与容量探测单测;
      验证:`cargo test -p mornlea_engine && cargo clippy --all-targets
      -- -D warnings`。
- [x] 2.2 `internal/nativeabi` 绑定与输入输出编码测试;probe 高度差分与
      壳覆盖结构测试(oracle 保留方案);
      验证:`go test ./internal/nativeabi -race -count=1`(差分测试随
      `internal/lod` 落地后一并运行)。
- [x] 2.3 新建 `internal/lod` 包并登记 `internal/archcheck` 依赖;
      验证:`go test ./internal/archcheck -count=1`。

## 3. 协议 v23 种子下发(变基重编,原编号 v18)

- [x] 3.1 `internal/network`:`LoginSuccess.WorldSeed uint64`、协议版本
      v21→v23(v22 已被在飞的 authoritative-farming 占用)、编解码与 golden wire
      更新、握手版本拒绝新旧组合测试(Memory 与 TCP 双传输);服务端
      在构造 LoginSuccess 时填入真实世界种子(internal/server,单机与
      专服同一路径);
      验证:`go test ./internal/network -race -count=1`。

## 4. client 远环 pass(Rust;ABI v5→v6,终审修复波随雾 setter 升 v7)

- [x] 4.1 `mornlea_client`:`render_upload_lod_tile`/`render_drop_lod_tile`
      (tile 整体替换语义)、远环 pipeline(世界坐标大 quad)、距离雾
      WGSL(外缘带全雾,昼夜 tint 同源)、tile 级视锥剔除、帧序
      天空→远环→近环→实体;入口校验拒绝单测;
      验证:`cargo test -p mornlea_client && cargo clippy --all-targets
      -- -D warnings && make -C .. rust`。
- [x] 4.2 `internal/client` 上传绑定与测试;
      验证:`go test ./internal/client -race -count=1`。

## 5. Go 编排与接线

- [x] 5.1 `internal/lod.Scheduler`:环形入队、pending 覆盖、距离升序
      冲刷、独立帧预算、`DropOutside` 释放;worker goroutine 生成模型
      (镜像字形 worker,结果切片发送后不可变);测试先行;
      验证:`go test ./internal/lod -race -count=1`。
- [x] 5.2 `internal/config` 调参 `lodEnabled`/`lodFarMultiplier`/
      `lodStep`;`cmd/mornlea` 接线:登录取得种子播种、跨 tile 边界
      增量入队、每帧冲刷、禁用时零参与;显式验收:接线 MUST 按
      `lodFarMultiplier` 推导雾距离并调用雾设置出口(非默认倍率下
      外缘全雾仍成立),且环形入队半径与 `MAX_LOD_TILES` 容量一致
      (最大合法配置下加「不触 CAPACITY」单元断言);
      验证:`go test ./internal/config ./cmd/mornlea -race -count=1`。
- [x] 5.3 capture golden 重新生成(变化仅远景带)+ `far-horizon`
      场景入库;既有场景近处像素不变的比对断言;
      验证:`go test ./cmd/mornlea -race -count=1 -run TestCapture`。
- [x] 5.4 benchmark producer 默认 `lodEnabled=false`,scenario 保持
      v17(变基后与 main 一致,不迁移),LOD 专项数值另存记录(只记录
      不门禁);
      验证:`go test ./cmd/mornlea -race -count=1 -run Benchmark`。

## 6. 收尾

- [x] 6.1 全量门禁:`make rust`、`go test ./... -race`、
      `go vet ./...`、`gofmt -l .` 无输出、
      `go test ./internal/archcheck -count=1`、
      `openspec validate --all --strict --no-interactive`。
- [x] 6.2 真窗自动化验收(CGEvent 注入,复用 R1/R2c 工具):远景入画、
      雾过渡平滑、移动跨 tile 无闪烁、关闭路径干净退出;
      验证:截图与退出码。
- [x] 6.3 文档基线:`docs/notes/progress.md`、AGENTS.md/CLAUDE.md
      (engine ABI v6、client ABI v7、协议 v23、远环语义与海平面壳
      钳制)。
