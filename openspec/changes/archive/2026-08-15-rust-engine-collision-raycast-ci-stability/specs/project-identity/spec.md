## MODIFIED Requirements

### Requirement: 当前项目身份统一为 Mornlea
系统 MUST 以 `Mornlea` 作为当前产品名，以 `github.com/channing771/mornlea` 作为 Go module，以 `mornlea` 和 `mornlea-server` 作为客户端与专用服务端命令。

#### Scenario: clean checkout 构建当前入口
- **WHEN** 在 Apple Silicon/macOS 执行 canonical build
- **THEN** MUST 生成 `bin/mornlea`、`bin/mornlea-server` 与同目录 `libmornlea_engine.dylib`

#### Scenario: Linux 专服发布为同目录 bundle
- **WHEN** 在 Linux amd64 原生执行 canonical server build
- **THEN** MUST 生成同目录 `mornlea-server` 与 `libmornlea_engine.so`
- **AND** 两者 MUST 作为一个不可混装的发布单元升级

#### Scenario: 旧入口不再发布
- **WHEN** 枚举当前 module、命令、native ABI、构建和 Hook 身份
- **THEN** MUST 不存在 `mcgo`/`mcgod` wrapper、旧 `mcgo` C symbol、`libmornlea_mesh.dylib`、`libmornlea_mesh.so` 或旧环境变量 fallback
- **AND** additive ABI v1 的 `mornlea_mesh_section` MUST 继续保留
