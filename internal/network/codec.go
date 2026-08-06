package network

import (
	"errors"
	"fmt"

	"minecraft-go/internal/core"
)

const MaxSmallPayload = 64 << 10

const (
	remotePlayerStateWireBytes   = 41
	remotePlayerStatesMaxPayload = 8 + 1 + 7*remotePlayerStateWireBytes
)

var (
	errInvalidDimension = errors.New("network: dimension is not overworld")
	errInvalidCount     = errors.New("network: packet count is outside 1..4096")
	errCountShortInput  = errors.New("network: packet count exceeds remaining payload")
)

func encodeClientPacketPayload(state State, packet ClientPacket) (packetID uint32, payload []byte, err error) {
	if err := validateClientWirePacket(state, packet); err != nil {
		return 0, nil, codecError("encode client", state, 0, err)
	}
	packetID, ok := clientPacketID(state, packet)
	if !ok {
		return 0, nil, codecError("encode client", state, 0, invalidClientPacket(state, packet))
	}
	var e byteEncoder
	switch state {
	case StateHandshake:
		message := packet.(ClientHello)
		e.uvarint(message.ProtocolVersion)
	case StateLogin:
		message := packet.(LoginStart)
		e.data = append(e.data, message.PlayerID[:]...)
		e.string(message.DisplayName, 128)
	case StatePlay:
		switch message := packet.(type) {
		case PlayerInput:
			e.u64(message.Sequence)
			e.i8(message.MoveX)
			e.i8(message.MoveZ)
			e.bool(message.Jump)
			e.f32(message.Yaw)
			e.f32(message.Pitch)
			e.bool(message.Mining)
		case PlaceBlock:
			e.u64(message.Sequence)
			e.f32(message.Yaw)
			e.f32(message.Pitch)
			e.u8(message.Slot)
		case SelectHotbar:
			e.u64(message.Sequence)
			e.u8(message.Slot)
		case MoveInventoryStack:
			e.u64(message.Sequence)
			e.u8(message.From)
			e.u8(message.To)
		case DropSelectedItem:
			e.u64(message.Sequence)
		case CraftRecipe:
			e.u64(message.Sequence)
			e.u8(uint8(message.Recipe))
		case OpenFurnace:
			e.u64(message.Sequence)
			e.f32(message.Yaw)
			e.f32(message.Pitch)
		case MoveFurnaceStack:
			e.u64(message.Sequence)
			encodeFurnaceRef(&e, message.Furnace)
			e.u8(message.From)
			e.u8(message.To)
		case CloseFurnace:
			e.u64(message.Sequence)
		case RequestChunkResync:
			e.u64(message.Sequence)
			e.i32(int32(message.Dimension))
			e.i32(message.Chunk.X)
			e.i32(message.Chunk.Z)
			e.u64(message.HaveRevision)
		case KeepAliveReply:
			e.u64(message.Token)
		default:
			return 0, nil, codecError("encode client", state, packetID, invalidClientPacket(state, packet))
		}
	default:
		return 0, nil, codecError("encode client", state, packetID, invalidClientPacket(state, packet))
	}
	return finishEncode("encode client", state, packetID, e)
}

