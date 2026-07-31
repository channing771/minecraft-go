package network

import (
	"errors"
	"fmt"

	"minecraft-go/internal/core"
)

const MaxSmallPayload = 64 << 10

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
		case BreakBlock:
			e.u64(message.Sequence)
			e.f32(message.Yaw)
			e.f32(message.Pitch)
		case PlaceBlock:
			e.u64(message.Sequence)
			e.f32(message.Yaw)
			e.f32(message.Pitch)
			e.u16(uint16(message.Block))
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
			packet = PlayerInput{Sequence: sequence, MoveX: moveX, MoveZ: moveZ, Jump: jump, Yaw: yaw, Pitch: pitch}
		case 1:
			var sequence uint64
			var yaw, pitch float32
			sequence, err = d.u64()
			if err == nil {
				yaw, err = d.f32()
			}
			if err == nil {
				pitch, err = d.f32()
			}
			packet = BreakBlock{Sequence: sequence, Yaw: yaw, Pitch: pitch}
		case 2:
			var sequence uint64
			var yaw, pitch float32
			var block uint16
			sequence, err = d.u64()
			if err == nil {
				yaw, err = d.f32()
			}
			if err == nil {
				pitch, err = d.f32()
			}
			if err == nil {
				block, err = d.u16()
			}
			packet = PlaceBlock{Sequence: sequence, Yaw: yaw, Pitch: pitch, Block: core.BlockID(block)}
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
	// The login state machine must observe an otherwise well-formed hello in
	// order to return the frozen HandshakeVersionMismatch response. Outbound
	// callers remain unable to encode an unsupported version.
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
		case CommandRejected:
			reason, _ := commandRejectReasonID(message.Reason)
			e.u64(message.Sequence)
			e.u8(reason)
		case KeepAlive:
			e.u64(message.Token)
		case Disconnect:
			e.u8(uint8(message.Code))
			e.string(message.Message, 256)
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
	if err == nil && (count < 1 || count > 4096) {
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
