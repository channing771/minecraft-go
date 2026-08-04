# M4F 权威工具与计时采掘实施计划

> **供执行代理：** 必须使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，逐任务执行本计划；所有步骤使用复选框（- [ ]）跟踪。

**目标：** 在现有 20 Hz 服务端权威循环中加入石镐、铁镐和按住左键计时采掘，并让协议、多人确定性、掉落原子性、HUD、存档兼容和 scenario v10 性能门禁形成完整闭环。

**架构：** 采掘意图只作为 PlayerInput 的一个持续布尔值进入 simulation owner；sim 每 tick 使用移动后的权威位置、视角、选中物品和六格射线推进每玩家独立的固定状态。完成破坏复用现有区块、掉落物和熔炉原子预演，server 只把 sim 定长值映射进 PlayerState，客户端只渲染最后确认的状态。

**技术栈：** Go 1.26、现有 Memory/TCP 协议与 binary codec、simulation owner 单写循环、现有 WebGPU hotbar pipeline、OpenSpec、Go test/race/fuzz/benchmark、cmd/perfcheck。

## 全局约束

- [ ] 全程通过 zsh -ic 'gvm use go1.26.0 >/dev/null && ...' 使用现有 GVM Go；不得下载或安装另一份 Go。
- [ ] 自动验证不得启动或聚焦游戏窗口；只运行 Go 测试、fuzz、benchmark、perfcheck 和已有无窗口 benchmark 路径。
- [ ] 每个任务严格执行 RED → GREEN → REFACTOR → 验证 → 单独提交；一个任务通过后自动进入下一个。
- [ ] 保留用户未跟踪目录 midscene_run/，任何提交都不得暂存它。
- [ ] 不新增包、依赖、通用物品注册表、metadata、耐久、木材链、共享 damage map、每目标 goroutine、裂纹纹理或客户端采掘预测。
- [ ] 所有新增或修改的开发者注释、测试说明和文档使用中文；Go 标识符和稳定 wire 名称保留英文。
- [ ] 不修改玩家 schema v3、区块 schema v4、M2 基线字节、既有绝对门禁或 20% 相对阈值。
- [ ] 旧 BreakBlock 只为保持任务 2～4 的中间提交可编译而暂留；任务 5 必须删除类型、codec、registry、server 翻译、sim command 和全部调用方，并断言 client packet ID 1 未分配。
- [ ] Hook 失败只修根因，不关闭、改写或绕过 guard。

---

## 文件与接口映射

### Core 契约

文件：

- 修改：internal/core/item.go
- 修改：internal/core/inventory.go
- 修改：internal/core/recipe.go
- 测试：internal/core/item_test.go
- 测试：internal/core/inventory_test.go
- 测试：internal/core/recipe_test.go
- 测试：internal/storage/player_codec_test.go

稳定新增项：

    const (
        ItemStonePickaxe ItemID = 10
        ItemIronPickaxe  ItemID = 11
    )

    const (
        RecipeStonePickaxe RecipeID = 4
        RecipeIronPickaxe  RecipeID = 5
    )

    func ItemStackLimit(item ItemID) (uint8, bool)

ItemStackLimit 是唯一新增的物品规则：既有九种物品返回 64，两把镐返回 1，ItemNone 或未知 ID 返回 0,false。ItemStack.Valid、Hotbar.Add、Inventory.AddStack 和 Inventory.MoveStack 直接使用它，不引入注册表对象或接口。

### Wire 契约

文件：

- 修改：internal/network/message.go
- 修改：internal/network/codec.go
- 修改：internal/network/packet.go
- 修改：internal/network/registry.go
- 测试：internal/network/message_test.go
- 测试：internal/network/codec_test.go
- 测试：internal/network/packet_test.go
- 测试：internal/network/registry_test.go
- 测试：internal/network/login_test.go
- 测试：internal/network/benchmark_test.go

任务 5 后的稳定消息结构：

    type PlayerInput struct {
        Sequence uint64
        MoveX    int8
        MoveZ    int8
        Jump     bool
        Yaw      float32
        Pitch    float32
        Mining   bool
    }

    type PlayerState struct {
        ServerTick          uint64
        LastInputSequence   uint64
        Dimension           core.DimensionID
        Position            mgl32.Vec3
        Velocity            mgl32.Vec3
        Yaw, Pitch          float32
        OnGround            bool
        Ready               bool
        Reset               bool
        MiningActive        bool
        MiningTarget        core.BlockPos
        MiningProgressTicks uint16
        MiningRequiredTicks uint16
        MiningHarvestable   bool
    }

PlayerState.Validate 强制以下两种规范形式之一：

    inactive := !state.MiningActive &&
        state.MiningTarget == (core.BlockPos{}) &&
        state.MiningProgressTicks == 0 &&
        state.MiningRequiredTicks == 0 &&
        !state.MiningHarvestable

    active := state.MiningActive &&
        state.MiningProgressTicks >= 1 &&
        state.MiningProgressTicks < state.MiningRequiredTicks

Ready=false 或 Reset=true 时必须使用 inactive 形式；active 目标的 Y 必须位于 core.MinY..core.MaxY-1。

ProtocolVersion 升为 8。PlayerInput 追加一个规范 bool 字节；PlayerState 依次追加 active、目标 X/Y/Z、progress、required 和 harvestable。Play 客户端 ID 0 与 2..10 保持冻结，任务 5 后 ID 1 为未知值。

### Simulation 契约

文件：

- 新增：internal/sim/mining.go
- 新增：internal/sim/mining_test.go
- 修改：internal/sim/command.go
- 修改：internal/sim/player.go
- 修改：internal/sim/engine.go
- 修改：internal/sim/bench_test.go
- 迁移：internal/sim/interaction_test.go
- 迁移：internal/sim/player_interaction_test.go
- 迁移：internal/sim/drop_test.go
- 迁移：internal/sim/furnace_inventory_test.go
- 迁移：internal/sim/hotbar_test.go
- 迁移：internal/sim/movement_test.go
- 迁移：internal/sim/engine_test.go
- 迁移：internal/sim/persistence_lifecycle_test.go

