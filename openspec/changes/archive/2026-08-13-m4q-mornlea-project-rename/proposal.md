## Why

当前 `minecraft-go`、`mcgo` 与 `mcgod` 容易暗示官方兼容关系；项目实际是独立体素游戏。M4Q 将当前产品与技术身份统一为 Mornlea，并保持既有玩家身份和配置连续可用。

## What Changes

- 把当前仓库、Go module、客户端/服务端命令、Rust crate/C ABI、构建入口和当前文档切换为 Mornlea。
- 新默认用户目录为 `mornlea`；仅在新文件缺失时校验并复制旧 `minecraft-go` config/profile。
- 保持协议、存档、ABI 数值、benchmark scenario、fixture、golden 与性能 baseline 不变。

## Non-Goals

- 不保留旧命令、module、C symbol 或环境变量兼容层。
- 不改写历史设计、归档 change、性能证据或 Git 历史。
- 不删除旧用户目录，也不建立新旧目录双向同步。

## Capabilities

### New Capabilities

- `project-identity`: 当前产品、module、命令、构建和开发入口统一使用 Mornlea。
- `local-data-migration`: 新默认本机数据路径安全继承旧 config/profile。

### Modified Capabilities

- `natural-material-generation`: 离线迁移命令改为 `mornlea-server`。
- `repository-code-organization`: 允许且仅允许 6 个命令身份测试改名和 1 个新身份守卫，同时保持其余入口与统一基线 artifact。
- `rust-engine-mesh`: crate、header、C symbol、dylib 与客户端产物改为 Mornlea 身份。

## Impact

影响当前 Go module 与 imports、客户端和专用服务端入口、Rust mesh ABI 与打包、默认 config/profile 路径、构建/CI/Hook、架构守卫及当前文档。仅默认 config/profile 加载新增跨进程原子 no-clobber 发布；显式路径继续使用既有 replace-on-save，权威 tick、客户端/服务端运行期边界与 goroutine 边界不变。协议 v15、区块 schema v8、玩家 schema v6、metadata v2、ABI 数值、fixture、golden、benchmark scenario v15 与性能 baseline 保持不变；迁移仅复制旧本机文件并保留旧目录。
