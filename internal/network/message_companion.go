package network

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

const (
	// chatCommandMaxWireBytes 是 ChatCommand 载荷的固定 wire 上限。推导：
	// uvarint 长度前缀 2 bytes（文本长度 128..16383 时 uvarint 占 2 bytes）+
	// 文本 ≤ companion.MaxPlanCommandBytes（1024）= 1026。文本上限与 companion
	// 常量同源（E7）：Planner 指令上限变化时本 wire 上限自动随动，不再依赖
	// 裸字面量与注释约定。数值当前冻结为 1026。
	chatCommandMaxWireBytes = companion.MaxPlanCommandBytes + 2
	// chatEventMaxWireBytes 是 ChatEvent 载荷的固定 wire 上限。推导：8 event
	// ID（u64）+ 16 玩家 ID + 130 玩家名（128-byte 名称上限 + 2-byte uvarint
	// 前缀）+ 16 伙伴 ID + 130 伙伴名 + 1 kind + 1 reason + 文本槽位
	// （= chatCommandMaxWireBytes）= 1328。文本槽位按 kind 复用：指令槽位
	// ≤ 1024+2 决定上界，台词槽位 ≤ 256+2=258 更小。指令槽位与 companion
	// 常量同源（E7），名称上限当前冻结为 128 bytes。数值当前冻结为 1328。
	chatEventMaxWireBytes = 8 + 16 + 130 + 16 + 130 + 1 + 1 + chatCommandMaxWireBytes
	// companionSpawnMaxWireBytes 是 CompanionSpawn 载荷的固定 wire 上限。
	// 推导：16 伙伴 ID + 130 伙伴名（128-byte 名称上限 + 2-byte uvarint 前缀）
	// + 8 tick（u64）+ 4 维度（i32）+ 12 位置（3×f32）+ 4 yaw（f32）+
	// 4 pitch（f32）= 178。数值当前冻结为 178。
	companionSpawnMaxWireBytes = 178
	// companionStateWireBytes 是 CompanionStates 批次内单个伙伴状态的固定编码
	// 长度。推导：16 伙伴 ID + 4 维度（i32）+ 12 位置（3×f32）+ 4 yaw（f32）
	// + 4 pitch（f32）+ 1 reset bool = 41。解码端按「剩余字节数 == count×41」
	// 整批校验（见 decodeCompanionStates），任何字段变宽都会在这里先失配。
	// 数值当前冻结为 41。
	companionStateWireBytes = 41
	maxCompanionStates      = companion.MaxActive
	// companionStatesMaxWireBytes 是 CompanionStates 载荷的固定 wire 上限。
	// 推导：8 tick（u64）+ 1 count uvarint（状态数 ≤ companion.MaxActive=4，
	// 单字节）+ 每状态 companionStateWireBytes（41）bytes = 173。数值当前
	// 冻结为 173。
	companionStatesMaxWireBytes = 8 + 1 + maxCompanionStates*companionStateWireBytes
)

// ChatCommand 是客户端发送的有界聊天文本。
type ChatCommand struct {
	Text string
}

func (ChatCommand) clientMessage() {}
func (ChatCommand) clientPacket()  {}

// Validate 验证聊天文本的 UTF-8、长度和边界空白。
func (command ChatCommand) Validate() error {
	return validateCommandText(command.Text)
}

// ChatEventKind 标识聊天寻址是否成功以及伙伴任务生命周期的推进阶段。
type ChatEventKind uint8

const (
	ChatEventAccepted ChatEventKind = iota + 1
	ChatEventRejected
	ChatEventTaskStarted
	ChatEventTaskProgress
	ChatEventTaskCompleted
	ChatEventTaskFailed
	ChatEventTaskTimedOut
	// ChatEventTaskStopped 是 v18 追加的停止事件：服务端确认一条已被停止旁路终结的任务，
	// 携带完整伙伴身份与被停止任务的原始指令；广播语义由服务端任务实现，本包只做组合校验。
	ChatEventTaskStopped
	// ChatEventCompanionSpeech 是 v19 追加的伙伴台词事件：Dialogue 表达平面产出的一句
	// 符合人设的台词（1..256 bytes）。它是 ChatEvent 中唯一允许携带模型生成文本的 kind，
	// 任务事实事件（Task*）仍只复述玩家原始指令。wire 上台词复用既有文本槽位并在本 kind
	// 内收紧为 256-byte 上界，既有 kind 的 golden 字节因此保持不变；广播接收者语义由
	// 服务端决定，本包只做组合校验。
	ChatEventCompanionSpeech
)