新增的 sim 内部发布值：

    type MiningUpdate struct {
        Active        bool
        Target        core.BlockPos
        ProgressTicks uint16
        RequiredTicks uint16
        Harvestable   bool
    }

    type PlayerUpdate struct {
        // existing fields
        Mining MiningUpdate
    }

内部定长状态与入口：

    type playerMiningState struct {
        target        core.BlockPos
        block         core.BlockID
        tool          core.ItemID
        progressTicks uint16
        requiredTicks uint16
        harvestable   bool
    }

    func miningRule(block core.BlockID, held core.ItemID) (requiredTicks uint16, harvestable bool)

    func (engine *Engine) advanceMining(
        pending map[core.ChunkKey]*pendingChunkChanges,
        result *TickResult,
    )

    func (engine *Engine) completeMining(
        dimension core.DimensionID,
        target core.BlockPos,
        block core.BlockID,
        harvestable bool,
        pending map[core.ChunkKey]*pendingChunkChanges,
    ) (RejectReason, bool)

playerState 增加 miningHeld bool 和 mining playerMiningState，Command 增加 Mining bool。任何采掘字段都不得进入 physics.Input、PlayerSnapshot、存储 DTO 或磁盘 codec。

### 客户端与渲染契约

文件：

- 修改：internal/client/input.go
- 修改：internal/client/predictor.go
- 修改：cmd/mcgo/main.go
- 修改：cmd/mcgo/app.go
- 修改：internal/render/hotbar.go
- 测试：internal/client/input_test.go
- 测试：internal/client/predictor_test.go
- 测试：cmd/mcgo/main_test.go
- 测试：cmd/mcgo/app_test.go
- 测试：internal/render/hotbar_test.go

输入与渲染值：

    type Actions struct {
        Mining         bool
        Place          bool
        Select         bool
        SelectSlot     uint8
        ToggleInventory bool
        Click          bool
    }

    type Control struct {
        MoveX int8
        MoveZ int8
        Jump  bool
        Yaw   float32
        Pitch float32
        Mining bool
    }

    type MiningOverlay struct {
        Active        bool
        ProgressTicks uint16
        RequiredTicks uint16
        Harvestable   bool
    }

    func (renderer *HotbarRenderer) Prepare(
        inventory core.Inventory,
        open bool,
        source int,
        furnace *FurnaceOverlay,
        mining MiningOverlay,
        width, height uint32,
        budget *UploadBudget,
    ) error

application 保存一个 render.MiningOverlay，且只从通过验证的 PlayerState 填充。inactive、reset 和 closeClientSession 都将其清零；它从不读取当前鼠标状态。

---

## 任务 1：加入固定工具、堆叠上限与配方

OpenSpec 覆盖：1.1–1.3。

**文件：**

- 修改：internal/core/item_test.go
- 修改：internal/core/inventory_test.go
- 修改：internal/core/recipe_test.go
- 修改：internal/storage/player_codec_test.go
- 修改：internal/core/item.go
- 修改：internal/core/inventory.go
- 修改：internal/core/recipe.go

- [ ] 步骤 1：先写稳定 ID 与上限的失败测试。

加入表驱动断言：

    func TestToolIDsAndStackLimitsAreStable(t *testing.T) {
        if core.ItemStonePickaxe != 10 || core.ItemIronPickaxe != 11 {
            t.Fatalf("工具 ID 发生变化: stone=%d iron=%d",
                core.ItemStonePickaxe, core.ItemIronPickaxe)
        }
        tests := []struct {
            item core.ItemID
            want uint8
            ok   bool
        }{
            {core.ItemStone, 64, true},
            {core.ItemIronBlock, 64, true},
            {core.ItemStonePickaxe, 1, true},
            {core.ItemIronPickaxe, 1, true},
            {core.ItemNone, 0, false},
            {core.ItemID(4242), 0, false},
        }
        for _, test := range tests {
            got, ok := core.ItemStackLimit(test.item)
            if got != test.want || ok != test.ok {
                t.Errorf("ItemStackLimit(%d)=(%d,%v)，想要 (%d,%v)",
                    test.item, got, ok, test.want, test.ok)
            }
        }
    }

同时断言 ItemStack{tool,2} 非法而 ItemStack{tool,1} 合法。

- [ ] 步骤 2：运行 RED 检查。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -run "ToolIDs|StackLimits" -count=1'

预期：工具 ID 和 ItemStackLimit 尚不存在，因此编译失败。

- [ ] 步骤 3：加入最小物品规则。

在末尾追加两个 ID 并实现：

    func ItemStackLimit(item ItemID) (uint8, bool) {
        switch item {
        case ItemStone, ItemDirt, ItemGrass, ItemStoneBrick, ItemCoal,
            ItemRawIron, ItemIronIngot, ItemFurnace, ItemIronBlock:
            return MaxStackCount, true
        case ItemStonePickaxe, ItemIronPickaxe:
            return 1, true
        default:
            return 0, false
        }
    }

让 RegisteredItem 委托 ItemStackLimit；ItemStack.Valid 用返回的 limit 校验 Count；Hotbar.Add 仅在 Count 小于该物品上限时合并。

- [ ] 步骤 4：先写物品栏移动的失败测试。

覆盖：

- adding a second stone pickaxe uses the next empty slot;
- moving a pickaxe onto the same pickaxe returns false;
- moving unlike tools swaps;
- ordinary items still merge to 64;
- SetSlot rejects a two-count tool stack.

- [ ] 步骤 5：更新 Inventory.fillPhase 与 MoveStack。

只查询一次来源物品上限，并替换两处 MaxStackCount 计算：

    limit, _ := ItemStackLimit(stack.Item)
    space := limit - current.Count

MoveStack 只在同物品合并分支使用来源物品上限，不加入工具特判。

- [ ] 步骤 6：先写配方失败测试。

断言 ID 4/5、精确配方、跨多个栏位扣除三份原料、产出一把工具，以及 36 格全被占用时原子失败。

- [ ] 步骤 7：追加固定配方。

    case RecipeStonePickaxe:
        return CraftingRecipe{
            Input:  ItemStack{Item: ItemStone, Count: 3},
            Output: ItemStack{Item: ItemStonePickaxe, Count: 1},
        }, true
    case RecipeIronPickaxe:
        return CraftingRecipe{
            Input:  ItemStack{Item: ItemIronIngot, Count: 3},
            Output: ItemStack{Item: ItemIronPickaxe, Count: 1},
        }, true

