## MODIFIED Requirements

### Requirement: 离线迁移与服务器启动互斥
系统 SHALL 提供 `mornlea-server --world <世界目录> --migrate-materials --backup <备份目录>` 离线迁移入口。`--backup` MUST 必填；迁移模式 MUST 获取既有世界锁、不得监听 TCP 或启动服务器，并在世界正在使用、备份目录位于源世界内部或备份目录不能安全识别时失败。

#### Scenario: 迁移模式不启动服务端
- **GIVEN** 用户提供有效世界目录、`--migrate-materials` 和外部 `--backup` 目录
- **WHEN** `mornlea-server` 执行迁移模式
- **THEN** 它 MUST 在持有世界锁期间完成迁移，且 MUST NOT 监听 TCP 或进入服务端运行循环

#### Scenario: 被占用世界拒绝迁移
- **GIVEN** 目标世界已被服务器或客户端持有既有世界锁
- **WHEN** 用户执行迁移模式
- **THEN** 命令 MUST 失败，且 MUST NOT 修改源世界或写入完成状态
