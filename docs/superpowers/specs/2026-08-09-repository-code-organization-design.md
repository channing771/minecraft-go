# M4O 全仓职责化代码组织设计

日期：2026-08-09

## 1. 背景

项目持续加入权威模拟、持久化、网络、渲染与性能验证后，部分单一 Go 文件开始同时承载多个职责。当前较明显的生产代码热点包括：

| 文件 | 当前行数 | 混合职责 |
| --- | ---: | --- |
| `cmd/mcgo/app.go` | 1545 | 状态、装配、连接、生命周期、帧循环、消息、输入与渲染辅助 |
| `internal/render/hotbar.go` | 1173 | 快捷栏、背包、合成、熔炉、箱子、生命值与 GPU 绘制 |
| `internal/sim/engine.go` | 1056 | tick、订阅、区块接入、命令执行与变化收尾 |
| `internal/network/codec.go` | 1036 | 双向 packet 编解码、校验与共享 wire 值 |
| `internal/storage/chunk_codec.go` | 841 | envelope、逻辑区块、容器槽与基础解码器 |
| `internal/server/session.go` | 828 | session 生命周期、心跳、注册表与输入入口 |

行数本身不是缺陷。例如 `internal/gfx/wgpu.go` 虽有 1307 行，但整体仍属于 WebGPU 后端；它需要的是按后端资源职责拆文件，不是额外的抽象层。

本变更审计全仓所有 Go 文件，并按真实职责整理代码。审计覆盖不等于强制修改：职责已经单一、命名清晰的文件继续保留。

## 2. 目标

- 审计 `cmd/` 与 `internal/` 下全部 Go 文件，明确其职责归属。
- 把同时承载多个独立职责的文件拆成可单独理解和测试的单元。
- 默认在原包内拆文件；只有稳定、单向且可独立测试的边界才提取新包。
- 保持现有 CLI、游戏行为、协议、存档、渲染、并发和性能契约不变。
- 让每个实施波次可以独立评审、验证和回退。

## 3. 非目标

- 不新增游戏功能或修复与代码组织无关的行为问题。
- 不设置生产文件或测试文件的机械行数上限。
- 不新增 `utils`、`common`、工厂、单实现接口或为未来预留的扩展点。
- 不升级协议、schema、benchmark scenario、性能基线或视觉 golden。
- 不把现有内部包重新设计成公共 API。
- 不要求每个被审计文件都产生 diff。

## 4. 边界判定

每个 Go 文件在实施计划中归入以下一种结果：

1. **保留**：文件只有一个内聚职责，继续保持原状。
2. **同包拆分**：职责不同但共享包内状态，按职责移动声明。
3. **提取新包**：职责拥有稳定名称、窄 API、单向依赖和独立测试边界。
4. **删除**：代码能够证明无调用方且无兼容职责。

新包必须同时满足：

- 对外 API 明显小于被隐藏的实现；
- 不产生循环依赖、反向依赖或新的跨层访问；
- 使用方不需要读取原包的内部状态；
- 能在不依赖上层装配包的情况下独立测试；
- 在 `internal/archcheck/deps_test.go` 中登记后仍符合现有依赖方向。

不满足任一条件时使用同包拆分。行数只帮助发现候选，不作为拆分或验收标准。

## 5. 目标组织

### 5.1 基础与叶子包

审计 `core`、`world`、`physics`、`mesh`、`assets`、`profile`、`config`、`logging`、`worldgen` 与 `gfx`。

- 已按领域命名的小文件继续保留。
- `internal/gfx/wgpu.go` 在 `internal/gfx` 内按转换、设备、surface、资源和 command encoder 拆分。
- C/Objective-C bridge 留在能够安全引用它的文件中；所有平台实现文件保留正确的 build tag。
- 大型测试按被测职责拆分，共享 helper 留在同一测试包。

### 5.2 协议与存储