不得改变 CraftingRecipe 结构或 Inventory.Craft 顺序。

- [ ] 步骤 8：在不改变 schema 的前提下扩展玩家 codec 往返测试。

把两把工具加入既有 v3 物品栏 fixture，并断言：

    if currentPlayerSchema != 3 {
        t.Fatalf("玩家 schema=%d，工具没有改变栏位布局", currentPlayerSchema)
    }

保留真正未知 ID 4242 的拒绝测试。在测试注释中说明：M4E 二进制的注册范围止于 9，因此同样会拒绝 ID 10/11；不得复制一份冻结旧 decoder。

- [ ] 步骤 9：运行聚焦验证与结构验证。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/storage -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

预期：全部测试通过，gofmt 与 diff check 均无输出。

- [ ] 步骤 10：只提交任务 1 文件。

    git add internal/core/item.go internal/core/inventory.go internal/core/recipe.go internal/core/item_test.go internal/core/inventory_test.go internal/core/recipe_test.go internal/storage/player_codec_test.go
    git commit -m "feat: 定义采掘工具与固定配方"

---

## 任务 2：加入协议 v8 字段并保持中间提交可编译

OpenSpec 覆盖：2.1–2.2 的协议字段部分；ID 1 移除项到任务 5 才勾选。

**文件：**

- 修改：internal/network/message_test.go
- 修改：internal/network/codec_test.go
- 修改：internal/network/packet_test.go
- 修改：internal/network/login_test.go
- 修改：internal/network/benchmark_test.go
- 修改：internal/network/message.go
- 修改：internal/network/codec.go
- 修改：internal/network/packet.go

- [ ] 步骤 1：先写 PlayerInput 与 PlayerState 规范形式的失败测试。

加入 Mining=true 的输入 fixture，并加入以下 PlayerState 用例：

    valid := network.PlayerState{
        Dimension:           core.Overworld,
        MiningActive:        true,
        MiningTarget:        core.BlockPos{X: 1, Y: 2, Z: 3},
        MiningProgressTicks: 6,
        MiningRequiredTicks: 15,
        MiningHarvestable:   true,
    }

拒绝 inactive 但 target/progress/required/harvestable 非零、progress 为零、progress 等于 required 以及 progress 大于 required 的状态。

- [ ] 步骤 2：运行 RED 检查。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "PlayerInput|PlayerState|Protocol" -count=1'

预期：新增字段尚不存在，因此编译失败。

- [ ] 步骤 3：追加 wire 字段与规范校验。

在 PlayerInput 的 Pitch 后编码 Mining；解码继续通过既有 byteDecoder.bool 拒绝非规范 bool。

在 PlayerState 的 Reset 后按以下精确顺序编码：

    e.bool(message.MiningActive)
    e.i32(message.MiningTarget.X)
    e.i32(message.MiningTarget.Y)
    e.i32(message.MiningTarget.Z)
    e.u16(message.MiningProgressTicks)
    e.u16(message.MiningRequiredTicks)
    e.bool(message.MiningHarvestable)

按相同顺序解码；本任务不改变 packet ID。

- [ ] 步骤 4：替换精确 codec golden。

根据文档字段顺序独立生成期望字节，再把字面量 hex 冻结到 codec_test.go。至少包含一个全零 inactive 状态和一个 active 6/15 状态；不得调用生产 encoder 计算 golden。

- [ ] 步骤 5：把 ProtocolVersion 升为 8，并断言 v7 握手被拒绝。

同步更新 ServerHello 与 ClientHello 期望。新增登录/握手测试：发送版本 7，并在进入 StatePlay 前收到既有 unsupported-version 拒绝。

- [ ] 步骤 6：让网络 benchmark 使用 v8 payload。

PlayerInput benchmark 使用 Mining=true；PlayerState benchmark 使用规范 active 采掘状态，使 payload 大小与分配结果覆盖新增字段。

- [ ] 步骤 7：运行协议验证。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

- [ ] 步骤 8：提交任务 2。

    git add internal/network
    git commit -m "feat: 扩展权威采掘协议 v8"

---

## 任务 3：把持续采掘输入传入 simulation owner

OpenSpec 覆盖：3.1–3.2 的输入部分。旧即时路径只为中间提交兼容而保留到任务 5，客户端不再调用它。

**文件：**

- 修改：internal/client/input_test.go
- 修改：internal/client/predictor_test.go
- 修改：cmd/mcgo/main_test.go
- 修改：internal/server/player_test.go
- 修改：internal/client/input.go
- 修改：internal/client/predictor.go
- 修改：cmd/mcgo/main.go
- 修改：internal/server/session.go
- 修改：internal/sim/command.go
- 修改：internal/sim/engine.go

- [ ] 步骤 1：先写输入路由失败测试。

验证连续三帧按住主键都返回 Mining=true，松开返回 false；inventoryOpen 时只有主键上升沿进入 Click，Mining 始终为 false。

核心断言：

    first := state.Update(true, false, 0, false, false)
    held := state.Update(true, false, 0, false, false)
    if !first.Mining || !held.Mining {
        t.Fatalf("按住左键未保持采掘: first=%+v held=%+v", first, held)
    }

- [ ] 步骤 2：先写 predictor 失败测试。

断言普通固定步把 Control.Mining 复制到每个 PlayerInput；即使 Control.Mining 为 true，suspended/history-full 的 neutral 输入仍必须 Mining=false。

- [ ] 步骤 3：实现客户端值流。

把 Actions.Break 改为 Actions.Mining，增加 Control.Mining。只在物品栏关闭时设置 actions.Mining=primary；普通 PlayerInput 带上 Mining，sendNeutral 保持零值。

在 applyInteractiveInput 中移除 breakBlock 调用并构造：

    control := client.Control{
        MoveX:  movement.MoveX,
        MoveZ:  movement.MoveZ,
        Jump:   movement.Jump,
        Yaw:    a.camera.Yaw,
        Pitch:  a.camera.Pitch,
        Mining: allowActions && actions.Mining,
    }

