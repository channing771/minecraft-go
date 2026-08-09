# 确认受伤红屏反馈任务

## 1. 确认生命值反馈状态机

- [ ] 1.1 在 `cmd/mcgo/damage_feedback_test.go` 覆盖首次基线、确认下降、回复、不变、连续下降、elapsed 边界与 reset。
- [ ] 1.2 在 `cmd/mcgo/damage_feedback.go` 实现固定 180ms 的最小值类型状态机。
- [ ] 1.3 运行 `go test ./cmd/mcgo -run '^TestDamageFeedback' -race -count=1`。

## 2. overlay renderer 与 application 接线

- [ ] 2.1 为固定资源、零强度路径、钳制、三顶点 draw、幂等释放和 headless 像素写失败测试。
- [ ] 2.2 实现 `DamageOverlayRenderer` 与固定 WGSL，不引入通用特效框架。
- [ ] 2.3 接入 application 构造、逆序释放、会话 reset、frame 更新和 name tag/HUD 间的绘制顺序。
- [ ] 2.4 更新共享 test fixture、生命周期断言与 README 当前能力。
- [ ] 2.5 运行 `go test ./cmd/mcgo ./internal/render -race -count=1` 与 hidden benchmark。

## 3. 验证与归档

- [ ] 3.1 运行架构、全仓 race、vet、gofmt 与 OpenSpec strict 门禁。
- [ ] 3.2 确认协议、存档、scenario、capture 与 golden 均未变化，工作区只剩用户日志。
- [ ] 3.3 归档 `confirmed-damage-feedback` 并再次严格验证主规格。
