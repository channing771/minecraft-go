# Design: rust-engine-physics-step

完整设计见 `docs/superpowers/specs/2026-08-15-rust-engine-physics-step-design.md`。要点：

- ABI：magic `MGP1`、layout v1、128 字节 header（position/velocity、on_ground/jump/
  move_x/move_z、yaw_sin/yaw_cos、fixed_delta_seconds、tunables×8、sweep bounds×6、
  prism origin/dimensions）+ 每 cell 196 字节（沿用 MGC1 cell 格式）；输出 32 字节
  （position/velocity、clipped mask、on_ground/used_step/hit_unknown、reserved）。
- 三角函数留在 Go：`math.Sin/Cos` 算 yaw_sin/yaw_cos 传入，规避跨语言 libm 末位差异。
- 位移凸包 sweep bounds：水平轴 `[min(0,v,t)·dt, max(0,v,t)·dt]`，垂直轴按
  jump/fallen/−terminal 分支取凸包；Rust 积分后自检位移在界内，违约返回 StatusInput。
- 运算顺序逐条镜像 Go：mgl32 `Len` 为 f32 平方和 → f64 Sqrt → f32；`Normalize` 为
  `1/Len` 再乘分量；`max`/`min` 符号零语义（+0 胜出）两侧一致；sqrt 正确舍入无双重舍入。
- collision.rs 拆分 `resolve_collision_parts`，cells 切片零拷贝复用输入尾部。
- 依赖方向不变：physics → nativeabi → C ABI；archcheck 无需登记新边。
- 回退：单 PR revert 恢复旧布局；oracle 即旧实现副本，可随时移回生产。

## 平台假设

奇偶差分门禁只在 arm64（开发机与 macOS CI）运行；amd64 上 Go bounds/oracle 不收缩而
Rust mul_add 融合（IEEE 正确舍入，跨平台一致），二者可差 ≤1 ulp，由 prism 1e-5 epsilon
边距与自检 1 ulp 余量兜底；生产跨平台确定性由 Rust 单一内核保证。