本任务暂不删除 application.breakBlock；任务 5 与其余调用方和测试一起删除。

- [ ] 步骤 4：先写服务端翻译失败测试。

发送 network.PlayerInput{Mining:true}，期望 sim.Command{Kind:CommandPlayerInput, Mining:true}。非法旋转经 sim 校验后，玩家保留状态中的 Mining 必须为 false。

- [ ] 步骤 5：在 sim.Command 与翻译中加入 Mining。

在 translateClientMessage 中映射 PlayerInput.Mining；CommandPlayerInput 处理如下：

    player.lastInputSequence = command.Sequence
    if !validPlayerInput(command) {
        player.input = physics.Input{Yaw: player.yaw}
        player.miningHeld = false
        // existing rejection
        continue
    }
    player.miningHeld = command.Mining

本任务只增加 miningHeld；进度值到任务 4 才出现，因此不需要临时状态类型。

- [ ] 步骤 6：在不启动前台客户端的情况下验证持续输入。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/client ./internal/server ./cmd/mcgo -run "Input|Predictor|Translate|Interactive" -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

- [ ] 步骤 7：提交任务 3。

    git add internal/client/input.go internal/client/input_test.go internal/client/predictor.go internal/client/predictor_test.go cmd/mcgo/main.go cmd/mcgo/main_test.go internal/server/session.go internal/server/player_test.go internal/sim/command.go internal/sim/engine.go internal/sim/player.go
    git commit -m "feat: 接通持续采掘输入"

---

## 任务 4：实现固定权威进度与原子完成

OpenSpec 覆盖：4.1–4.4，以及 5.1–5.2 的新完成行为。

**文件：**

- 新增：internal/sim/mining.go
- 新增：internal/sim/mining_test.go
- 修改：internal/sim/player.go
- 修改：internal/sim/engine.go
- 修改：internal/sim/bench_test.go

- [ ] 步骤 1：先写完整规则表的失败测试。

用一张表列出预期 required ticks 与 harvestable：

    tests := []struct {
        block core.BlockID
        held  core.ItemID
        ticks uint16
        drop  bool
    }{
        {core.DirtID, core.ItemNone, 5, true},
        {core.GrassID, core.ItemDirt, 5, true},
        {core.StoneID, core.ItemNone, 30, true},
        {core.StoneID, core.ItemDirt, 30, false},
        {core.StoneID, core.ItemStonePickaxe, 15, true},
        {core.StoneID, core.ItemIronPickaxe, 8, true},
        {core.StoneBrickID, core.ItemNone, 30, false},
        {core.StoneBrickID, core.ItemStonePickaxe, 15, true},
        {core.FurnaceID, core.ItemIronPickaxe, 8, true},
        {core.CoalOreID, core.ItemStonePickaxe, 15, true},
        {core.IronOreID, core.ItemIronPickaxe, 8, true},
        {core.IronBlockID, core.ItemNone, 40, false},
        {core.IronBlockID, core.ItemStonePickaxe, 20, false},
        {core.IronBlockID, core.ItemIronPickaxe, 10, true},
        {core.BedrockID, core.ItemIronPickaxe, 0, false},
    }

为每种普通手持物对石头补充显式用例，保证它们都不会被视为裸手。

- [ ] 步骤 2：用一个 switch 实现 miningRule。

不加 tier 接口或配置表。基岩、空气和不支持的方块返回 0,false；石头、共用 30/15/8 组和铁块分别使用明确的内层 switch。

- [ ] 步骤 3：先写进度与重置失败测试。

复用既有 Ready 玩家 helper，覆盖：

- first held tick publishes 1/required;
- same target and tool increments exactly once;
- release clears in the same tick;
- yaw target change and selected tool change restart at 1;
- target block ID replacement restarts at 1;
- out-of-range, missing chunk, view loss, open furnace, reset and bedrock clear without Rejected entries;
- eight sessions keep independent states and publish in sorted order.

仅检查进度的用例使用需要 30 tick 的石头，确保每个断言都在完成前停止。

- [ ] 步骤 4：加入 MiningUpdate 与玩家发布。

实现：

    func (state playerMiningState) update() MiningUpdate {
        if state.requiredTicks == 0 {
            return MiningUpdate{}
        }
        return MiningUpdate{
            Active:        true,
            Target:        state.target,
            ProgressTicks: state.progressTicks,
            RequiredTicks: state.requiredTicks,
            Harvestable:   state.harvestable,
        }
    }

PlayerUpdate.Mining 接收 player.mining.update()。beginReset、非法输入和未就绪路径都清零 miningHeld 与 mining；Unregister 会删除整个 playerState，不需要额外处理。

- [ ] 步骤 5：先写原子完成失败测试。

覆盖：

- bare hand destroys stone at tick 30 and creates one stone drop;
- an ordinary item destroys stone at tick 30 without a drop;
- stone pickaxe destroys ores at tick 15 with the correct item;
- wrong tool destroys ores without a drop or capacity reservation;
- stone pickaxe destroys iron block at tick 20 without a drop;
- iron pickaxe destroys iron block at tick 10 with a drop;
- full drop capacity rejects a harvestable completion once and preserves block, drops and revision;
- wrong-tool ordinary completion succeeds even when drop slots are full;
- wrong-tool furnace drops only non-empty input/fuel/output;
- adequate tool furnace drops body and contents;
- furnace batch capacity failure preserves block, furnace slot, drops and revision;
- two sessions completing one target in the same tick commit one change and one drop.

- [ ] 步骤 6：用既有原子原语实现 completeMining。

普通方块：

    item, ok := core.BlockDrop(block)
    if !ok {
        return RejectProtectedBlock, true
    }
    dropSlot := 0
    if harvestable {
        var capacityOK bool
        dropSlot, capacityOK = record.Chunk.PrepareDrop(item, blockIndex)
        if !capacityOK {
            return RejectDropCapacity, true
        }
    }
    _, changed, err := dimension.SetBlock(target, core.AirID)
    // 映射错误，记录一次变化，仅在 harvestable 时提交掉落。

