# Change: rust-engine-physics-step

## Why

物理 tick 积分（加速度/摩擦/重力/跳跃/速度裁剪）仍在 Go。上一波已把 collision resolver
与 raycast DDA 迁入 Rust `mornlea_engine`；本变更把积分也迁入，使物理核心整体位于
engine，Go 只保留领域 API 与编码。arm64 上行为必须与现有 Go oracle 逐位一致
（float32 bit 级）；其他平台由 Rust 单一内核提供一致生产结果。

## What Changes

- Rust：新增 `mornlea_physics_step`（magic `MGP1`，128 字节 header + 每 cell 196 字节，
  输出固定 32 字节）；engine ABI version 1→2；`mornlea_collision_resolve` 保留为测试专用入口。
- Go：`physics.Step` 改为薄封装（输入校验、tunables 快照、yaw 三角、位移凸包 sweep
  bounds、prism 构建与编码、一次 native 调用、输出解码）；旧积分逻辑移入 `_test.go` oracle。
- 行为零变化：协议 v16、存档 schema、benchmark scenario v16 均不变。

## Impact

- 受影响包：internal/physics、internal/nativeabi、engine/crates/mornlea_engine、engine/include。
- 兼容性：engine ABI +1，Go binary 与 `libmornlea_engine.so` 仍为不可跨版本混装的
  release unit（`$ORIGIN` 约定不变）；既有 mesh/raycast/collision ABI 不动。
- 性能：每 tick 仍一次 native 调用；header 增 80 字节、输出 32 字节，benchmark/perfcheck
  数值只记录，不改变退出状态。
- 并发：Step 保持纯函数快照语义；SetTunables 原子换指针语义不变。

## 非目标

不改动碰撞 kernel 行为；不迁移世界生成、存档编解码、光照传播；不引入生产 Go fallback。
