# texture-pack-loading Specification

## Purpose

定义 Mornlea 客户端在启动时加载本地 16×16 目录材质包、逐层回退并拒绝材质语义重映射的稳定行为边界。

## Requirements

### Requirement: 客户端只在启动时加载目录材质包

图形客户端 SHALL 接受一个可选的本地目录材质包，并 MUST 只在启动阶段读取它。有效材质包 MUST 包含格式版本为 `1` 且名称非空的 manifest；材质文件 MAY 只提供部分已知逻辑 layer。启动完成后的文件变化 MUST NOT 改变当前进程使用的材质。

#### Scenario: 有效目录覆盖在启动时生效

- **GIVEN** 用户显式配置了一个有效的 v1 目录材质包，其中提供一个已知逻辑 layer 的材质
- **WHEN** 图形客户端启动完成
- **THEN** 该 layer MUST 使用用户目录中的像素
- **AND** 启动完成后修改目录内容 MUST NOT 改变当前进程的材质

#### Scenario: 未配置目录时不访问用户文件系统

- **GIVEN** 用户没有配置材质包目录
- **WHEN** 图形客户端启动
- **THEN** 客户端 MUST 使用内嵌默认与程序化回退
- **AND** MUST NOT 尝试打开用户材质目录

### Requirement: 材质覆盖逐层回退且不得重映射

客户端 MUST 按程序化基线、内嵌默认、用户目录的固定顺序逐层覆盖；用户目录中缺失的已知 layer MUST 保留内嵌默认结果，内嵌默认中缺失的 layer MUST 保留程序化结果。材质包 MUST NOT 改变 layer 编号、方块面映射、透明分类或已知逻辑名到 layer 的映射，未知文件 MUST 被忽略。

#### Scenario: 部分用户包逐层回退

- **GIVEN** 用户材质包只提供部分已知 layer，且内嵌默认也只映射现有 layer 的一个子集
- **WHEN** 客户端构造完整材质集合
- **THEN** 用户提供的 layer MUST 使用用户像素
- **AND** 用户缺失但内嵌默认提供的 layer MUST 使用内嵌像素
- **AND** 两层都缺失的 layer MUST 使用程序化像素

#### Scenario: 目录内容不能重排材质语义

- **GIVEN** 材质目录包含未知文件或额外元数据
- **WHEN** 客户端加载该目录
- **THEN** 既有 layer 编号、方块面映射和透明分类 MUST 保持不变
- **AND** 未知文件 MUST NOT 成为新的材质 layer 或映射来源

### Requirement: 材质输入固定有界并规范化为 RGBA

每个已知材质输入 MUST 是恰好 16×16 像素且不超过既定文件上限的 PNG；客户端 MUST 将 PNG 支持的颜色模型规范化为固定 8-bit RGBA layer。manifest 与材质文件的读取 MUST 有固定上限，任何存在但无法读取、超限、损坏或尺寸不符的已知文件 MUST 使该材质包失败。

#### Scenario: 支持的 16×16 PNG 规范化为固定 layer

- **GIVEN** 一个有效材质包提供采用 PNG 支持颜色模型的 16×16 图像
- **WHEN** 客户端加载该 layer
- **THEN** 输出 MUST 是长度固定为 `16×16×4` 的 8-bit RGBA 像素

#### Scenario: 越界或损坏的已知输入被拒绝

- **GIVEN** manifest 或一个存在的已知材质文件不可读、超限、损坏，或图像尺寸不是 16×16
- **WHEN** 客户端加载该材质包
- **THEN** 加载 MUST 返回包含材质包及逻辑文件上下文的错误
- **AND** MUST NOT 暴露部分应用的材质集合

### Requirement: 显式无效配置在客户端副作用前失败

显式配置的材质包无效时，客户端 MUST 返回启动错误，MUST NOT 静默回退，并 MUST 在创建窗口、打开世界存储、建立网络连接或构造权威 host 之前停止。专用服务端 MUST NOT 打开、校验或分发材质包。

#### Scenario: 无效用户包阻止任何客户端外部副作用

- **GIVEN** 用户显式配置的材质包无效
- **WHEN** 启动本地或远程图形客户端
- **THEN** 客户端 MUST 返回材质包错误
- **AND** MUST NOT 创建窗口、打开存储、连接网络或构造权威 host

#### Scenario: 专用服务端忽略客户端材质配置

- **GIVEN** 配置包含 `texturePackPath`
- **WHEN** 启动专用服务端
- **THEN** 服务端 MUST NOT 打开或校验该路径
- **AND** 网络与持久化内容 MUST NOT 包含材质像素或材质包元数据