熔炉复用固定 [4] batch，索引 0 为炉体或空堆：

    stacks := [4]core.ItemStack{
        {},
        furnace.Input,
        furnace.Fuel,
        furnace.Output,
    }
    if harvestable {
        stacks[0] = core.ItemStack{Item: core.ItemFurnace, Count: 1}
    }
    next, ok := record.Chunk.PrepareDropBatch(
        stacks, blockIndex, DropPickupDelayTicks,
    )

PrepareDropBatch 已忽略空项，因此不需要第二个 helper。只有 SetBlock 成功后才记录空气变化、停用熔炉并提交预演 batch。

- [ ] 步骤 7：实现固定八会话遍历。

使用局部定长数组：

    var sessions [8]SessionID
    count := 0

按序把活动玩家 ID 插入 sessions[:count]，不得使用 append、slices.Sort 或闭包。若出现第九名活动玩家，以 sim 不变量文本 panic；server.Config 已拒绝超过八人的配置。

对每个已排序会话：

1. Clear when !miningHeld, reset, !hasView or viewFurnace.
2. Raycast once from authority position plus physics.EyeHeight using current yaw/pitch and interactionReach.
3. Treat no hit and any unreadable chunk as normal clear.
4. Read selected ItemID.
5. Call miningRule.
6. If target, block and tool match, increment; otherwise replace state with progress 1.
7. If progress reaches required, call completeMining, clear progress on success or rejection, and append one rejection using the last valid PlayerInput sequence only when completion fails.

完成时不得把 miningHeld 设为 false。仍按住的输入会在下一 tick 开始新一轮，因此容量失败只有再次走完整时长后才可能再次拒绝。

- [ ] 步骤 8：在正确顺序点加入采掘推进。

Engine.Step 保留既有放置交互，随后调用：

    engine.advanceMining(pending, &result)
    engine.finishChanges(pending, &result)

调用点位于 advanceDrops、advanceFurnaces、熔炉移动和放置之后。旧 BreakBlock 分支只并存到任务 5，以保持既有测试在中间提交中通过。

- [ ] 步骤 9：加入直接的稳定路径分配检查。

mining_test.go 使用 package sim。创建八名玩家并分别对准长时长石头目标，预热 fixture；每次调用后清零私有 mining 值，避免触发完成写入：

    allocations := testing.AllocsPerRun(1000, func() {
        result.Rejected = result.Rejected[:0]
        engine.advanceMining(pending, &result)
        for _, session := range engine.sessions {
            session.player.mining = playerMiningState{}
        }
    })
    if allocations != 0 {
        t.Fatalf("八人采掘每 tick 分配=%f，想要 0", allocations)
    }

断言一次调用后八名玩家都恰好到 progress 1。结合一玩家一状态的遍历结构，可锁定最多八次逻辑射线，且不增加生产 observer hook。

- [ ] 步骤 10：加入 BenchmarkAuthoritativeMiningEightPlayers。

复用同一预热 fixture，并调用 ReportAllocs 和 ResetTimer。benchmark 只调用 advanceMining，使新路径的分配契约不受既有 Engine.Step 结果分配影响。

- [ ] 步骤 11：运行任务 4 验证。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "^$" -bench AuthoritativeMiningEightPlayers -benchmem -count=3'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

- [ ] 步骤 12：提交任务 4。

    git add internal/sim/mining.go internal/sim/mining_test.go internal/sim/player.go internal/sim/engine.go internal/sim/bench_test.go
    git commit -m "feat: 推进权威计时采掘"

---

## 任务 5：退役即时破坏并迁移既有测试集

OpenSpec 覆盖：2.1–2.2、3.1–3.2 和 5.1–5.4 的剩余部分。

**文件：**

- 修改：internal/sim/engine.go
- 修改：internal/sim/command.go
- 修改：internal/network/message.go
- 修改：internal/network/codec.go
- 修改：internal/network/packet.go
- 修改：internal/network/registry.go
- 修改：internal/network/registry_test.go
- 修改：internal/network/codec_test.go
- 修改：internal/network/packet_test.go
- 修改：internal/network/benchmark_test.go
- 修改：internal/server/session.go
- 修改：cmd/mcgo/app.go
- 迁移：文件与接口映射中列出以及 rg 找到的全部 BreakBlock 引用。

- [ ] 步骤 1：从 executeInteraction 抽出放置逻辑。

把 executeInteraction 改名为 executePlacement，并拒绝 CommandPlaceBlock 之外的 kind。删除 break switch 分支，保持权威眼睛位置、六格射线、物品栏预演、熔炉槽预留和重叠检查不变。

命令收集从 break/place 交互改为仅 place：

    case CommandPlaceBlock:
        // existing validation and append to placements

- [ ] 步骤 2：用共享按住输入 helper 迁移旧 break 测试。

只加入测试 helper，不加生产抽象：

    func mineUntilComplete(
        t *testing.T,
        engine *sim.Engine,
        session sim.SessionID,
        sequence *uint64,
        yaw, pitch float32,
        ticks int,
    ) sim.TickResult

它入队一条 Kind=CommandPlayerInput、Mining=true 的 sim.Command，再精确执行 ticks 次 Step 并返回最终结果。需要松键的测试显式入队 Mining=false。保留原测试对权威位置、revision、掉落、持久化和多人顺序的断言。

- [ ] 步骤 3：从所有位置删除旧路径。

删除：

- network.BreakBlock and Validate;
- codec encode/decode case 1;
- registry type and ID mapping for 1;
- ValidateClientPacket case;
- server translateClientMessage case;
- sim.CommandBreakBlock;
- application.breakBlock;
- old benchmark cases and fixtures.

运行：

    rg -n "BreakBlock|CommandBreakBlock|breakBlock\(" --glob='*.go' .

预期：无输出。

- [ ] 步骤 4：把 packet ID 1 冻结为空洞。

Registry 测试必须断言：

    if _, ok := clientPacketForID(network.StatePlay, 1); ok {
        t.Fatal("Play client packet ID 1 必须保持未分配")
    }

同时断言 PlaceBlock 仍为 2、CloseFurnace 仍为 10；decoder 遇到 ID 1 必须返回 errUnknownPacketID。不得重排后续 packet。

