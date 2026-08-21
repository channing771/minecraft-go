# rust-engine-lod-shell Delta Spec

## Purpose

新增确定性远环 LOD:客户端经 v23 登录取得世界种子后(变基重编,原 v18),
本地生成同步半径外的
纯地表壳 mesh,以距离雾掩盖精度接缝,把可视距离扩展到配置倍数;近环权威
语义逐位不变,远环不反映同步半径外的人工修改(种子即真相)。

## ADDED Requirements

### Requirement: 远环壳生成是确定性纯函数

`mornlea_engine` MUST 以无状态纯函数导出 `mornlea_lod_shell`:同 perm
播种 + 同 tile + 同 LOD 步长的调用 MUST 在全平台产生逐位一致的输出;输出
MUST 为壳 quad 流(世界坐标顶面 + 高度断差侧裙),步长窗内列高 MUST 取
窗内最高列(保守遮挡),材质 MUST 取该最高列的 worldgen 表层材质;流体
开启(材料表 water != air)时,固体顶面低于海平面(64)的窗口 MUST 把
顶面钳到海平面并取水材质——水窗等高故水下 MUST NOT 产生裙边,陆海断差
由陆侧按钳制后高度发裙;流体关闭(water == air)时钳制 MUST 整体跳过,
输出与注水门控引入前逐位一致;status
与两段式容量探测语义 MUST 与既有 engine 导出一致。

#### Scenario: 同输入跨平台逐位一致

- GIVEN 固定 seed 播种、tile 原点与步长
- WHEN 在任意平台调用 `mornlea_lod_shell` 并比对输出字节
- THEN 输出逐位一致,且确定性 golden 回归测试通过

#### Scenario: ABI 版本不匹配被拒绝

- GIVEN abi_version 不等于当前 engine ABI 版本
- WHEN 调用 `mornlea_lod_shell`
- THEN 返回 `MORNLEA_STATUS_ABI_VERSION`,不写入任何输出

#### Scenario: 非法步长与两段式容量探测

- GIVEN 步长不在 {2,4,8} 或 tile 参数越界
- WHEN 调用 `mornlea_lod_shell`
- THEN 返回 `INVALID_ARGUMENT`/`INPUT` 类 status,不产生部分输出
- GIVEN 合法输入但输出缓冲容量不足
- WHEN 调用 `mornlea_lod_shell`
- THEN 返回 overflow status 并报告所需容量;调用方扩容后重试 MUST 成功

#### Scenario: 壳高度与 worldgen 一致且构造无洞

- GIVEN 任意 tile 与步长
- WHEN 以 `mornlea_worldgen_probe` 逐列采样并与壳窗高比对
- THEN 每个窗高 == 窗内 max(worldgen 列高) 经海平面钳制(低于 64 的窗
  取 64 与水材质),每个顶面窗恰好覆盖一次,
  断差边界裙边闭合(边界遍历无洞)

### Requirement: 世界种子经登录成功下发

服务端 MUST 在登录成功应答中携带 `WorldSeed`;单机内置服务端与 TCP 专用
服务端 MUST 走同一编码;协议版本 MUST 升到 v23(v22 已被在飞的
authoritative-farming 占用;变基重编,原编号 v18),
版本不匹配的握手 MUST 被既有拒绝机制拒绝,不产生半兼容会话。

#### Scenario: v23 登录携带种子

- GIVEN 客户端以 v23 协议完成登录
- WHEN 解码登录成功应答
- THEN 取得 `WorldSeed` 并完成远环播种,无需任何额外往返

#### Scenario: 旧版本握手被拒绝

- GIVEN v23 服务端与非 v23 客户端(或反向组合)
- WHEN 执行登录握手
- THEN 握手被版本校验拒绝,连接不进入游戏会话

### Requirement: 远环呈现与距离雾

darwin 客户端 MUST 以独立上传入口与独立 pipeline 呈现远环壳(天空 →
远环 → 近环 → 实体/HUD 帧序,远环写深度);远环 MUST 按相机距离向天空色
衰减,最外缘带全雾,雾色与昼夜 tint 同源;确定性橡树与其他非地表几何
MUST NOT 出现在远环;近环的权威同步、mesh、光照、交互行为 MUST 与远环
禁用时逐位一致。

#### Scenario: 远景入画且近环语义不变

- GIVEN 启用远环的客户端与禁用远环的同种子客户端
- WHEN 在同一位置观察并比对
- THEN 前者呈现超出近环半径的地表壳,视野不再在近环边缘截断;近环内
  方块、光照与交互行为与后者一致

#### Scenario: 雾掩盖精度接缝与外缘

- GIVEN 远环渲染中
- WHEN 观察近/远环过渡带与远环最外缘
- THEN 过渡带与外缘由距离雾衰减到天空色,无可见裂缝或突兀截断

### Requirement: 远环编辑语义为种子即真相

远环 MUST NOT 反映同步半径外的人工修改;远环几何 MUST 仅由种子派生。
近环内的修改照常由权威同步呈现。

#### Scenario: 同步半径外的修改不出现在远环

- GIVEN 另一玩家在本地同步半径之外放置方块
- WHEN 本地客户端渲染远环
- THEN 远环仍呈现种子生成的地表,该方块不出现;两客户端相互进入近环后
  修改照常呈现

### Requirement: 远环 tile 调度与独立预算

客户端远环调度 MUST 按与中心距离从近到远处理 tile,重复入队 MUST 覆盖
旧值,超出远环半径的 tile MUST 被丢弃并释放渲染资源;远环生成与上传
MUST 使用独立帧预算,预算耗尽即停,且 MUST NOT 减少近环 section 上传
可用的预算。

#### Scenario: 预算内由近到远且不挤压近环

- GIVEN 大量待处理远环 tile 与本帧近环 section 上传
- WHEN 帧预算冲刷
- THEN 远环 tile 按距离升序处理,预算耗尽即停;近环 SectionScheduler
  的上传行为与远环禁用时一致

#### Scenario: 界外 tile 被丢弃并释放

- GIVEN 玩家移动使若干 tile 超出远环半径
- WHEN 调度器冲刷
- THEN 界外 pending 被丢弃,对应渲染器资源被释放,后续帧不再呈现该 tile

### Requirement: 配置与基准可比性

`lodEnabled`、`lodFarMultiplier`(默认 3,范围 2..8)与 `lodStep`
(默认 4)MUST 作为配置调参暴露;benchmark producer MUST 默认禁用远环,
benchmark scenario MUST 保持 v17(变基后与 main 一致)且输出结构不变;
既有 capture 场景的 golden 更新 MUST 仅包含新增远景带与 main 注水地形引入
的变化(近处不变双侧守卫),并 MUST 新增 `far-horizon` 视觉场景
作为长期门禁;`water-underwater` MUST 保持在场景表最后,`far-horizon`
MUST 排在其之前(倒数第二)。

#### Scenario: benchmark 保持可比

- GIVEN benchmark producer 运行
- WHEN 产出基准报告
- THEN 远环未参与,scenario 为 v17,报告结构与既有基准可比

#### Scenario: golden 更新仅限远景带

- GIVEN 既有全部 capture 场景
- WHEN 重新生成 golden
- THEN 每个场景的变化仅出现在新增远景带,近处像素不变;`far-horizon`
  场景与 golden 入库并通过比对