func decodeClientPacketPayload(state State, packetID uint32, payload []byte) (ClientPacket, error) {
	if err := checkSmallPayload(payload); err != nil {
		return nil, codecError("decode client", state, packetID, err)
	}
	d := byteDecoder{data: payload}
	var packet ClientPacket
	var err error
	switch state {
	case StateHandshake:
		if packetID != 0 {
			return nil, codecError("decode client", state, packetID, errUnknownPacketID)
		}
		var version uint32
		version, err = d.uvarint()
		packet = ClientHello{ProtocolVersion: version}
	case StateLogin:
		if packetID != 0 {
			return nil, codecError("decode client", state, packetID, errUnknownPacketID)
		}
		var id core.PlayerID
		var name string
		if data, readErr := d.take(len(id)); readErr != nil {
			err = readErr
		} else {
			copy(id[:], data)
			name, err = d.string(MaxSmallPayload, MaxSmallPayload)
		}
		packet = LoginStart{PlayerID: id, DisplayName: name}
	case StatePlay:
		switch packetID {
		case 0:
			var sequence uint64
			var moveX, moveZ int8
			var jump bool
			var yaw, pitch float32
			var mining bool
			sequence, err = d.u64()
			if err == nil {
				moveX, err = d.i8()
			}
			if err == nil {
				moveZ, err = d.i8()
			}
			if err == nil {
				jump, err = d.bool()
			}
			if err == nil {
				yaw, err = d.f32()
			}
			if err == nil {
				pitch, err = d.f32()
			}
			if err == nil {
				mining, err = d.bool()
			}
			packet = PlayerInput{Sequence: sequence, MoveX: moveX, MoveZ: moveZ, Jump: jump, Yaw: yaw, Pitch: pitch, Mining: mining}
		case 2:
			var sequence uint64
			var yaw, pitch float32
			var slot uint8
			sequence, err = d.u64()
			if err == nil {
				yaw, err = d.f32()
			}
			if err == nil {
				pitch, err = d.f32()
			}
			if err == nil {
				slot, err = d.u8()
			}
			packet = PlaceBlock{Sequence: sequence, Yaw: yaw, Pitch: pitch, Slot: slot}
		case 3:
			var sequence, revision uint64
			var dimension, chunkX, chunkZ int32
			sequence, err = d.u64()
			if err == nil {
				dimension, err = d.i32()
			}
			if err == nil {
				chunkX, err = d.i32()
			}
			if err == nil {
				chunkZ, err = d.i32()
			}
			if err == nil {
				revision, err = d.u64()
			}
			packet = RequestChunkResync{Sequence: sequence, Dimension: core.DimensionID(dimension), Chunk: core.ChunkPos{X: chunkX, Z: chunkZ}, HaveRevision: revision}
		case 4:
			var token uint64
			token, err = d.u64()
			packet = KeepAliveReply{Token: token}
		case 5:
			var sequence uint64
			var slot uint8
			sequence, err = d.u64()
			if err == nil {
				slot, err = d.u8()
			}
			packet = SelectHotbar{Sequence: sequence, Slot: slot}
		case 6:
			var sequence uint64
			var from, to uint8
			sequence, err = d.u64()
			if err == nil {
				from, err = d.u8()
			}
			if err == nil {
				to, err = d.u8()
			}
			packet = MoveInventoryStack{Sequence: sequence, From: from, To: to}
		case 7:
			var sequence uint64
			var recipe uint8
			sequence, err = d.u64()
			if err == nil {
				recipe, err = d.u8()
			}
			packet = CraftRecipe{Sequence: sequence, Recipe: core.RecipeID(recipe)}
		case 8:
			var open OpenFurnace
			open.Sequence, err = d.u64()
			if err == nil {
				open.Yaw, err = d.f32()
			}
			if err == nil {
				open.Pitch, err = d.f32()
			}
			packet = open
		case 9:
			var move MoveFurnaceStack
			move.Sequence, err = d.u64()
			if err == nil {
				move.Furnace, err = decodeFurnaceRef(&d)
			}
			if err == nil {
				move.From, err = d.u8()
			}
			if err == nil {
				move.To, err = d.u8()
			}
			packet = move
		case 10:
			var closeFurnace CloseFurnace
			closeFurnace.Sequence, err = d.u64()
			packet = closeFurnace
		case 11:
			var drop DropSelectedItem
			drop.Sequence, err = d.u64()
			packet = drop
		default:
			return nil, codecError("decode client", state, packetID, errUnknownPacketID)
		}
	default:
		return nil, codecError("decode client", state, packetID, errUnknownPacketID)
	}
	if err == nil {
		err = d.done()
	}
	if err == nil {
		err = validateDecodedClientWirePacket(state, packet)
	}
	if err != nil {
		return nil, codecError("decode client", state, packetID, err)
	}
	return packet, nil
}