- [ ] 步骤 5：运行完整受影响闭包。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/network ./internal/server ./cmd/mcgo -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

预期：全部迁移测试通过，旧路径 rg 无输出。

- [ ] 步骤 6：提交任务 5。

    git add internal/sim internal/network internal/server cmd/mcgo
    git commit -m "feat: 原子完成工具采掘"

---

## 任务 6：通过 Memory 与 TCP 发布规范采掘状态

OpenSpec 覆盖：6.1–6.3。

**文件：**

- 修改：internal/server/publication.go
- 修改：internal/server/publication_test.go
- 修改：internal/server/integration_test.go
- 修改：internal/server/tcp_integration_test.go
- 修改：internal/server/multiplayer_memory_integration_test.go
- 修改：internal/server/multiplayer_tcp_integration_test.go
- 修改：internal/client/predictor_test.go

- [ ] 步骤 1：先写发布映射失败测试。

给定 sim.PlayerUpdate{Mining: MiningUpdate{Active:true,...}}，本地 PlayerState 必须携带全部五个值；远端玩家采掘状态不得出现在 RemotePlayerStates。

- [ ] 步骤 2：在 publishLocalResult 中直接映射 sim 字段。

    MiningActive:        playerUpdate.Mining.Active,
    MiningTarget:        playerUpdate.Mining.Target,
    MiningProgressTicks: playerUpdate.Mining.ProgressTicks,
    MiningRequiredTicks: playerUpdate.Mining.RequiredTicks,
    MiningHarvestable:   playerUpdate.Mining.Harvestable,

不得入队第二条消息，也不得改变远端状态 packet。

- [ ] 步骤 3：编写 Memory 纵向测试。

复用既有无窗口 host/login helper：

1. Wait for Ready and a confirmed inventory.
2. Send PlayerInput with Mining=true and fixed look.
3. Observe progress 1..required-1 only in PlayerState.
4. Observe the final BlockChanges, drop and inactive PlayerState.
5. Repeat for tool switch reset, wrong-tool no-drop, two-player competition, release and disconnect.

断言每个 PlayerState.Validate 都成功。

- [ ] 步骤 4：用同一语义表编写 TCP 纵向测试。

复用既有 tcpIntegration helper，只增加采掘命令/状态断言。覆盖原始 v7 ClientHello 拒绝和原始 Play packet ID 1 协议违规，不复制新 harness。

- [ ] 步骤 5：断言 Memory/TCP 收敛。

在两个 transport 上运行同一确定脚本并比较：

- final chunk hash;
- final player inventory snapshot digest;
- final drop snapshot digest;
- ordered mining progress transcript;
- final inactive canonical state.

不得在 PlayerSnapshot 中持久化或比较进行中的采掘，因为它属于连接期状态。

- [ ] 步骤 6：验证网络生命周期。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/client -run "Mining|ProtocolVersion|UnknownPacket" -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server -run "Multiplayer.*Mining|Mining.*Multiplayer" -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

- [ ] 步骤 7：提交任务 6。

    git add internal/server internal/client/predictor_test.go
    git commit -m "feat: 发布多人权威采掘状态"

---

## 任务 7：渲染五条配方与纯权威采掘条

OpenSpec 覆盖：7.1–7.3。

**文件：**

- 修改：internal/render/hotbar_test.go
- 修改：internal/render/hotbar.go
- 修改：cmd/mcgo/app_test.go
- 修改：cmd/mcgo/app.go

- [ ] 步骤 1：先写五配方布局失败测试。

更新固定容量期望：

    recipeQuads  = 5 * 3
    recipeGlyphs = 5 * 2

断言 RecipeButtonAt 命中 0..4 行并返回 ID 1..5；第 3 行消耗石头，第 4 行消耗铁锭。保留既有固定最坏缓冲断言。

- [ ] 步骤 2：扩展固定配方数组与颜色。

追加 RecipeStonePickaxe 与 RecipeIronPickaxe，在 hotbarItemColor 中增加两种程序化颜色；不得增加纹理或 glyph。

- [ ] 步骤 3：先写采掘 overlay 布局失败测试。

覆盖：

- inactive emits no extra quads;
- active 6/15 emits exactly background plus fill;
- fill width is 40% of the fixed bar width;
- harvestable fill is green;
- non-harvestable fill is orange;
- open inventory suppresses the mining bar;
- required zero is treated as inactive defensively.

- [ ] 步骤 4：加入双 quad overlay。

在既有 HUD 几何常量附近加入固定值：

    miningBarWidth  = float32(240)
    miningBarHeight = float32(12)
    miningBarGap    = float32(16)

appendMiningBar 加入暗色背景，并只在规范正比例下加入一个彩色填充；比例使用 min(progress/required,1)。快捷栏物品布局完成后，仅在 !open 时调用。

物品栏最坏布局已大于关闭快捷栏加两个采掘 quad。五条配方后，maxHotbarQuads 继续使用基于 maxOverlayQuads 的公式；新增测试证明 closed+mining 位于固定容量内，不增加单独缓冲。

- [ ] 步骤 5：先写 app 权威/reset 失败测试。

向 drainServerMessages 注入 active PlayerState，并断言 application.miningOverlay 等于权威值。没有新 PlayerState 时，改变鼠标输入不得改变它；Reset、inactive 状态与 closeClientSession 都必须清零。

- [ ] 步骤 6：只保存渲染本地的已确认值。

在 application 中增加 miningOverlay render.MiningOverlay；PlayerState 分支设置：

    a.miningOverlay = render.MiningOverlay{
        Active:        state.MiningActive,
        ProgressTicks: state.MiningProgressTicks,
        RequiredTicks: state.MiningRequiredTicks,
        Harvestable:   state.MiningHarvestable,
    }

inactive 或 reset 时赋零值；把它传给 HotbarRenderer.Prepare，并在 closeClientSession 中与其他连接期镜像一起清零。

- [ ] 步骤 7：只运行无窗口 UI 测试。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo ./internal/client -run "Hotbar|Recipe|Mining|PlayerState|Reset" -race -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

预期：不创建 application 窗口，全部测试使用 fake gfx/window 值。

