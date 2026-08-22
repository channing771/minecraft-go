## ADDED Requirements

### Requirement: 配置 v1 支持启动时本地材质包路径

配置 schema v1 SHALL 接受可选顶层字符串 `texturePackPath`。空值 MUST 表示禁用用户覆盖；绝对路径 MUST 清理后使用，相对路径 MUST 按实际加载的配置文件所在目录解析为绝对路径。保存配置时 MUST 保留用户原始路径表达，MUST NOT 把解析后的路径写回文件；该字段 MUST NOT 进入数值调试面板或权威模拟参数。

#### Scenario: 相对路径按配置文件目录解析

- **GIVEN** 从某个配置文件加载 `texturePackPath: "packs/local"`
- **WHEN** 客户端解析材质包路径
- **THEN** 生效路径 MUST 是相对于该配置文件所在目录的清理后绝对路径
- **AND** 保存时 MUST 继续写出原始值 `packs/local`

#### Scenario: 可选字段不提升配置版本

- **GIVEN** 一份不含 `texturePackPath` 的现有 v1 配置文件
- **WHEN** 新客户端加载该文件
- **THEN** 用户材质覆盖 MUST 保持禁用
- **AND** 配置 schema 版本 MUST 仍为 `1`

#### Scenario: 非字符串材质路径被拒绝

- **GIVEN** v1 配置中的 `texturePackPath` 不是字符串
- **WHEN** 加载该配置文件
- **THEN** 加载 MUST 返回带字段上下文的错误

### Requirement: 自动化模式忽略本地材质覆盖

benchmark 与 capture 模式 MUST 使用编译默认配置中的空材质包路径，MUST NOT 读取或应用用户配置中的 `texturePackPath`；capture MUST 继续使用内嵌默认材质。

#### Scenario: benchmark 忽略本地材质路径

- **GIVEN** 本机配置包含非空 `texturePackPath`
- **WHEN** 以 benchmark 模式启动
- **THEN** benchmark MUST NOT 打开或应用该目录
- **AND** benchmark scenario MUST 保持 v18

#### Scenario: capture 忽略本地材质路径

- **GIVEN** 本机配置包含非空 `texturePackPath`
- **WHEN** 以 capture 模式启动
- **THEN** capture MUST NOT 打开或应用该目录
- **AND** MUST 使用内嵌默认材质生成视觉结果