// ChatRejectReason 标识聊天寻址被拒绝的原因。值 3 预留未分配，
// 拒绝原因整体保留 0..15 的编号空间，与 TaskFailReason 错开。
type ChatRejectReason uint8

const (
	ChatRejectNone ChatRejectReason = iota
	ChatRejectInvalidFormat
	ChatRejectUnknownCompanion
	_
	ChatRejectQueueFull
	// ChatRejectNotFollowing 是 v18 追加的停止旁路同步拒绝：目标伙伴当前没有可停止的
	// 持续任务。它针对特定伙伴的当前任务状态，因此与 QueueFull 一样必须携带完整伙伴身份
	// 与被拒指令，只回复发令者。
	ChatRejectNotFollowing
)

// TaskFailReason 是 TaskFailed 事件携带的稳定失败原因枚举。
// 它与 ChatRejectReason 共用 ChatEvent 的 reason wire 槽位，但从 16 起编号，
// 只允许出现在 ChatEventTaskFailed 上。
type TaskFailReason uint8

const (
	TaskFailPlannerUnavailable TaskFailReason = 16 + iota
	TaskFailInvalidPlan
	TaskFailPathUnreachable
	TaskFailWorldChanged
	// TaskFailInventoryFull 是 v18 追加的容量失败原因：采掘产物或放置物品在伙伴背包
	// 无容量或已耗尽，任务因此终结。
	TaskFailInventoryFull
)

// ChatEvent 是服务端在 tick 边界确认的聊天寻址事实。
type ChatEvent struct {
	EventID       uint64
	PlayerID      core.PlayerID
	PlayerName    string
	CompanionID   companion.ID
	CompanionName string
	Kind          ChatEventKind
	RejectReason  ChatRejectReason
	Command       string
	// Speech 是 CompanionSpeech 事件携带的伙伴台词（1..256 bytes，v19 追加）。
	// 它与 Command 共用 wire 上唯一的文本槽位，编码按 kind 二选一写入，因此两者在
	// 组合校验上互斥：非 Speech kind 携带台词、或 Speech kind 复述玩家指令，
	// 都会在应用任何字段前被整条拒绝。
	Speech string
}

func (ChatEvent) serverMessage() {}
func (ChatEvent) serverPacket()  {}