- [ ] 步骤 8：提交任务 7。

    git add internal/render/hotbar.go internal/render/hotbar_test.go cmd/mcgo/app.go cmd/mcgo/app_test.go
    git commit -m "feat: 显示采掘进度与工具配方"

---

## 任务 8：升级 scenario v10 与兼容文档

OpenSpec 覆盖：8.1–8.4。

**文件：**

- 修改：cmd/mcgo/benchmark.go
- 修改：cmd/mcgo/benchmark_v5_test.go
- 修改：cmd/mcgo/benchmark_v6_test.go
- 修改：cmd/perfcheck/main.go
- 修改：cmd/perfcheck/main_test.go
- 修改：README.md
- 修改：docs/notes/lan-server.md
- 修改：docs/notes/perf-baseline.md

- [ ] 步骤 1：先写 scenario v10 producer 失败测试。

把既有版本断言改为 10；保留对 2560×1440、still/flying 阶段、2048 个 GPU 样本、RSS、tick 指标和 transport metadata 的精确检查。

- [ ] 步骤 2：先写比较器迁移失败测试。

使用完整报告并覆盖：

    tests := []struct {
        baseline int
        current  int
        allow    string
        wantErr  bool
    }{
        {9, 10, "", true},
        {9, 10, "9:10", false},
        {10, 9, "9:10", true},
        {8, 10, "8:10", true},
        {8, 9, "8:9", true},
        {10, 10, "", false},
    }

退役的 8:9 参数不得继续接受。历史 v6-v9 报告仍可解析，但只有同版本配对或显式 9:10 迁移可比较。

- [ ] 步骤 3：实现唯一允许的迁移。

设置 scenarioVersion=10，把 flag 帮助更新为 9:10，并实现：

    allowedScenarioUpgrade :=
        baseline.ScenarioVersion == 9 &&
        current.ScenarioVersion == 10 &&
        allowScenarioUpgrade == "9:10"

保持迁移行为：验证报告完整性、来源、同硬件和全部当前绝对门禁，只跳过跨 scenario 相对回归。相同 v10 与 Memory→TCP 比较继续执行既有稳定门禁。

- [ ] 步骤 4：更新中文兼容与运维文档。

写明：

- hold-left authority mining and exact tool recipes;
- the fixed timing/harvest table;
- protocol v8 rejects v7 at handshake;
- client packet ID 1 is retired;
- player schema v3 and chunk schema v4 are unchanged;
- shutdown and full-world backup before upgrade;
- rollback restores the backup from before tools were first saved;
- scenario v10 requires explicit 9:10 only;
- M2 baseline is untouched and M5 v10 is not written until task 10;
- durability, wood, multi-input crafting, shared progress and crack textures remain unimplemented.

- [ ] 步骤 5：运行 producer/comparator 验证。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/network -race -count=1'
    openspec validate --all --strict --no-interactive
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check

- [ ] 步骤 6：提交任务 8。

    git add cmd/mcgo/benchmark.go cmd/mcgo/benchmark_v5_test.go cmd/mcgo/benchmark_v6_test.go cmd/perfcheck/main.go cmd/perfcheck/main_test.go README.md docs/notes/lan-server.md docs/notes/perf-baseline.md
    git commit -m "feat: 升级 benchmark scenario v10"

---

## 任务 9：用完整非交互门禁关闭候选版本

OpenSpec 覆盖：9.1–9.4。

**文件：**

- 修改：openspec/changes/m4f-authoritative-mining-tools/tasks.md
- 仅发现不一致时修改：change proposal、design 或 delta specs
- 仅修根因时修改：实现/测试文件

- [ ] 步骤 1：把每条 requirement 映射到可执行证据。

只有对应提交和命令证据存在时才勾选任务 1–8。对照代码、测试与八份 delta specs，并运行：

    rg -n "TO[D]O|TB[D]|placeholde[r]|稍[后]|待[定]" openspec/changes/m4f-authoritative-mining-tools docs/superpowers/plans/2026-08-04-m4f-authoritative-mining-tools.md
    rg -n "BreakBlock|CommandBreakBlock|breakBlock\(" --glob='*.go' .
    rg -n "ItemStonePickaxe|ItemIronPickaxe|MiningProgressTicks|scenarioVersion" internal cmd

预期：没有未决标记或旧 break 输出，每个稳定新增项都有生产与测试引用。

- [ ] 步骤 2：运行全仓正确性门禁。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check
    openspec validate --all --strict --no-interactive

预期：全部命令通过，gofmt 与 diff check 无输出。

- [ ] 步骤 3：运行协议、存储与性能专项检查。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=30s'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "^$" -fuzz FuzzDecodePlayer -fuzztime=30s'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network ./internal/sim -run "^$" -bench "SmallPacketCodec|AuthoritativeMiningEightPlayers" -benchmem -count=5'

- [ ] 步骤 4：确认进程与工作树卫生。

    pgrep -fl 'mcgo|perfcheck'
    git status --short

预期：本次工作启动的 benchmark/game 进程都已退出；唯一无关路径为 ?? midscene_run/。

- [ ] 步骤 5：冻结候选版本。

把 OpenSpec 任务 1–9 标为完成并提交：

    git add openspec/changes/m4f-authoritative-mining-tools
    git commit -m "chore: 关闭 M4F 权威计时采掘"

记录所得完整 HEAD。此后若没有新修复提交并重跑任务 9，不得改变 producer、阈值、scenario 或采掘热路径。

---

## 任务 10：建立一次性 M5 scenario v10 基线

OpenSpec 覆盖：10.1–10.4。

**文件：**

- 两次运行都通过后修改：docs/notes/perf-baseline-m5.json
- 两次运行都通过后修改：docs/notes/perf-baseline.md
- 两次运行都通过后修改：docs/notes/perf-baseline-m5.md

- [ ] 步骤 1：暂停并请求明确授权。

先采集只读身份：

    git rev-parse HEAD
    shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json
    system_profiler SPHardwareDataType
    sw_vers
    zsh -ic 'gvm use go1.26.0 >/dev/null && go version'
    pgrep -fl 'mcgo|perfcheck'

报告：

