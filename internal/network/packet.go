package network

import (
	"errors"
	"fmt"

	"minecraft-go/internal/core"
)

// ProtocolVersion 是 M4F 唯一支持的协议版本。
const ProtocolVersion uint32 = 8

// State 标识连接当前允许交换的 packet 集合。
type State uint8

const (
	StateHandshake State = iota + 1
	StateLogin
	StatePlay
)

// ClientPacket 是客户端可在线上发送的封闭 packet 集合。
type ClientPacket interface {
	clientPacket()
}

// ServerPacket 是服务端可在线上发送的封闭 packet 集合。
type ServerPacket interface {
	serverPacket()
}

type ClientHello struct {
	ProtocolVersion uint32
}

func (ClientHello) clientPacket() {}

type ServerHello struct {
	ProtocolVersion uint32
}

func (ServerHello) serverPacket() {}

type HandshakeRejectCode uint8

const (
	HandshakeVersionMismatch HandshakeRejectCode = 1
)

type HandshakeReject struct {
	ServerProtocolVersion uint32
	Code                  HandshakeRejectCode
	Message               string
}

func (HandshakeReject) serverPacket() {}

type LoginStart struct {
	PlayerID    core.PlayerID
	DisplayName string
}

func (LoginStart) clientPacket() {}

type LoginSuccess struct {
	PlayerID core.PlayerID
}

func (LoginSuccess) serverPacket() {}

type LoginRejectCode uint8

const (
	LoginServerFull LoginRejectCode = iota + 1
	LoginInvalidIdentity
	LoginPlayerDataCorrupt
	LoginStoreUnavailable
	LoginProtocolViolation
	LoginInternalError
	LoginAlreadyOnline
)

type LoginReject struct {
	Code    LoginRejectCode
	Message string
}

func (LoginReject) serverPacket() {}

type KeepAlive struct {
	Token uint64
}

func (KeepAlive) serverPacket()  {}
func (KeepAlive) serverMessage() {}

type KeepAliveReply struct {
	Token uint64
}

func (KeepAliveReply) clientPacket()  {}
func (KeepAliveReply) clientMessage() {}

type DisconnectCode uint8

const (
	DisconnectProtocolViolation DisconnectCode = iota + 1
	DisconnectTimeout
	DisconnectServerShutdown
	DisconnectSlowClient
	DisconnectInternalError
)

type Disconnect struct {
	Code    DisconnectCode
	Message string
}

func (Disconnect) serverPacket()  {}
func (Disconnect) serverMessage() {}

// ValidateClientPacket 验证 state、packet 类型及其线上字段。
func ValidateClientPacket(state State, packet ClientPacket) error {
	switch state {
	case StateHandshake:
		clientHello, ok := packet.(ClientHello)
		if !ok {
			return invalidClientPacket(state, packet)
		}
		if clientHello.ProtocolVersion != ProtocolVersion {
			return fmt.Errorf("network: unsupported client protocol version %d", clientHello.ProtocolVersion)
		}
		return nil
	case StateLogin:
		loginStart, ok := packet.(LoginStart)
		if !ok {
			return invalidClientPacket(state, packet)
		}
		if !loginStart.PlayerID.Valid() {
			return errors.New("network: login player ID is not UUIDv4")
		}
		if _, err := core.NormalizeDisplayName(loginStart.DisplayName); err != nil {
			return fmt.Errorf("network: invalid login display name: %w", err)
		}
		return nil
	case StatePlay:
		switch clientPacket := packet.(type) {
		case PlayerInput:
			return clientPacket.Validate()
		case PlaceBlock:
			return clientPacket.Validate()
		case SelectHotbar:
			return clientPacket.Validate()
		case MoveInventoryStack:
			return clientPacket.Validate()
		case CraftRecipe:
			return clientPacket.Validate()
		case OpenFurnace:
			return clientPacket.Validate()
		case MoveFurnaceStack:
			return clientPacket.Validate()
		case CloseFurnace:
			return clientPacket.Validate()
		case RequestChunkResync:
			return clientPacket.Validate()
		case KeepAliveReply:
			if clientPacket.Token == 0 {
				return errors.New("network: keep alive reply token is zero")
			}
			return nil
		default:
			return invalidClientPacket(state, packet)
		}
	default:
		return invalidClientPacket(state, packet)
	}
}