// Validate 验证事件种类与所携字段的精确组合。
//
// 组合规则是原子的：任一字段不满足当前 kind/reason 的要求即整体拒绝。
// 任务事件（Task*）必须携带完整伙伴身份与原始指令，且 MUST NOT 携带模型
// 生成的自由文本——wire 上唯一的文本槽位按 kind 复用（Command 或 Speech），
// CompanionSpeech 是唯一允许携带模型文本的 kind 且不复述指令，该互斥约束由
// 组合校验结构性保证。QueueFull 与 NotFollowing 拒绝保留与 Accepted 相同的
// 身份与指令要求，以便发令者能对应到具体被拒的指令。
func (event ChatEvent) Validate() error {
	if event.EventID == 0 || !event.PlayerID.Valid() || !validPlayerName(event.PlayerName) {
		return errors.New("network: invalid chat event player identity")
	}
	// 台词字段只属于 CompanionSpeech：任何其他 kind（含任务事实与拒绝事件）
	// 携带台词都必须在应用任何字段前整条拒绝。
	if event.Kind != ChatEventCompanionSpeech && event.Speech != "" {
		return errors.New("network: chat event kind carries companion speech")
	}
	switch event.Kind {
	case ChatEventAccepted:
		if event.RejectReason != ChatRejectNone || !event.CompanionID.Valid() ||
			companion.ValidateName(event.CompanionName) != nil || validateCommandText(event.Command) != nil {
			return errors.New("network: invalid accepted chat event")
		}
	case ChatEventRejected:
		switch event.RejectReason {
		case ChatRejectInvalidFormat:
			// 格式错误的指令连寻址都没发生：伙伴身份与指令必须全部清空。
			if event.CompanionID != (companion.ID{}) || event.Command != "" || event.CompanionName != "" {
				return errors.New("network: invalid-format chat event leaks companion identity or command")
			}
		case ChatRejectUnknownCompanion:
			// 只保留合法目标名称，供发令者核对拼写；身份与指令必须清空。
			if event.CompanionID != (companion.ID{}) || event.Command != "" ||
				companion.ValidateName(event.CompanionName) != nil {
				return errors.New("network: unknown-companion chat event carries identity or command")
			}
		case ChatRejectQueueFull, ChatRejectNotFollowing:
			// QueueFull 与 NotFollowing 都针对特定伙伴的当前任务状态：
			// 必须让发令者能定位被拒指令，因此携带完整伙伴身份与合法指令。
			if !event.CompanionID.Valid() || companion.ValidateName(event.CompanionName) != nil ||
				validateCommandText(event.Command) != nil {
				return fmt.Errorf("network: chat rejection %d lacks companion identity or command", event.RejectReason)
			}
		default:
			return errors.New("network: invalid chat rejection reason")
		}
	case ChatEventTaskStarted, ChatEventTaskProgress, ChatEventTaskCompleted, ChatEventTaskTimedOut, ChatEventTaskStopped:
		// 任务推进事件（含 v18 的停止事件）只复述原始指令；reason 槽位必须保持 None，
		// 失败原因只允许出现在 TaskFailed 上。
		if event.RejectReason != ChatRejectNone || !event.CompanionID.Valid() ||
			companion.ValidateName(event.CompanionName) != nil || validateCommandText(event.Command) != nil {
			return fmt.Errorf("network: invalid %d task chat event", event.Kind)
		}
	case ChatEventTaskFailed:
		// TaskFailed 的 reason 槽位承载 TaskFailReason 固定枚举（16..20）。
		if !validTaskFailReason(TaskFailReason(event.RejectReason)) || !event.CompanionID.Valid() ||
			companion.ValidateName(event.CompanionName) != nil || validateCommandText(event.Command) != nil {
			return errors.New("network: invalid failed task chat event")
		}
	case ChatEventCompanionSpeech:
		// v19 台词事件：reason 必须为 None，不得复述玩家指令，且必须携带完整
		// 伙伴身份与满足 1..256-byte 文本纪律的台词；台词是模型生成的有界表达。
		if event.RejectReason != ChatRejectNone || event.Command != "" ||
			!event.CompanionID.Valid() || companion.ValidateName(event.CompanionName) != nil ||
			validateSpeechText(event.Speech) != nil {
			return errors.New("network: invalid companion speech chat event")
		}
	default:
		return errors.New("network: invalid chat event kind")
	}
	return nil
}

// validTaskFailReason 判断失败原因是否属于 TaskFailed 允许的固定枚举；
// 拒绝原因区间（0..15）与其他越界值都必须为假。
func validTaskFailReason(reason TaskFailReason) bool {
	switch reason {
	case TaskFailPlannerUnavailable, TaskFailInvalidPlan, TaskFailPathUnreachable,
		TaskFailWorldChanged, TaskFailInventoryFull:
		return true
	default:
		return false
	}
}

// CompanionSpawn 在客户端首次可见时发布伙伴的完整身份与身体。
type CompanionSpawn struct {
	ID        companion.ID
	Name      string
	Tick      uint64
	Dimension core.DimensionID
	Position  mgl32.Vec3
	Yaw       float32
	Pitch     float32
}

func (CompanionSpawn) serverMessage() {}
func (CompanionSpawn) serverPacket()  {}

// Validate 验证伙伴出生消息的身份、维度与有限位姿。
func (spawn CompanionSpawn) Validate() error {
	if !spawn.ID.Valid() || companion.ValidateName(spawn.Name) != nil ||
		spawn.Dimension != core.Overworld || !validCompanionPose(spawn.Position, spawn.Yaw, spawn.Pitch) {
		return errors.New("network: invalid companion spawn")
	}
	return nil
}

// CompanionState 是伙伴在一个 tick 的权威身体状态。
type CompanionState struct {
	ID        companion.ID
	Dimension core.DimensionID
	Position  mgl32.Vec3
	Yaw       float32
	Pitch     float32
	Reset     bool
}

func (state CompanionState) validate() error {
	if !state.ID.Valid() || state.Dimension != core.Overworld ||
		!validCompanionPose(state.Position, state.Yaw, state.Pitch) {
		return errors.New("invalid companion state")
	}
	return nil
}

// CompanionStates 是按 ID 严格升序的有界伙伴状态批次。
type CompanionStates struct {
	Tick   uint64
	States []CompanionState
}