- frozen candidate HEAD;
- SHA-256 of docs/notes/perf-baseline.json and docs/notes/perf-baseline-m5.json;
- exact M5 hardware/OS/Go identity;
- two new non-existent output paths containing the frozen HEAD;
- Memory first, TCP second;
- any failure stops the chain;
- no rerun and no baseline overwrite on failure.

用户明确授权这一精确边界前，不得执行正式 benchmark。

- [ ] 步骤 2：解析并验证精确的一次性路径。

    M4F_HEAD=$(git rev-parse --short=12 HEAD)
    M4F_MEMORY_REPORT=/tmp/mcgo-m5-v10-$M4F_HEAD-memory.json
    M4F_MEMORY_LOG=/tmp/mcgo-m5-v10-$M4F_HEAD-memory.log
    M4F_TCP_REPORT=/tmp/mcgo-m5-v10-$M4F_HEAD-tcp.json
    M4F_TCP_LOG=/tmp/mcgo-m5-v10-$M4F_HEAD-tcp.log
    test ! -e "$M4F_MEMORY_REPORT"
    test ! -e "$M4F_MEMORY_LOG"
    test ! -e "$M4F_TCP_REPORT"
    test ! -e "$M4F_TCP_LOG"

若任一路径已存在，正式运行前停止并与用户确定新的明确后缀；不得删除或覆盖旧证据。

- [ ] 步骤 3：通过既有无窗口路径只生成一次 Memory 报告。

精确运行：

    TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '$M4F_MEMORY_REPORT'" | tee "$M4F_MEMORY_LOG"

把 v9 M5 基线与新报告比较：

    zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '$M4F_MEMORY_REPORT' --max-regression 0.20 --allow-scenario-upgrade 9:10"

任何命令或门禁失败都立即停止：保留日志/输出，不运行 TCP，不覆盖基线，并报告证据。

- [ ] 步骤 4：仅在 Memory 通过后生成一次 TCP 报告。

运行：

    TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '$M4F_TCP_REPORT'" | tee "$M4F_TCP_LOG"
    zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline '$M4F_MEMORY_REPORT' --current '$M4F_TCP_REPORT' --max-regression 0.20"

失败时停止，保留两份报告，不重跑也不覆盖基线。

- [ ] 步骤 5：只有两步都通过后才提升 Memory 精确字节。

把已验证的 Memory 报告逐字节复制到 docs/notes/perf-baseline-m5.json，并用 cmp 和 SHA-256 证明完全一致：

    cp "$M4F_MEMORY_REPORT" docs/notes/perf-baseline-m5.json
    cmp -s "$M4F_MEMORY_REPORT" docs/notes/perf-baseline-m5.json

记录：

- frozen HEAD;
- commands;
- report/log SHA-256;
- M5 identity and environment;
- replaced v9 report identity;
- Memory→TCP comparison result.

重新计算 M2 基线哈希，并断言它等于步骤 1 的值。步骤 3–6 每次打开新 shell 时都先重复步骤 2 的五个只读变量赋值，确保路径一致。

- [ ] 步骤 6：验证并提交基线。

    zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '$M4F_MEMORY_REPORT' --max-regression 0.20"
    git diff --check
    openspec validate --all --strict --no-interactive
    git add docs/notes/perf-baseline-m5.json docs/notes/perf-baseline.md docs/notes/perf-baseline-m5.md
    git commit -m "chore: 建立 M5 scenario v10 基线"

---

## 任务 11：同步稳定规格并归档 M4F

OpenSpec 覆盖：11.1–11.3。

**文件：**

- 修改：openspec/specs/authoritative-mining/spec.md
- 修改：受 delta specs 影响的七份既有主规格
- 通过 OpenSpec 移动/归档：openspec/changes/m4f-authoritative-mining-tools

- [ ] 步骤 1：在基线提交上重跑最终全仓门禁。

    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go vet ./...'
    zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/archcheck -count=1'
    zsh -ic 'gvm use go1.26.0 >/dev/null && gofmt -l .'
    git diff --check
    openspec validate --all --strict --no-interactive

- [ ] 步骤 2：把 delta specs 同步到主规格。

使用 openspec-sync-specs skill，并核对：

- authoritative-mining has a real Purpose;
- protocol v7 requirement is replaced by v8;
- PlayerInput and PlayerState mining fields are canonical;
- player schema v3 and chunk schema v4 remain;
- scenario v10 and explicit 9:10 migration are present;
- M2 and M5 baseline responsibilities are not conflated.

- [ ] 步骤 3：归档 change。

使用 openspec-archive-change skill。归档前确认所有复选框都完成，然后运行：

    openspec validate --all --strict --no-interactive
    git diff --check

- [ ] 步骤 4：提交归档。

    git add openspec
    git commit -m "chore: 归档 M4F 权威计时采掘"

- [ ] 步骤 5：最终交接。

报告分支、顺序提交、全部验证证据、正式 benchmark 结果与基线哈希。除非用户要求，否则不合并也不推送。

---

## 计划自审

- [ ] 规格覆盖：八份 delta specs 的每条 requirement 和 scenario 都映射到至少一个任务和具体断言。
- [ ] 接口一致性：PlayerInput.Mining → sim.Command.Mining → playerState.miningHeld；sim.MiningUpdate → network.PlayerState 字段 → render.MiningOverlay。
- [ ] 生命周期一致性：非法输入、松键、reset、视野丢失、打开容器、断线和客户端关闭都清除连接期采掘状态。
- [ ] 原子性一致性：harvestable 掉落与熔炉内容都在方块/熔炉/revision 变化前完成预演。
- [ ] 确定性一致性：最多八个活动 session ID 按稳定顺序处理，每人最多执行一次射线。
- [ ] 兼容性一致性：协议 v8 拒绝 v7；ID 1 保持空洞；玩家 v3/区块 v4 不变；旧二进制回退依赖备份。
- [ ] 性能一致性：不增加 goroutine、目标 map、动态 registry、shader、pipeline 或无界集合。
- [ ] 未决标记扫描：计划中不存在未落实的测试动作、类比实现或未解析的类型和文件名。
- [ ] 执行边界：任务 10 明确阻塞于用户的一次性授权；失败后停止，不重跑也不覆盖。