// ValidateServerPacket 验证 state、packet 类型及其线上字段。
func ValidateServerPacket(state State, packet ServerPacket) error {
	switch state {
	case StateHandshake:
		switch serverPacket := packet.(type) {
		case ServerHello:
			if serverPacket.ProtocolVersion != ProtocolVersion {
				return fmt.Errorf("network: unsupported server protocol version %d", serverPacket.ProtocolVersion)
			}
			return nil
		case HandshakeReject:
			if !validHandshakeRejectCode(serverPacket.Code) {
				return fmt.Errorf("network: invalid handshake reject code %d", serverPacket.Code)
			}
			return nil
		default:
			return invalidServerPacket(state, packet)
		}
	case StateLogin:
		switch serverPacket := packet.(type) {
		case LoginSuccess:
			if !serverPacket.PlayerID.Valid() {
				return errors.New("network: login success player ID is not UUIDv4")
			}
			return nil
		case LoginReject:
			if !validLoginRejectCode(serverPacket.Code) {
				return fmt.Errorf("network: invalid login reject code %d", serverPacket.Code)
			}
			return nil
		default:
			return invalidServerPacket(state, packet)
		}
	case StatePlay:
		switch serverPacket := packet.(type) {
		case ChunkSnapshot:
			return serverPacket.Validate()
		case BlockChanges:
			return serverPacket.Validate()
		case ForgetChunks:
			return serverPacket.Validate()
		case PlayerState:
			return serverPacket.Validate()
		case CommandRejected:
			return serverPacket.Validate()
		case KeepAlive:
			if serverPacket.Token == 0 {
				return errors.New("network: keep alive token is zero")
			}
			return nil
		case Disconnect:
			if !validDisconnectCode(serverPacket.Code) {
				return fmt.Errorf("network: invalid disconnect code %d", serverPacket.Code)
			}
			return nil
		case RemotePlayerSpawn:
			return serverPacket.Validate()
		case RemotePlayerDespawn:
			return serverPacket.Validate()
		case RemotePlayerStates:
			return serverPacket.Validate()
		case InventoryState:
			return serverPacket.Validate()
		case ItemDropUpserts:
			return serverPacket.Validate()
		case ItemDropRemoves:
			return serverPacket.Validate()
		case FurnaceState:
			return serverPacket.Validate()
		case FurnaceClosed:
			return serverPacket.Validate()
		default:
			return invalidServerPacket(state, packet)
		}
	default:
		return invalidServerPacket(state, packet)
	}
}

func validHandshakeRejectCode(code HandshakeRejectCode) bool {
	return code == HandshakeVersionMismatch
}

func validLoginRejectCode(code LoginRejectCode) bool {
	switch code {
	case LoginServerFull, LoginInvalidIdentity, LoginPlayerDataCorrupt,
		LoginStoreUnavailable, LoginProtocolViolation, LoginInternalError, LoginAlreadyOnline:
		return true
	default:
		return false
	}
}

func validDisconnectCode(code DisconnectCode) bool {
	switch code {
	case DisconnectProtocolViolation, DisconnectTimeout, DisconnectServerShutdown,
		DisconnectSlowClient, DisconnectInternalError:
		return true
	default:
		return false
	}
}

func invalidClientPacket(state State, packet ClientPacket) error {
	return fmt.Errorf("network: client packet %T is not valid in state %d", packet, state)
}

func invalidServerPacket(state State, packet ServerPacket) error {
	return fmt.Errorf("network: server packet %T is not valid in state %d", packet, state)
}