func (CompanionStates) serverMessage() {}
func (CompanionStates) serverPacket()  {}

// Validate 验证批次数量、每项状态和 ID 严格顺序。
func (states CompanionStates) Validate() error {
	if len(states.States) < 1 || len(states.States) > maxCompanionStates {
		return fmt.Errorf("network: companion state count is outside 1..%d", maxCompanionStates)
	}
	for index, state := range states.States {
		if err := state.validate(); err != nil {
			return fmt.Errorf("network: companion state %d: %w", index, err)
		}
		if index > 0 && bytes.Compare(states.States[index-1].ID[:], state.ID[:]) >= 0 {
			return errors.New("network: companion states are not strictly sorted")
		}
	}
	return nil
}

// CompanionDespawn 按独立伙伴 ID 移除客户端可见实体。
type CompanionDespawn struct {
	ID companion.ID
}

func (CompanionDespawn) serverMessage() {}
func (CompanionDespawn) serverPacket()  {}

// Validate 验证消失消息携带有效伙伴 ID。
func (despawn CompanionDespawn) Validate() error {
	if !despawn.ID.Valid() {
		return errors.New("network: invalid companion despawn")
	}
	return nil
}

// validateCommandText 验证聊天指令文本：1..companion.MaxPlanCommandBytes 字节、
// 有效 UTF-8、无 NUL/Unicode control 且无首尾空白。上限与 Planner 快照指令
// （companion.MaxPlanCommandBytes，plan_types.go）同源（E7）：玩家能发出的
// 每条指令必须总能进入权威规划输入，两侧由同一常量保证不漂移；行为级锁测试
// （message_companion_limit_lock_test.go）在任一侧漂移时变红。
func validateCommandText(text string) error {
	if len(text) < 1 || len(text) > companion.MaxPlanCommandBytes || !utf8.ValidString(text) || strings.TrimSpace(text) != text {
		return errors.New("network: invalid chat command text")
	}
	for _, r := range text {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("network: chat command contains control character")
		}
	}
	return nil
}

// validateSpeechText 验证伙伴台词文本：1..companion.MaxDialogueLineBytes 字节、
// 有效 UTF-8、无 NUL/Unicode control 且无首尾空白。文本纪律与玩家指令同构，
// 但上界从指令的 MaxPlanCommandBytes 收紧为 MaxDialogueLineBytes——台词是
// 模型生成的有界表达，不是玩家输入通道。上界与 Dialogue 表达平面的解码边界
// （companion/dialogue_types.go）共用同一常量（E7），两侧不可能漂移。
func validateSpeechText(text string) error {
	if len(text) < 1 || len(text) > companion.MaxDialogueLineBytes || !utf8.ValidString(text) || strings.TrimSpace(text) != text {
		return errors.New("network: invalid companion speech text")
	}
	for _, r := range text {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("network: companion speech contains control character")
		}
	}
	return nil
}

func validPlayerName(name string) bool {
	canonical, err := core.NormalizeDisplayName(name)
	return err == nil && canonical == name
}

func validCompanionPose(position mgl32.Vec3, yaw, pitch float32) bool {
	return finiteVec3(position) && finite32(yaw) && finite32(pitch) && pitch >= -math.Pi/2 && pitch <= math.Pi/2
}

func encodeChatEvent(e *byteEncoder, event ChatEvent) {
	e.u64(event.EventID)
	e.data = append(e.data, event.PlayerID[:]...)
	e.string(event.PlayerName, 128)
	e.data = append(e.data, event.CompanionID[:]...)
	e.string(event.CompanionName, 128)
	e.u8(uint8(event.Kind))
	e.u8(uint8(event.RejectReason))
	// 文本槽位按 kind 复用：CompanionSpeech 写入台词（编码层即收紧为
	// companion.MaxDialogueLineBytes 上界），其余 kind 保持
	// companion.MaxPlanCommandBytes 指令编码，既有 kind 的 wire 字节不受影响。
	if event.Kind == ChatEventCompanionSpeech {
		e.string(event.Speech, companion.MaxDialogueLineBytes)
	} else {
		e.string(event.Command, companion.MaxPlanCommandBytes)
	}
}

func encodeCompanionSpawn(e *byteEncoder, spawn CompanionSpawn) {
	e.data = append(e.data, spawn.ID[:]...)
	e.string(spawn.Name, 128)
	e.u64(spawn.Tick)
	e.i32(int32(spawn.Dimension))
	for _, value := range spawn.Position {
		e.f32(value)
	}
	e.f32(spawn.Yaw)
	e.f32(spawn.Pitch)
}