func validateDecodedClientWirePacket(state State, packet ClientPacket) error {
	// The login state machine must observe every structurally valid hello in
	// order to return the frozen HandshakeVersionMismatch response. Outbound
	// callers remain unable to encode unsupported versions.
	if state == StateHandshake {
		if _, ok := packet.(ClientHello); ok {
			return nil
		}
	}
	// A structurally complete LoginStart must reach the login driver so it can
	// return the frozen LoginInvalidIdentity code for semantic identity errors.
	if state == StateLogin {
		if _, ok := packet.(LoginStart); ok {
			return nil
		}
	}
	return validateClientWirePacket(state, packet)
}

func encodeServerControlPayload(state State, packet ServerPacket) (packetID uint32, payload []byte, err error) {
	if state == StatePlay {
		if _, ok := packet.(ChunkSnapshot); ok {
			return 0, nil, codecError("encode server", state, 0, errSnapshotDelegated)
		}
	}
	if err := validateServerWirePacket(state, packet); err != nil {
		return 0, nil, codecError("encode server", state, 0, err)
	}
	// 协议 v10 不能表示磨损值；过渡期只允许满耐久工具进入编码器，
	// 避免解码 shim 把磨损值静默补满。Task 5 升级到 v11 时与该 shim 一并删除。
	if message, ok := packet.(InventoryState); ok && !inventoryRepresentableV10(message.Inventory) {
		return 0, nil, codecError("encode server", state, 0, errors.New("network: protocol v10 cannot encode worn tool durability"))
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
				e.u16(uint16(stack.Item))
				e.u8(stack.Count)
			}
			for _, stack := range message.Inventory.Backpack {
				e.u16(uint16(stack.Item))
				e.u8(stack.Count)
			}
		case FurnaceState:
			encodeFurnaceRef(&e, message.Furnace)
			for _, stack := range [3]core.ItemStack{message.Input, message.Fuel, message.Output} {
				e.u16(uint16(stack.Item))
				e.u8(stack.Count)
			}
			e.u8(message.ProgressTicks)
			e.u16(message.BurnTicks)
		case FurnaceClosed:
			encodeFurnaceRef(&e, message.Furnace)
		case ItemDropUpserts:
			e.u64(message.ServerTick)
			e.uvarint(uint32(len(message.Drops)))
			for _, drop := range message.Drops {
				encodeDropID(&e, drop.ID)
				e.u32(drop.BlockIndex)
				e.u16(uint16(drop.Item))
				e.u8(drop.Count)
			}
		case ItemDropRemoves:
			e.u64(message.ServerTick)
			e.uvarint(uint32(len(message.IDs)))
			for _, id := range message.IDs {
				encodeDropID(&e, id)
			}
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
			state.Furnace, err = decodeFurnaceRef(&d)
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
			var closed FurnaceClosed
			closed.Furnace, err = decodeFurnaceRef(&d)
			packet = closed
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

const (
	dropIDWireBytes   = 4 + 4 + 4 + 1 + 4
	itemDropWireBytes = dropIDWireBytes + 4 + 2 + 1
)

// decodeItemStack 读取一格固定编码；沿用已有错误则原样返回。
func decodeItemStack(d *byteDecoder, err error) (core.ItemStack, error) {
	if err != nil {
		return core.ItemStack{}, err
	}
	item, err := d.u16()
	if err != nil {
		return core.ItemStack{}, err
	}
	count, err := d.u8()
	if err != nil {
		return core.ItemStack{}, err
	}
	stack := core.ItemStack{Item: core.ItemID(item), Count: count}
	// 协议 v10 的物品负载只有 Item/Count，没有耐久字节；编码器已拒绝磨损工具，
	// 因此解码时只会恢复满耐久工具。Task 5 升级到 v11 读取真实字段时必须
	// 把这个 shim 与编码可表示性门禁一并删除。
	if max, ok := core.ItemMaxDurability(stack.Item); ok {
		stack.Durability = max
	}
	return stack, nil
}

func inventoryRepresentableV10(inventory core.Inventory) bool {
	for slot := uint8(0); slot < core.InventorySlots; slot++ {
		stack, _ := inventory.Slot(slot)
		if max, ok := core.ItemMaxDurability(stack.Item); ok && stack.Durability != max {
			return false
		}
	}
	return true
}

// furnaceRefWireBytes 是熔炉引用的固定编码长度。
const furnaceRefWireBytes = 4 + 4 + 4 + 1 + 4

func encodeFurnaceRef(e *byteEncoder, ref core.FurnaceRef) {
	e.i32(int32(ref.Dimension))
	e.i32(ref.Chunk.X)
	e.i32(ref.Chunk.Z)
	e.u8(ref.Slot)
	e.u32(ref.Generation)
}

func decodeFurnaceRef(d *byteDecoder) (core.FurnaceRef, error) {
	var ref core.FurnaceRef
	dimension, err := d.i32()
	if err != nil {
		return core.FurnaceRef{}, err
	}
	ref.Dimension = core.DimensionID(dimension)
	if ref.Chunk.X, err = d.i32(); err != nil {
		return core.FurnaceRef{}, err
	}
	if ref.Chunk.Z, err = d.i32(); err != nil {
		return core.FurnaceRef{}, err
	}
	if ref.Slot, err = d.u8(); err != nil {
		return core.FurnaceRef{}, err
	}
	if ref.Generation, err = d.u32(); err != nil {
		return core.FurnaceRef{}, err
	}
	return ref, nil
}

func encodeDropID(e *byteEncoder, id core.DropID) {
	e.i32(int32(id.Dimension))
	e.i32(id.Chunk.X)
	e.i32(id.Chunk.Z)
	e.u8(id.Slot)
	e.u32(id.Generation)
}

func decodeDropID(d *byteDecoder) (core.DropID, error) {
	var id core.DropID
	dimension, err := d.i32()
	if err != nil {
		return core.DropID{}, err
	}
	id.Dimension = core.DimensionID(dimension)
	if id.Chunk.X, err = d.i32(); err != nil {
		return core.DropID{}, err
	}
	if id.Chunk.Z, err = d.i32(); err != nil {
		return core.DropID{}, err
	}
	if id.Slot, err = d.u8(); err != nil {
		return core.DropID{}, err
	}
	if id.Generation, err = d.u32(); err != nil {
		return core.DropID{}, err
	}
	return id, nil
}

// decodeItemDropBatchCount 在分配任何切片前校验计数与剩余字节。
func decodeItemDropBatchCount(d *byteDecoder, itemBytes int) (uint32, error) {
	count, err := d.uvarint()
	if err != nil {
		return 0, err
	}
	if count < 1 || count > MaxItemDropBatch {
		return 0, errors.New("network: item drop batch count is outside 1..32")
	}
	if len(d.data)-d.offset < int(count)*itemBytes {
		return 0, errCountShortInput
	}
	return count, nil
}

func decodeItemDropUpserts(d *byteDecoder) (ServerPacket, error) {
	var result ItemDropUpserts
	serverTick, err := d.u64()
	if err != nil {
		return nil, err
	}
	result.ServerTick = serverTick
	count, err := decodeItemDropBatchCount(d, itemDropWireBytes)
	if err != nil {
		return nil, err
	}
	result.Drops = make([]ItemDrop, count)
	for index := range result.Drops {
		drop := &result.Drops[index]
		if drop.ID, err = decodeDropID(d); err != nil {
			return nil, err
		}
		if drop.BlockIndex, err = d.u32(); err != nil {
			return nil, err
		}
		item, err := d.u16()
		if err != nil {
			return nil, err
		}
		drop.Item = core.ItemID(item)
		if drop.Count, err = d.u8(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func decodeItemDropRemoves(d *byteDecoder) (ServerPacket, error) {
	var result ItemDropRemoves
	serverTick, err := d.u64()
	if err != nil {
		return nil, err
	}
	result.ServerTick = serverTick
	count, err := decodeItemDropBatchCount(d, dropIDWireBytes)
	if err != nil {
		return nil, err
	}
	result.IDs = make([]core.DropID, count)
	for index := range result.IDs {
		if result.IDs[index], err = decodeDropID(d); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func decodeRemotePlayerStates(d *byteDecoder) (ServerPacket, error) {
	var result RemotePlayerStates
	serverTick, err := d.u64()
	result.ServerTick = serverTick
	var count uint32
	if err == nil {
		count, err = d.uvarint()
	}
	if err == nil && (count < 1 || count > 7) {
		err = errors.New("network: remote player state count is outside 1..7")
	}
	if err == nil && len(d.data)-d.offset < int(count)*remotePlayerStateWireBytes {
		err = errors.New("network: remote player states exceed remaining payload")
	}
	if err == nil {
		result.Players = make([]RemotePlayerState, int(count))
		for index := range result.Players {
			player := &result.Players[index]
			var id []byte
			id, err = d.take(len(player.PlayerID))
			if err == nil {
				copy(player.PlayerID[:], id)
			}
			var dimension int32
			if err == nil {
				dimension, err = d.i32()
				player.Dimension = core.DimensionID(dimension)
			}
			for component := range player.Position {
				if err == nil {
					player.Position[component], err = d.f32()
				}
			}
			if err == nil {
				player.Yaw, err = d.f32()
			}
			if err == nil {
				player.Pitch, err = d.f32()
			}
			if err == nil {
				player.Reset, err = d.bool()
			}
			if err != nil {
				break
			}
		}
	}
	return result, err
}

func decodeBlockChanges(d *byteDecoder) (ServerPacket, error) {
	var result BlockChanges
	var dimension int32
	var err error
	dimension, err = d.i32()
	result.Dimension = core.DimensionID(dimension)
	if err == nil {
		result.Chunk.X, err = d.i32()
	}
	if err == nil {
		result.Chunk.Z, err = d.i32()
	}
	if err == nil {
		result.BaseRevision, err = d.u64()
	}
	if err == nil {
		result.NewRevision, err = d.u64()
	}
	var count uint32
	if err == nil {
		count, err = d.uvarint()
	}
	// v4 允许零条方块变化作为纯掉落物变化的 revision barrier。
	if err == nil && count > 4096 {
		err = errInvalidCount
	}
	if err == nil && len(d.data)-d.offset < int(count)*14 {
		err = errCountShortInput
	}
	if err == nil {
		result.Changes = make([]BlockChange, int(count))
		for index := range result.Changes {
			if result.Changes[index].Position.X, err = d.i32(); err != nil {
				break
			}
			if result.Changes[index].Position.Y, err = d.i32(); err != nil {
				break
			}
			if result.Changes[index].Position.Z, err = d.i32(); err != nil {
				break
			}
			var block uint16
			if block, err = d.u16(); err != nil {
				break
			}
			result.Changes[index].Block = core.BlockID(block)
		}
	}
	return result, err
}

func decodeForgetChunks(d *byteDecoder) (ServerPacket, error) {
	var result ForgetChunks
	dimension, err := d.i32()
	result.Dimension = core.DimensionID(dimension)
	var count uint32
	if err == nil {
		count, err = d.uvarint()
	}
	if err == nil && (count < 1 || count > 4096) {
		err = errInvalidCount
	}
	if err == nil && len(d.data)-d.offset < int(count)*8 {
		err = errCountShortInput
	}
	if err == nil {
		result.Chunks = make([]core.ChunkPos, int(count))
		for index := range result.Chunks {
			if result.Chunks[index].X, err = d.i32(); err != nil {
				break
			}
			if result.Chunks[index].Z, err = d.i32(); err != nil {
				break
			}
		}
	}
	return result, err
}

func validateClientWirePacket(state State, packet ClientPacket) error {
	return ValidateClientPacket(state, packet)
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

func finishEncode(direction string, state State, packetID uint32, e byteEncoder) (uint32, []byte, error) {
	if e.err != nil {
		return 0, nil, codecError(direction, state, packetID, e.err)
	}
	if err := checkSmallPayload(e.data); err != nil {
		return 0, nil, codecError(direction, state, packetID, err)
	}
	return packetID, e.data, nil
}

func checkSmallPayload(payload []byte) error {
	if len(payload) > MaxSmallPayload {
		return errors.New("network: payload exceeds 64 KiB")
	}
	return nil
}

var (
	errUnknownPacketID   = errors.New("network: unknown packet ID")
	errSnapshotDelegated = errors.New("network: Play/S→C/ID 0 ChunkSnapshot is handled by Task 5")
)

func codecError(direction string, state State, packetID uint32, err error) error {
	return fmt.Errorf("network: %s state=%d packetID=%d: %w", direction, state, packetID, err)
}
