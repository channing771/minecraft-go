# rust-engine-physics-step Delta Spec

## ADDED Requirements

### Requirement: 物理 tick 积分由 Rust engine 独占生产

系统 MUST 使物理固定步的积分（移动目标、加速/摩擦、跳跃、重力、终端速度裁剪）与碰撞解析、速度裁剪
在单次 `mornlea_physics_step` native 调用内完成；Go 生产路径不得包含积分实现，旧 Go
实现只允许存在于测试 oracle。

#### Scenario: 生产 Step 与 Go oracle 逐位一致

- GIVEN 任意合法 State、Input、CollisionSource 与运行时 Tunables
- WHEN 调用 physics.Step
- THEN 结果 State（Position/Velocity/OnGround）、UsedStep、HitUnknown 与测试内 Go
  oracle 实现逐位一致（float32 bit 级）

#### Scenario: 对角输入无斜向加速增益

- GIVEN 地面玩家，OnGround=true，MoveX=1，MoveZ=1，默认 tunables
- WHEN 执行一个固定步
- THEN 水平速度模长 ≈ 2.0（acceleration×dt，误差 < 1e-5）

#### Scenario: 跳跃与重力使用固定常量

- GIVEN 地面玩家，Jump=true，默认 tunables
- WHEN 执行一个固定步
- THEN 垂直速度等于 JumpSpeed 且 OnGround=false
- GIVEN 空中玩家垂直速度 −78
- WHEN 执行一个固定步
- THEN 垂直速度不低于 −TerminalFallSpeed

### Requirement: 运行时 tunables 每步生效

Step MUST 在入口取一次 ActiveTunables 快照并随调用传入 engine；SetTunables 之后的下一次
Step 使用新参数。

#### Scenario: SetTunables 后下一步生效

- GIVEN 任意合法状态与碰撞源
- WHEN SetTunables(增大 StepHeight) 后调用 physics.Step
- THEN 该步 prism 尺寸与结果反映新 StepHeight（4096-cell 上限用例保持通过）

### Requirement: sweep bounds 违约拒绝

Go MUST 按位移凸包界构建 prism 并在输入中携带 sweep bounds；Rust 积分后自检位移落在界内，
违约返回 StatusInput，Go 以稳定中文 panic 文案报告，不得产出静默漂移结果。

#### Scenario: 位移越界被拒绝

- GIVEN 合法 state/input 但输入 sweep bounds 不含该步积分位移
- WHEN 调用 native physics_step
- THEN 返回 StatusInput，且输出缓冲不被修改

### Requirement: 碰撞差分入口保留

`mornlea_collision_resolve` MUST 继续可用且行为不变，仅供测试差分；生产路径只调用
`mornlea_physics_step`。

#### Scenario: 碰撞差分测试继续通过

- GIVEN 现有碰撞级差分语料
- WHEN 调用 nativeabi.CollisionResolve
- THEN 与 Go 碰撞 oracle 逐位一致