- `internal/network/codec.go` 在原包内拆为客户端编码、客户端解码、服务端编码、服务端解码和共享 wire 值。
- `internal/storage/chunk_codec.go` 在原包内拆为 envelope、逻辑区块、容器槽和基础解码器。
- 不提取 `network/wire` 或 `storage/codec` 子包；现有消息与存档类型会令这类提取产生循环依赖或大范围类型搬迁。
- wire bytes、packet ID、schema、压缩 envelope、错误语义、golden 和 fuzz 语义保持不变。

### 5.3 权威模拟与服务端

- `internal/sim/engine.go` 按 tick 调度、订阅、区块接入、命令执行和变化收尾拆分。
- `internal/server/session.go` 按 session 生命周期、心跳、注册表和输入入口拆分。
- `internal/server/persistence.go` 与 `player_persistence.go` 按调度、重试、flush 和 cache 生命周期拆分。
- 上述逻辑依赖共享权威状态，保持原包，不引入协调接口。

### 5.4 客户端与呈现

- `internal/client/mesher.go` 和 `predictor.go` 按队列、worker、状态推进和查询职责拆分。
- 将 `internal/render/hotbar.go` 的完整 HUD 职责提取为 `internal/render/hud`。
- `internal/render/hud` 负责快捷栏、背包、合成、熔炉、箱子、生命值、命中测试和对应 GPU pass；它使用既有 `render.GlyphSource` 与窄内部 API `render.ItemColor`，`internal/render` 不反向依赖 HUD。
- 程序化物品颜色的唯一实现归属 `internal/render/drop.go`：原 `hotbarItemColor` 提升为 `render.ItemColor`，掉落物与 HUD 都直接调用它；`hud/layout.go` 不拥有或复制实现，不增加 wrapper、alias、callback/config、第二包或重复实现。
- `internal/render` 继续负责世界、天空、实体、文字和通用渲染设施。
- HUD shader 随其唯一所有者移动，内容和渲染结果不变。
- 跨职责的 `TestScreenSpaceRenderersIgnoreWorldDaylight` 仍留在 `internal/render/daylight_test.go`；它以 test-only `//go:embed hud/shader/hotbar.wgsl` 读取移动后的同一份唯一 shader，保留原测试名及 name tag/hotbar 断言，不复制字节、不新增生产 API 或 `render -> hud` 生产依赖，也不保留生产 wrapper。

### 5.5 命令与工具

- `cmd/mcgo/app.go` 按状态定义、依赖装配、启动连接、生命周期、帧循环、消息处理、输入交互和渲染辅助拆分。
- benchmark、capture 与 `cmd/perfcheck` 只在一个文件确实混合多个职责时拆分。
- 不提取通用 application framework；这些装配逻辑只有一个产品和一个调用入口。
- `cmd/mcgod` 与 `cmd/gfxspike` 若审计后仍为单一职责则保留。

### 5.6 测试组织

- 测试与其生产职责在同一波次移动。
- 大型集成测试按场景拆文件，例如登录、重启、恢复、Memory/TCP parity 与熔炉场景。
- 共享 harness 留在同包单一 `_test.go` helper 文件。
- 不创建通用 `testutil` 包，不把为了访问未导出状态的测试改造成导出生产 API。

## 6. 实施波次

整个工作使用一个 OpenSpec change，暂定名 `m4o-responsibility-oriented-code-organization`，按以下顺序执行：

1. 基础与叶子包；
2. 协议与存储；
3. 权威模拟与服务端；
4. 客户端与呈现；
5. 命令与工具；
6. 全仓审计收尾与最终门禁。

每个波次进一步拆成单包或单职责任务。每项任务独立提交，只有对应验证通过后才能进入下一项。测试文件随所属职责处理，不单独形成一次仓库级测试搬迁。

## 7. 行为与数据流不变量

每次修改只做声明搬迁、包归属调整或确定的死代码删除，不同时修改算法。

- 服务端仍是世界和玩家状态的唯一权威。
- Memory 与 TCP 继续经过相同登录、协议和模拟路径。
- goroutine、channel、锁、buffer 与资源的所有权和释放顺序不变。
- 错误值、错误文本、日志字段和 GPU label 保持不变。
- wire bytes、packet ID、schema、chunk/player fixture 和 hash 保持不变。
- build tag、CGO 边界和无图形专用服务端构建能力保持不变。
- 视觉 golden、benchmark workload、scenario 和硬件基线身份保持不变。

