# tunable-constants Specification

## Purpose
使物理与模拟的手感参数可以通过配置文件与游戏内调试面板调整，而不必改源码、重编译、重启并重新走到测试位置才能看到一次调整的效果，同时保证联机时服务端仍是这些参数的唯一权威、自动化验证仍基于稳定的编译期默认值。
## Requirements
### Requirement: 默认行为不变

无配置文件时，物理与模拟的生效参数 MUST 与本变更之前的编译常量逐字段相等。

#### Scenario: 无配置文件时生效参数等于原编译常量

- **GIVEN** 进程启动时未提供配置文件（默认路径不存在，也未指定 `--config`）
- **WHEN** 读取物理与模拟的生效参数
- **THEN** 每个字段的值 MUST 与本变更之前对应的编译常量逐字段相等

### Requirement: 配置文件容错

加载配置文件时，字段缺失 MUST 取默认值；字段越界 MUST 被钳制到合法区间并告警，且进程 MUST 正常启动；未知字段 MUST 被忽略并告警；JSON 语法错误与不认识的 `version` MUST 报错退出。

#### Scenario: 字段缺失取默认值

- **GIVEN** 配置文件中某个可调字段未出现
- **WHEN** 加载该配置文件
- **THEN** 该字段的生效值 MUST 等于其编译期默认值

#### Scenario: 字段越界被钳制并告警

- **GIVEN** 配置文件中某个可调字段的取值超出其合法区间
- **WHEN** 加载该配置文件
- **THEN** 该字段的生效值 MUST 被钳制到合法区间的边界
- **AND** MUST 产生一条告警
- **AND** 进程 MUST 正常启动

#### Scenario: 未知字段被忽略并告警

- **GIVEN** 配置文件中包含一个不属于任何已知字段的键
- **WHEN** 加载该配置文件
- **THEN** 该键 MUST 被忽略
- **AND** MUST 产生一条告警
- **AND** 其余已知字段 MUST 正常生效

#### Scenario: JSON 语法错误报错退出

- **GIVEN** 配置文件内容不是合法 JSON
- **WHEN** 加载该配置文件
- **THEN** 加载 MUST 返回错误
- **AND** 进程 MUST 不以该次加载的结果继续启动

#### Scenario: 不认识的 version 报错退出

- **GIVEN** 配置文件的 `version` 字段不是当前实现认识的版本号
- **WHEN** 加载该配置文件
- **THEN** 加载 MUST 返回错误
- **AND** 进程 MUST 不以该次加载的结果继续启动

### Requirement: 单次推进内参数不变

一次固定步或一次权威 tick 内，参数 MUST 全程使用同一份快照，MUST NOT 中途变化。

#### Scenario: 固定步执行期间参数变更不影响本次结果

- **GIVEN** 一次物理固定步正在使用某份参数快照推进
- **WHEN** 在该固定步执行期间参数被修改
- **THEN** 本次固定步的结果 MUST 仍然基于修改前的那份快照

#### Scenario: 权威 tick 内所有判定共用同一份快照

- **GIVEN** 一次权威模拟 tick 已经取得参数快照
- **WHEN** 该 tick 内发生多处依赖可调参数的判定
- **THEN** 这些判定 MUST 全部使用同一份快照
- **AND** 即使参数在该 tick 期间被修改，本次 tick 的判定结果 MUST NOT 因此改变

### Requirement: 联机时权威参数只读

连接远程服务端时，客户端 MUST NOT 修改物理与模拟参数。

#### Scenario: 联机时拒绝修改物理与模拟参数

- **GIVEN** 客户端已连接到远程服务端
- **WHEN** 用户尝试通过调试面板修改物理或模拟分组中的参数
- **THEN** 该次修改 MUST NOT 生效
- **AND** 物理与模拟的生效参数 MUST 保持不变

### Requirement: 自动化验证不受本机配置影响

性能门禁与抓帧比对路径 MUST 使用编译默认值，MUST NOT 读取用户配置文件。

#### Scenario: 性能门禁路径忽略本机配置文件

- **GIVEN** 本机存在一份修改过默认值的配置文件
- **WHEN** 以性能门禁模式启动
- **THEN** 生效参数 MUST 等于编译默认值
- **AND** MUST NOT 读取该配置文件

#### Scenario: 抓帧比对路径忽略本机配置文件

- **GIVEN** 本机存在一份修改过默认值的配置文件
- **WHEN** 以抓帧比对模式启动
- **THEN** 生效参数 MUST 等于编译默认值
- **AND** MUST NOT 读取该配置文件

### Requirement: 唯一读取入口

物理与模拟的可调参数 MUST 只能通过快照读取，MUST NOT 另有可直接读到编译期值的导出入口。

#### Scenario: 可调参数不再以导出常量暴露

- **GIVEN** 某个物理或模拟参数已被纳入可调参数集合
- **WHEN** 检查该参数所在包的公开符号
- **THEN** MUST NOT 存在一个导出常量或变量直接暴露该参数的编译期值
- **AND** 读取该参数的唯一方式 MUST 是取当前生效参数的快照

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
