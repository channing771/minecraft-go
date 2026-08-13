package network

import (
	"errors"

	"github.com/channing771/mornlea/internal/core"
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
		case OpenContainer:
			e.u64(message.Sequence)
			e.f32(message.Yaw)
			e.f32(message.Pitch)
		case MoveContainerStack:
			e.u64(message.Sequence)
			encodeContainerRef(&e, message.Container)
			e.u8(message.From)
			e.u8(message.To)
		case CloseContainer:
			e.u64(message.Sequence)
		case RequestChunkResync:
			e.u64(message.Sequence)
			e.i32(int32(message.Dimension))
			e.i32(message.Chunk.X)
			e.i32(message.Chunk.Z)
			e.u64(message.HaveRevision)
		case KeepAliveReply:
			e.u64(message.Token)
		case ChatCommand:
			e.string(message.Text, 1024)
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
	if state == StatePlay && packetID == 12 && len(payload) > chatCommandMaxWireBytes {
		return nil, codecError("decode client", state, packetID, errors.New("network: chat command payload exceeds 1026 bytes"))
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
			var open OpenContainer
			open.Sequence, err = d.u64()
			if err == nil {
				open.Yaw, err = d.f32()
			}
			if err == nil {
				open.Pitch, err = d.f32()
			}
			packet = open
		case 9:
			var move MoveContainerStack
			move.Sequence, err = d.u64()
			if err == nil {
				move.Container, err = decodeContainerRef(&d)
			}
			if err == nil {
				move.From, err = d.u8()
			}
			if err == nil {
				move.To, err = d.u8()
			}
			packet = move
		case 10:
			var closeContainer CloseContainer
			closeContainer.Sequence, err = d.u64()
			packet = closeContainer
		case 11:
			var drop DropSelectedItem
			drop.Sequence, err = d.u64()
			packet = drop
		case 12:
			var command ChatCommand
			command.Text, err = d.string(1024, 1024)
			packet = command
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

func validateClientWirePacket(state State, packet ClientPacket) error {
	return ValidateClientPacket(state, packet)
}