func encodeCompanionStates(e *byteEncoder, states CompanionStates) {
	e.u64(states.Tick)
	e.uvarint(uint32(len(states.States)))
	for _, state := range states.States {
		e.data = append(e.data, state.ID[:]...)
		e.i32(int32(state.Dimension))
		for _, value := range state.Position {
			e.f32(value)
		}
		e.f32(state.Yaw)
		e.f32(state.Pitch)
		e.bool(state.Reset)
	}
}

func decodeChatEvent(d *byteDecoder) (ServerPacket, error) {
	var event ChatEvent
	var err error
	event.EventID, err = d.u64()
	if err == nil {
		err = decodeFixedID(d, event.PlayerID[:])
	}
	if err == nil {
		event.PlayerName, err = d.string(128, 32)
	}
	if err == nil {
		err = decodeFixedID(d, event.CompanionID[:])
	}
	if err == nil {
		event.CompanionName, err = d.string(128, 32)
	}
	if err == nil {
		var kind uint8
		kind, err = d.u8()
		event.Kind = ChatEventKind(kind)
	}
	if err == nil {
		var reason uint8
		reason, err = d.u8()
		event.RejectReason = ChatRejectReason(reason)
	}
	if err == nil {
		// 文本槽位按 kind 复用：解码时先读出 kind，再把槽位读入台词（超过
		// companion.MaxDialogueLineBytes 直接拒绝）或玩家指令，随后 Validate
		// 按 kind 施加完整文本纪律。
		if event.Kind == ChatEventCompanionSpeech {
			event.Speech, err = d.string(companion.MaxDialogueLineBytes, companion.MaxDialogueLineBytes)
		} else {
			event.Command, err = d.string(companion.MaxPlanCommandBytes, companion.MaxPlanCommandBytes)
		}
	}
	if err != nil {
		return nil, err
	}
	return event, nil
}

func decodeCompanionSpawn(d *byteDecoder) (ServerPacket, error) {
	var spawn CompanionSpawn
	var err error
	err = decodeFixedID(d, spawn.ID[:])
	if err == nil {
		spawn.Name, err = d.string(128, 32)
	}
	if err == nil {
		spawn.Tick, err = d.u64()
	}
	if err == nil {
		var dimension int32
		dimension, err = d.i32()
		spawn.Dimension = core.DimensionID(dimension)
	}
	for index := range spawn.Position {
		if err == nil {
			spawn.Position[index], err = d.f32()
		}
	}
	if err == nil {
		spawn.Yaw, err = d.f32()
	}
	if err == nil {
		spawn.Pitch, err = d.f32()
	}
	if err != nil {
		return nil, err
	}
	return spawn, nil
}

func decodeCompanionStates(d *byteDecoder) (ServerPacket, error) {
	var states CompanionStates
	var err error
	states.Tick, err = d.u64()
	var count uint32
	if err == nil {
		count, err = d.uvarint()
	}
	if err == nil && (count < 1 || count > maxCompanionStates) {
		err = fmt.Errorf("network: companion state count is outside 1..%d", maxCompanionStates)
	}
	if err == nil && len(d.data)-d.offset != int(count)*companionStateWireBytes {
		err = errors.New("network: companion states length does not match count")
	}
	if err != nil {
		return nil, err
	}
	states.States = make([]CompanionState, int(count))
	for index := range states.States {
		state := &states.States[index]
		if err = decodeFixedID(d, state.ID[:]); err != nil {
			return nil, err
		}
		dimension, readErr := d.i32()
		if readErr != nil {
			return nil, readErr
		}
		state.Dimension = core.DimensionID(dimension)
		for component := range state.Position {
			state.Position[component], err = d.f32()
			if err != nil {
				return nil, err
			}
		}
		if state.Yaw, err = d.f32(); err != nil {
			return nil, err
		}
		if state.Pitch, err = d.f32(); err != nil {
			return nil, err
		}
		if state.Reset, err = d.bool(); err != nil {
			return nil, err
		}
	}
	return states, nil
}

func decodeFixedID(d *byteDecoder, destination []byte) error {
	data, err := d.take(len(destination))
	if err != nil {
		return err
	}
	copy(destination, data)
	return nil
}
