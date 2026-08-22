package network

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

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
	// Health 是权威生命值，协议 v13 起随玩家状态同步；合法区间是 0..core.MaxHealth。
	Health uint8
	// Oxygen 是权威氧气，协议 v21 起随玩家状态同步（wire 上紧跟 Health 之后、
	// WorldTimeTicks 之前）；合法区间是 0..core.MaxOxygenTicks。它与 Health 一样
	// 只发给玩家本人，且不进存档：断线重连后服务端一律重新初始化为满值。
	Oxygen uint16
	// Hunger 是权威饥饿值，协议 v24 起随玩家状态同步（wire 上紧跟 `Oxygen` 之后、
	// WorldTimeTicks 之前）；合法区间是 0..`core.MaxHunger`。它与 `Health`、
	// `Oxygen` 一样只发给玩家本人。三层饥饿状态里**只有它上线**：饱和度与疲劳值
	// 是纯服务端推进量，界面不呈现，因而不占 wire 字段。
	Hunger uint8
	// WorldTimeTicks 是本 tick 结束时的权威绝对世界时间，协议 v9 起随玩家状态同步。
	WorldTimeTicks uint64
}

type RemotePlayerSpawn struct {
	PlayerID    core.PlayerID
	DisplayName string
	ServerTick  uint64
	Dimension   core.DimensionID
	Position    mgl32.Vec3
	Yaw, Pitch  float32
}

func (RemotePlayerSpawn) serverMessage() {}
func (RemotePlayerSpawn) serverPacket()  {}

func (spawn RemotePlayerSpawn) Validate() error {
	name, err := core.NormalizeDisplayName(spawn.DisplayName)
	if err != nil || name != spawn.DisplayName || !spawn.PlayerID.Valid() ||
		spawn.Dimension != core.Overworld || !finiteVec3(spawn.Position) ||
		!finite32(spawn.Yaw) || !finite32(spawn.Pitch) {
		return errors.New("network: invalid remote player spawn")
	}
	return nil
}

type RemotePlayerDespawn struct{ PlayerID core.PlayerID }

func (RemotePlayerDespawn) serverMessage() {}
func (RemotePlayerDespawn) serverPacket()  {}

func (despawn RemotePlayerDespawn) Validate() error {
	if !despawn.PlayerID.Valid() {
		return errors.New("network: invalid remote player despawn")
	}
	return nil
}

type RemotePlayerStates struct {
	ServerTick uint64
	Players    []RemotePlayerState
}

type RemotePlayerState struct {
	PlayerID   core.PlayerID
	Dimension  core.DimensionID
	Position   mgl32.Vec3
	Yaw, Pitch float32
	Reset      bool
}

func (RemotePlayerStates) serverMessage() {}
func (RemotePlayerStates) serverPacket()  {}

func (states RemotePlayerStates) Validate() error {
	if len(states.Players) < 1 || len(states.Players) > 7 {
		return errors.New("network: remote player state count is outside 1..7")
	}
	for index, state := range states.Players {
		if err := state.validate(); err != nil {
			return fmt.Errorf("network: remote player state %d: %w", index, err)
		}
		if index > 0 && bytes.Compare(states.Players[index-1].PlayerID[:], state.PlayerID[:]) >= 0 {
			return errors.New("network: remote player states are not strictly sorted")
		}
	}
	return nil
}

func (state RemotePlayerState) validate() error {
	if !state.PlayerID.Valid() || state.Dimension != core.Overworld || !finiteVec3(state.Position) ||
		!finite32(state.Yaw) || !finite32(state.Pitch) {
		return errors.New("invalid remote player state")
	}
	return nil
}

func (PlayerState) serverMessage() {}
func (PlayerState) serverPacket()  {}

func (state PlayerState) Validate() error {
	for _, value := range state.Position {
		if !finite32(value) {
			return errors.New("network: player state has non-finite position")
		}
	}
	for _, value := range state.Velocity {
		if !finite32(value) {
			return errors.New("network: player state has non-finite velocity")
		}
	}
	if !finite32(state.Yaw) || !finite32(state.Pitch) {
		return errors.New("network: player state has non-finite rotation")
	}
	if !core.ValidHealth(state.Health) {
		return errors.New("network: player state has out-of-range health")
	}
	if !core.ValidOxygen(state.Oxygen) {
		return errors.New("network: player state has out-of-range oxygen")
	}
	if !core.ValidHunger(state.Hunger) {
		return errors.New("network: player state has out-of-range hunger")
	}
	if !state.MiningActive {
		if state.MiningTarget != (core.BlockPos{}) || state.MiningProgressTicks != 0 ||
			state.MiningRequiredTicks != 0 || state.MiningHarvestable {
			return errors.New("network: inactive player state has mining fields")
		}
		return nil
	}
	if state.MiningProgressTicks == 0 || state.MiningProgressTicks >= state.MiningRequiredTicks {
		return errors.New("network: active player state has invalid mining progress")
	}
	return nil
}

type RejectReason string

const (
	RejectInvalidRay        RejectReason = "invalid_ray"
	RejectNoTarget          RejectReason = "no_target"
	RejectChunkNotReady     RejectReason = "chunk_not_ready"
	RejectProtectedBlock    RejectReason = "protected_block"
	RejectInvalidBlock      RejectReason = "invalid_block"
	RejectOccupied          RejectReason = "occupied"
	RejectInvalidInput      RejectReason = "invalid_input"
	RejectPlayerNotReady    RejectReason = "player_not_ready"
	RejectInvalidSlot       RejectReason = "invalid_slot"
	RejectHotbarFull        RejectReason = "hotbar_full"
	RejectDropCapacity      RejectReason = "drop_capacity"
	RejectContainerCapacity RejectReason = "container_capacity"
)

type CommandRejected struct {
	Sequence uint64
	Reason   RejectReason
}

func (CommandRejected) serverMessage() {}
func (CommandRejected) serverPacket()  {}

func (rejection CommandRejected) Validate() error {
	if _, ok := commandRejectReasonID(rejection.Reason); !ok {
		return errors.New("network: unknown command rejection reason")
	}
	return nil
}