package extraction 直接迁移内部调用方，不保留重复 wrapper、类型别名或旧实现兼容层。`render.ItemColor` 只服务既有生产调用方的真实跨包共享，不是测试 helper 或兼容层。若迁移需要新增状态、行为分支或协调接口，说明边界判断错误，应停止并修订设计。

## 8. 文件名相关守卫

当前部分架构测试直接读取固定文件，例如 `internal/archcheck/deps_test.go` 对 `cmd/mcgo/app.go` 的登录路径检查。文件拆分后，这类守卫应改为扫描整个目标包，同时保留原有语义断言。

不得因为声明移动而删除或放宽守卫。新的 `internal/render/hud` 必须登记精确依赖白名单，并保持 `hud -> render` 单向依赖、拒绝 `render -> hud`；只有 `internal/gfx` 可以直接导入 WebGPU 绑定的限制继续生效。

## 9. 测试与验证

行为不变的声明搬迁不制造人工 RED 测试。每项任务先记录当前 focused 测试结果，再移动一个职责并重新运行：

```bash
go test ./受影响包 -race -count=1
go test ./internal/archcheck -count=1
gofmt -l .
git diff --check
```

协议与存储波次额外运行现有 golden、故障注入和短时 fuzz。渲染与 HUD 波次运行无窗口 headless 测试和视觉 golden 校验，但不更新图片。性能相关包运行现有 benchmark 与 M2 baseline 自比较，不升级 scenario 或基线。

最终执行：

```bash
go test ./... -race
go vet ./...
gofmt -l .
openspec validate --all --strict --no-interactive
```

自动测试不得启动或聚焦前台游戏窗口。

## 10. 风险与回退

- **隐式行为漂移**：移动时夹带条件、顺序或错误处理变化。通过单职责提交和 focused 测试隔离。
- **依赖倒置**：新包反向读取原包状态。HUD 只调用 `render.ItemColor` 等既有窄接口，`internal/render` 不导入 HUD；若窄 API 无法成立，则取消提包并改为同包拆分。
- **共享颜色漂移**：掉落物与 HUD 分别保留颜色实现会产生视觉分叉。只保留 `internal/render/drop.go` 中的 `render.ItemColor`，并用掉落物 focused 测试与视觉 golden 共同验证。
- **平台构建破坏**：遗漏 build tag 或错误搬迁 CGO bridge。通过 Darwin focused 测试、无图形服务端构建和架构门禁验证。
- **性能变化**：包边界可能影响内联。沿用现有 benchmark 与 baseline，自身差异不能通过升级基线掩盖。
- **无意义碎片化**：为缩短文件制造大量单函数文件。以职责而非行数评审，内聚大文件允许保留。

每个波次独立提交。任一波次出现无法解释的行为、golden 或性能差异时立即停止；回退该波次提交，不修改期望值掩盖问题。

## 11. 被否决的方案

### 全面领域拆包

同时提取 `mcgo/app`、`network/wire`、`storage/codec`、`server/session` 等包会制造大量内部 API、依赖白名单变更和循环依赖风险，把纯结构任务升级成架构重写，因此否决。

### 仅机械拆文件

它风险最低，但无法处理 HUD 这种已经形成独立领域的职责，也不能改善真实包边界，因此只作为默认手段而不是唯一手段。

### 文件行数门禁

固定行数会鼓励无意义拆分、把相关逻辑分散到更多文件，并误伤单一职责的后端实现，因此不引入。

## 12. 完成条件

- `cmd/` 与 `internal/` 下所有 Go 文件均完成职责审计。
- 多职责文件已拆分；保留的大文件仍只有一个内聚职责。
- 唯一预先批准的新包是 `internal/render/hud`；其他新包必须先修订并重新批准本设计。
- 所有行为测试、协议与存储 golden、视觉 golden 和性能基线保持不变。
- 每个波次都能独立通过验证、评审和回退。
- 独立 review 未发现行为漂移、依赖倒置或无意义的小文件扩散。
