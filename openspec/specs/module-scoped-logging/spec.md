# module-scoped-logging Specification

## Purpose
使运行中的客户端与专用服务端能够按子系统分别控制日志详细程度，而不必在"全部静音"和"全部刷屏"之间二选一。
## Requirements
### Requirement: 全局等级过滤

进程 SHALL 有一个全局日志等级。低于该等级的日志记录 MUST NOT 出现在输出中。

#### Scenario: 低于全局等级的记录被丢弃

- **GIVEN** 全局等级为 info
- **WHEN** 某处产生一条 debug 记录
- **THEN** 输出中 MUST NOT 包含该记录

### Requirement: 按模块覆盖等级

配置 SHALL 能为单个模块指定不同于全局等级的等级。该模块的记录 MUST 按其自身等级过滤，其余模块 MUST 不受影响。

模块的归属 MUST 由记录产生的位置决定，MUST NOT 要求日志调用点显式声明模块。

#### Scenario: 单模块放宽

- **GIVEN** 全局等级为 info，模块 gfx 的等级为 debug
- **WHEN** gfx 与 sim 各产生一条 debug 记录
- **THEN** 输出 MUST 包含 gfx 的那条
- **AND** 输出 MUST NOT 包含 sim 的那条

#### Scenario: 单模块收紧

- **GIVEN** 全局等级为 info，模块 storage 的等级为 error
- **WHEN** storage 产生一条 warn 记录
- **THEN** 输出 MUST NOT 包含该记录

### Requirement: 全局关闭时不承担识别代价

当没有任何模块被放宽到低于全局等级时，低于全局等级的记录 MUST 在不进行模块识别的情况下被丢弃。

#### Scenario: 全局等级下无放宽模块时快速丢弃

- **GIVEN** 全局等级为 info，且没有任何模块的等级被设置为低于 info
- **WHEN** 某处产生一条 debug 记录
- **THEN** 该记录 MUST 在不确定其所属模块的情况下被丢弃
