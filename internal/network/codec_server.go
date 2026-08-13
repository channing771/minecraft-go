package network

import (
	"errors"

	"github.com/channing771/mornlea/internal/core"
)

func encodeServerControlPayload(state State, packet ServerPacket) (packetID uint32, payload []byte, err error) {
	if state == StatePlay {
		if _, ok := packet.(ChunkSnapshot); ok {
			return 0, nil, codecError("encode server", state, 0, errSnapshotDelegated)
		}
	}
	if err := validateServerWirePacket(state, packet); err != nil {
		return 0, nil, codecError("encode server", state, 0, err)
	}
	packetID, ok := serverPacketID(state, packet)
	if !ok {
		return 0, nil, codecError("encode server", state, 0, invalidServerPacket(state, packet))
	}
	var e byteEncoder
	switch state {
	case StateHandshake:
		switch message := packet.(type) {
		case ServerHello:
			e.uvarint(message.ProtocolVersion)
		case HandshakeReject:
			e.uvarint(message.ServerProtocolVersion)
			e.u8(uint8(message.Code))
			e.string(message.Message, 256)
		}
	case StateLogin:
		switch message := packet.(type) {
		case LoginSuccess:
			e.data = append(e.data, message.PlayerID[:]...)
		case LoginReject:
			e.u8(uint8(message.Code))
			e.string(message.Message, 256)
		}
	case StatePlay:
		switch message := packet.(type) {
		case BlockChanges:
			e.i32(int32(message.Dimension))
			e.i32(message.Chunk.X)
			e.i32(message.Chunk.Z)
			e.u64(message.BaseRevision)
			e.u64(message.NewRevision)
			e.uvarint(uint32(len(message.Changes)))
			for _, change := range message.Changes {
				e.i32(change.Position.X)
				e.i32(change.Position.Y)
				e.i32(change.Position.Z)
				e.u16(uint16(change.Block))
			}
		case ForgetChunks:
			e.i32(int32(message.Dimension))
			e.uvarint(uint32(len(message.Chunks)))
			for _, chunk := range message.Chunks {
				e.i32(chunk.X)
				e.i32(chunk.Z)
			}
		case PlayerState:
			e.u64(message.ServerTick)
			e.u64(message.LastInputSequence)
			e.i32(int32(message.Dimension))
			for _, value := range message.Position {
				e.f32(value)
			}
			for _, value := range message.Velocity {
				e.f32(value)
			}
			e.f32(message.Yaw)
			e.f32(message.Pitch)
			e.bool(message.OnGround)
			e.bool(message.Ready)
			e.bool(message.Reset)
			e.bool(message.MiningActive)
			e.i32(message.MiningTarget.X)
			e.i32(message.MiningTarget.Y)
			e.i32(message.MiningTarget.Z)
			e.u16(message.MiningProgressTicks)
			e.u16(message.MiningRequiredTicks)
			e.bool(message.MiningHarvestable)
			e.u8(message.Health)
			e.u64(message.WorldTimeTicks)
		case CommandRejected:
			reason, _ := commandRejectReasonID(message.Reason)
			e.u64(message.Sequence)
			e.u8(reason)
		case KeepAlive:
			e.u64(message.Token)
		case Disconnect:
			e.u8(uint8(message.Code))
			e.string(message.Message, 256)
		case RemotePlayerSpawn:
			e.data = append(e.data, message.PlayerID[:]...)
			e.string(message.DisplayName, 128)
			e.u64(message.ServerTick)
			e.i32(int32(message.Dimension))
			for _, value := range message.Position {
				e.f32(value)
			}
			e.f32(message.Yaw)
			e.f32(message.Pitch)
		case RemotePlayerDespawn:
			e.data = append(e.data, message.PlayerID[:]...)
		case RemotePlayerStates:
			e.u64(message.ServerTick)
			e.uvarint(uint32(len(message.Players)))
			for _, player := range message.Players {
				e.data = append(e.data, player.PlayerID[:]...)
				e.i32(int32(player.Dimension))
				for _, value := range player.Position {
					e.f32(value)
				}
				e.f32(player.Yaw)
				e.f32(player.Pitch)
				e.bool(player.Reset)
			}
		case InventoryState:
			e.u8(message.Inventory.Hotbar.Selected)
			for _, stack := range message.Inventory.Hotbar.Slots {
				encodeItemStack(&e, stack)
			}
			for _, stack := range message.Inventory.Backpack {
				encodeItemStack(&e, stack)
			}
		case FurnaceState:
			encodeContainerRef(&e, message.Furnace)
			for _, stack := range [3]core.ItemStack{message.Input, message.Fuel, message.Output} {
				encodeItemStack(&e, stack)
			}
			e.u8(message.ProgressTicks)
			e.u16(message.BurnTicks)
		case ChestState:
			encodeContainerRef(&e, message.Chest)
			for _, stack := range message.Items {
				encodeItemStack(&e, stack)
			}
		case ContainerClosed:
			encodeContainerRef(&e, message.Container)
		case ItemDropUpserts:
			e.u64(message.ServerTick)
			e.uvarint(uint32(len(message.Drops)))
			for _, drop := range message.Drops {
				encodeDropID(&e, drop.ID)
				e.u32(drop.BlockIndex)
				encodeItemStack(&e, core.ItemStack{
					Item: drop.Item, Count: drop.Count, Durability: drop.Durability,
				})
			}
		case ItemDropRemoves:
			e.u64(message.ServerTick)
			e.uvarint(uint32(len(message.IDs)))
			for _, id := range message.IDs {
				encodeDropID(&e, id)
			}
		case ChatEvent:
			encodeChatEvent(&e, message)
		case CompanionSpawn:
			encodeCompanionSpawn(&e, message)
		case CompanionStates:
			encodeCompanionStates(&e, message)
		case CompanionDespawn:
			e.data = append(e.data, message.ID[:]...)
		default:
			return 0, nil, codecError("encode server", state, packetID, invalidServerPacket(state, packet))
		}
	default:
		return 0, nil, codecError("encode server", state, packetID, invalidServerPacket(state, packet))
	}
	return finishEncode("encode server", state, packetID, e)
}

