# 确认受伤红屏反馈设计

## 状态与数据流

`cmd/mcgo` 持有 `damageFeedback`，只消费 `Predictor.Health()`；`internal/render` 只消费归一化强度，不知道生命值语义。正常帧顺序为 drain → receiver 错误检查 → 更新反馈 → renderFrame。会话清理主动 reset。

## 时序

固定持续时间为 180ms。首次 ready 只建立基线；确认下降当帧返回 1 且不扣 elapsed；其余帧仅在 elapsed>0 时衰减；elapsed>=remaining 清零；连续下降重置。增加与不变不启动新效果，已有效果继续衰减。

## renderer

`DamageOverlayRenderer` 持有一个 16 字节 uniform、一个 bind group 与一个 alpha-blend pipeline。WGSL uniform padding 使用三个独立 `f32`，避免 `vec3<f32>` 的自然对齐把 binding 扩大为 32 字节。WGSL 用全屏三角形和固定 `vec3f(0.65,0,0)`；alpha 为 `0.30 * strength * (1-smoothstep(0,0.35,edgeDistance))`。强度<=0 或 NaN 时直接返回；活动时一次 uniform 写、一个 pass、一次三顶点 draw。Release 幂等。

## 生命周期与绘制顺序

application 在 item-drop renderer 后、可选 debug-panel renderer 前创建 overlay，并按逆序释放。绘制顺序固定为 terrain/实体/name tag → overlay → HUD → debug panel。构造失败沿用 application 既有 errors.Join 与逆序清理。

## 并行、兼容与性能

生产代码只修改 app.go 并新增独立文件，不触碰 M4N 的 hotbar、terrain、assets、mesh 或 capture。README 可能有一行文本冲突，后合入方重放该句。本变更不写任何协议、schema、metadata 或 scenario 版本常量；当前分支上的 v13/v5/v6/v2/v14 保持原值，若先合入 M4N 则保持 M4N 的新值。非激活路径不得提交 pass、写 GPU 或分配。

## 被否决方案

- 爱心闪烁会修改 M4N 的 hotbar 文件且不够醒目。
- 通用 effect/post-processing 系统没有第二个消费者。
- 本地预测受伤破坏服务端唯一权威。
- capture golden 与 M4N 共享文件；本变更改用独立 headless 像素测试。
