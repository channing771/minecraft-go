# Mornlea 改名迁移

## 名称映射

| 旧身份 | 新身份 |
| --- | --- |
| 仓库 `channing771/minecraft-go` | `channing771/mornlea`（源码合并后由 operator 执行外部改名） |
| Go module `minecraft-go` | `github.com/channing771/mornlea` |
| 客户端 `mcgo`、`cmd/mcgo`、`bin/mcgo` | `mornlea`、`cmd/mornlea`、`bin/mornlea` |
| 专用服务端 `mcgod`、`cmd/mcgod`、`bin/mcgod` | `mornlea-server`、`cmd/mornlea-server`、`bin/mornlea-server` |
| Rust crate/dylib `mcgo_mesh`、`libmcgo_mesh.dylib` | `mornlea_mesh`、`libmornlea_mesh.dylib` |
| C header/C symbol `mcgo_engine.h`、`mcgo_engine_abi_version`、`mcgo_mesh_section` | `mornlea_engine.h`、`mornlea_engine_abi_version`、`mornlea_mesh_section` |
| Hook 环境变量 `MINECRAFT_GO_HOOKS_ALLOW_NO_SPEC` | `MORNLEA_HOOKS_ALLOW_NO_SPEC` |

## 本机数据

新默认目录为 `mornlea`，其中使用 `config.json` 和 `profile.json`。仅当新文件缺失时，程序校验并复制旧 `minecraft-go` config/profile；旧文件不移动、不删除。

## 回退与单向性

旧版本继续读取旧目录。新版本写入新目录后，两边不会自动同步；不要交替运行并假定状态会合并。

## 历史资料

`docs/superpowers/**`、归档 OpenSpec 与历史性能证据保留当时真实名称；当前使用方法以 README 和本说明为准。