func decodeServerControlPayload(state State, packetID uint32, payload []byte) (ServerPacket, error) {
	if err := checkSmallPayload(payload); err != nil {
		return nil, codecError("decode server", state, packetID, err)
	}
	if state == StatePlay {
		var max int
		switch packetID {
		case 16:
			max = chatEventMaxWireBytes
		case 17:
			max = companionSpawnMaxWireBytes
		case 18:
			max = companionStatesMaxWireBytes
		case 19:
			max = len(CompanionDespawn{}.ID)
		}
		if max > 0 && len(payload) > max {
			return nil, codecError("decode server", state, packetID, errors.New("network: companion payload exceeds fixed maximum"))
		}
	}
	if state == StatePlay && packetID == 9 && len(payload) > remotePlayerStatesMaxPayload {
		return nil, codecError("decode server", state, packetID, errors.New("network: remote player states payload exceeds 296 bytes"))
	}
	if state == StatePlay && packetID == 0 {
		return nil, codecError("decode server", state, packetID, errSnapshotDelegated)
	}
	d := byteDecoder{data: payload}
	var packet ServerPacket
	var err error
	switch state {
	case StateHandshake:
		switch packetID {
		case 0:
			var version uint32
			version, err = d.uvarint()
			packet = ServerHello{ProtocolVersion: version}
		case 1:
			var version uint32
			var code uint8
			var message string
			version, err = d.uvarint()
			if err == nil {
				code, err = d.u8()
			}
			if err == nil {
				message, err = d.string(256, 256)
			}
			packet = HandshakeReject{ServerProtocolVersion: version, Code: HandshakeRejectCode(code), Message: message}
		default:
			return nil, codecError("decode server", state, packetID, errUnknownPacketID)
		}
	case StateLogin:
		switch packetID {
		case 0:
			var id core.PlayerID
			if data, readErr := d.take(len(id)); readErr != nil {
				err = readErr
			} else {
				copy(id[:], data)
			}
			packet = LoginSuccess{PlayerID: id}
		case 1:
			var code uint8
			var message string
			code, err = d.u8()
			if err == nil {
				message, err = d.string(256, 256)
			}
			packet = LoginReject{Code: LoginRejectCode(code), Message: message}
		default:
			return nil, codecError("decode server", state, packetID, errUnknownPacketID)
		}
	case StatePlay:
		switch packetID {
		case 1:
			packet, err = decodeBlockChanges(&d)
		case 2:
			packet, err = decodeForgetChunks(&d)
		case 3:
			var statePacket PlayerState
			statePacket.ServerTick, err = d.u64()
			if err == nil {
				statePacket.LastInputSequence, err = d.u64()
			}
			var dimension int32
			if err == nil {
				dimension, err = d.i32()
				statePacket.Dimension = core.DimensionID(dimension)
			}
			for index := range statePacket.Position {
				if err == nil {
					statePacket.Position[index], err = d.f32()
				}
			}
			for index := range statePacket.Velocity {
				if err == nil {
					statePacket.Velocity[index], err = d.f32()
				}
			}
			if err == nil {
				statePacket.Yaw, err = d.f32()
			}
			if err == nil {
				statePacket.Pitch, err = d.f32()
			}
			if err == nil {
				statePacket.OnGround, err = d.bool()
			}
			if err == nil {
				statePacket.Ready, err = d.bool()
			}
			if err == nil {
				statePacket.Reset, err = d.bool()
			}
			if err == nil {
				statePacket.MiningActive, err = d.bool()
			}
			if err == nil {
				statePacket.MiningTarget.X, err = d.i32()
			}
			if err == nil {
				statePacket.MiningTarget.Y, err = d.i32()
			}
			if err == nil {
				statePacket.MiningTarget.Z, err = d.i32()
			}
			if err == nil {
				statePacket.MiningProgressTicks, err = d.u16()
			}
			if err == nil {
				statePacket.MiningRequiredTicks, err = d.u16()
			}
			if err == nil {
				statePacket.MiningHarvestable, err = d.bool()
			}
			if err == nil {
				statePacket.Health, err = d.u8()
			}
			if err == nil {
				statePacket.WorldTimeTicks, err = d.u64()
			}
			packet = statePacket
		case 4:
			var sequence uint64
			var reasonID uint8
			sequence, err = d.u64()
			if err == nil {
				reasonID, err = d.u8()
			}
			reason, ok := commandRejectReasonForID(reasonID)
			if err == nil && !ok {
				err = errors.New("network: unknown command rejection reason ID")
			}
			packet = CommandRejected{Sequence: sequence, Reason: reason}
		case 5:
			var token uint64
			token, err = d.u64()
			packet = KeepAlive{Token: token}
		case 6:
			var code uint8
			var message string
			code, err = d.u8()
			if err == nil {
				message, err = d.string(256, 256)
			}
			packet = Disconnect{Code: DisconnectCode(code), Message: message}
		case 7:
			var spawn RemotePlayerSpawn
			if data, readErr := d.take(len(spawn.PlayerID)); readErr != nil {
				err = readErr
			} else {
				copy(spawn.PlayerID[:], data)
				spawn.DisplayName, err = d.string(128, 32)
			}
			if err == nil {
				spawn.ServerTick, err = d.u64()
			}
			var dimension int32
			if err == nil {
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
			packet = spawn
		case 8:
			var despawn RemotePlayerDespawn
			if data, readErr := d.take(len(despawn.PlayerID)); readErr != nil {
				err = readErr
			} else {
				copy(despawn.PlayerID[:], data)
			}
			packet = despawn
		case 9:
			packet, err = decodeRemotePlayerStates(&d)
		case 11:
			packet, err = decodeItemDropUpserts(&d)
		case 12:
			packet, err = decodeItemDropRemoves(&d)
		case 10:
			var inventory core.Inventory
			inventory.Hotbar.Selected, err = d.u8()
			for index := range inventory.Hotbar.Slots {
				inventory.Hotbar.Slots[index], err = decodeItemStack(&d, err)
			}
			for index := range inventory.Backpack {
				inventory.Backpack[index], err = decodeItemStack(&d, err)
			}
			packet = InventoryState{Inventory: inventory}
		case 13:
			var state FurnaceState
			state.Furnace, err = decodeContainerRef(&d)
			state.Input, err = decodeItemStack(&d, err)
			state.Fuel, err = decodeItemStack(&d, err)
			state.Output, err = decodeItemStack(&d, err)
			if err == nil {
				state.ProgressTicks, err = d.u8()
			}
			if err == nil {
				state.BurnTicks, err = d.u16()
			}
			packet = state
		case 14:
			var closed ContainerClosed
			closed.Container, err = decodeContainerRef(&d)
			packet = closed
		case 15:
			var chest ChestState
			chest.Chest, err = decodeContainerRef(&d)
			for index := range chest.Items {
				chest.Items[index], err = decodeItemStack(&d, err)
			}
			packet = chest
		case 16:
			packet, err = decodeChatEvent(&d)
		case 17:
			packet, err = decodeCompanionSpawn(&d)
		case 18:
			packet, err = decodeCompanionStates(&d)
		case 19:
			var despawn CompanionDespawn
			err = decodeFixedID(&d, despawn.ID[:])
			packet = despawn
		default:
			return nil, codecError("decode server", state, packetID, errUnknownPacketID)
		}
	default:
		return nil, codecError("decode server", state, packetID, errUnknownPacketID)
	}
	if err == nil {
		err = d.done()
	}
	if err == nil {
		err = validateServerWirePacket(state, packet)
	}
	if err != nil {
		return nil, codecError("decode server", state, packetID, err)
	}
	return packet, nil
}

func validateServerWirePacket(state State, packet ServerPacket) error {
	if err := ValidateServerPacket(state, packet); err != nil {
		return err
	}
	switch message := packet.(type) {
	case BlockChanges:
		if message.Dimension != core.Overworld {
			return errInvalidDimension
		}
	case ForgetChunks:
		if message.Dimension != core.Overworld {
			return errInvalidDimension
		}
	case PlayerState:
		if message.Dimension != core.Overworld {
			return errInvalidDimension
		}
	}
	return nil
}
