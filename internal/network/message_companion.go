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
	chatCommandMaxWireBytes     = 1026
	chatEventMaxWireBytes       = 1328
	companionSpawnMaxWireBytes  = 178
	companionStateWireBytes     = 41
	maxCompanionStates          = companion.MaxActive
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
}

func (ChatEvent) serverMessage() {}
func (ChatEvent) serverPacket()  {}

// Validate 验证事件种类与所携字段的精确组合。
//
// 组合规则是原子的：任一字段不满足当前 kind/reason 的要求即整体拒绝。
// 任务事件（Task*）必须携带完整伙伴身份与原始指令，且 MUST NOT 携带模型
// 生成的自由文本——wire 形状上唯一的文本字段就是玩家原始指令 Command，
// 因此该约束由组合校验结构性保证。QueueFull 拒绝保留与 Accepted 相同的
// 身份与指令要求，以便发令者能对应到具体未入队的指令。
func (event ChatEvent) Validate() error {
	if event.EventID == 0 || !event.PlayerID.Valid() || !validPlayerName(event.PlayerName) {
		return errors.New("network: invalid chat event player identity")
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
		case ChatRejectQueueFull:
			// 队列满必须让发令者能定位被拒指令，因此携带完整伙伴身份与合法指令。
			if !event.CompanionID.Valid() || companion.ValidateName(event.CompanionName) != nil ||
				validateCommandText(event.Command) != nil {
				return errors.New("network: queue-full chat event lacks companion identity or command")
			}
		default:
			return errors.New("network: invalid chat rejection reason")
		}
	case ChatEventTaskStarted, ChatEventTaskProgress, ChatEventTaskCompleted, ChatEventTaskTimedOut:
		// 任务推进事件只复述原始指令；reason 槽位必须保持 None，
		// 失败原因只允许出现在 TaskFailed 上。
		if event.RejectReason != ChatRejectNone || !event.CompanionID.Valid() ||
			companion.ValidateName(event.CompanionName) != nil || validateCommandText(event.Command) != nil {
			return fmt.Errorf("network: invalid %d task chat event", event.Kind)
		}
	case ChatEventTaskFailed:
		// TaskFailed 的 reason 槽位承载 TaskFailReason 固定枚举（16..19）。
		if !validTaskFailReason(TaskFailReason(event.RejectReason)) || !event.CompanionID.Valid() ||
			companion.ValidateName(event.CompanionName) != nil || validateCommandText(event.Command) != nil {
			return errors.New("network: invalid failed task chat event")
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
	case TaskFailPlannerUnavailable, TaskFailInvalidPlan, TaskFailPathUnreachable, TaskFailWorldChanged:
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

func validateCommandText(text string) error {
	if len(text) < 1 || len(text) > 1024 || !utf8.ValidString(text) || strings.TrimSpace(text) != text {
		return errors.New("network: invalid chat command text")
	}
	for _, r := range text {
		if r == 0 || unicode.IsControl(r) {
			return errors.New("network: chat command contains control character")
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
	e.string(event.Command, 1024)
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
		event.Command, err = d.string(1024, 1024)
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
