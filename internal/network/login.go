package network

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"minecraft-go/internal/core"
)

type Identity struct {
	PlayerID    core.PlayerID
	DisplayName string
}

const (
	HandshakeTimeout = 5 * time.Second
	LoginTimeout     = 10 * time.Second
)

type RemoteError struct {
	State   State
	Code    uint8
	Message string
}

func (err *RemoteError) Error() string {
	return fmt.Sprintf("network: remote error in state %d (code %d): %s", err.State, err.Code, err.Message)
}

type PendingLogin struct {
	stream    ServerPacketStream
	identity  Identity
	decided   atomic.Bool
	login     context.Context
	cancel    context.CancelFunc
	stop      func() bool
	phaseMu   sync.Mutex
	handedOff bool
	phaseErr  error
	phaseDone chan struct{}
}

func BeginServerLogin(ctx context.Context, stream ServerPacketStream) (_ *PendingLogin, err error) {
	if stream == nil {
		return nil, errors.New("network: nil server packet stream")
	}
	defer func() {
		if err != nil {
			_ = stream.Close()
		}
	}()

	handshake, cancelHandshake := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancelHandshake()
	packet, err := stream.Recv(handshake, StateHandshake)
	if err != nil {
		return nil, loginReceiveError(err)
	}
	hello, ok := packet.(ClientHello)
	if !ok || hello.ProtocolVersion != ProtocolVersion {
		_ = stream.Send(handshake, StateHandshake, HandshakeReject{
			ServerProtocolVersion: ProtocolVersion,
			Code:                  HandshakeVersionMismatch,
			Message:               "协议版本不匹配",
		})
		return nil, protocolViolation(errors.New("unexpected client handshake packet"))
	}
	if err := stream.Send(handshake, StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}); err != nil {
		return nil, err
	}

	login, cancelLogin := context.WithTimeout(ctx, LoginTimeout)
	defer func() {
		if err != nil {
			cancelLogin()
		}
	}()
	packet, err = stream.Recv(login, StateLogin)
	if err != nil {
		return nil, loginReceiveError(err)
	}
	start, ok := packet.(LoginStart)
	if !ok {
		return nil, protocolViolation(errors.New("unexpected client login packet"))
	}
	canonicalName, identityErr := core.NormalizeDisplayName(start.DisplayName)
	if !start.PlayerID.Valid() || identityErr != nil {
		_ = stream.Send(login, StateLogin, LoginReject{
			Code:    LoginInvalidIdentity,
			Message: "玩家 ID 或昵称非法",
		})
		return nil, errors.New("network: invalid login identity")
	}
	pending := &PendingLogin{
		stream:    stream,
		identity:  Identity{PlayerID: start.PlayerID, DisplayName: canonicalName},
		login:     login,
		cancel:    cancelLogin,
		phaseDone: make(chan struct{}),
	}
	pending.stop = context.AfterFunc(login, pending.expire)
	return pending, nil
}

func (pending *PendingLogin) Identity() Identity {
	return pending.identity
}

// Context owns the Login phase deadline and is canceled when that phase ends.
func (pending *PendingLogin) Context() context.Context {
	return pending.login
}

func (pending *PendingLogin) Accept(ctx context.Context, attach func(ServerEndpoint) error) error {
	if attach == nil {
		return errors.New("network: nil login attach callback")
	}
	if err := pending.decide(); err != nil {
		return err
	}
	endpoint := newGatedServerPlayEndpoint(pending.stream, pending.login)
	if err := attach(endpoint); err != nil {
		endpoint.abort()
		_ = pending.send(ctx, LoginReject{Code: LoginInternalError, Message: "服务端无法建立会话"})
		pending.finish()
		pending.close()
		return err
	}
	if err := pending.send(ctx, LoginSuccess{PlayerID: pending.identity.PlayerID}); err != nil {
		endpoint.abort()
		pending.finish()
		pending.close()
		return err
	}
	endpoint.commit()
	if err := pending.finish(); err != nil {
		return err
	}
	return nil
}

func (pending *PendingLogin) Reject(ctx context.Context, code LoginRejectCode, message string) error {
	if err := pending.decide(); err != nil {
		return err
	}
	err := pending.send(ctx, LoginReject{Code: code, Message: message})
	phaseErr := pending.finish()
	pending.close()
	if err != nil {
		return err
	}
	if phaseErr != nil {
		return phaseErr
	}
	return nil
}

func (pending *PendingLogin) decide() error {
	if pending == nil {
		return errors.New("network: nil pending login")
	}
	if err := pending.login.Err(); err != nil {
		pending.finish()
		pending.close()
		return err
	}
	if !pending.decided.CompareAndSwap(false, true) {
		return errors.New("network: pending login already decided")
	}
	return nil
}

func (pending *PendingLogin) send(ctx context.Context, packet ServerPacket) error {
	if err := pending.login.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	op, cleanup := pending.operationContext(ctx)
	err := pending.stream.Send(op, StateLogin, packet)
	cleanup()
	if phaseErr := pending.login.Err(); phaseErr != nil {
		return phaseErr
	}
	if callerErr := ctx.Err(); callerErr != nil {
		return callerErr
	}
	return err
}

func (pending *PendingLogin) operationContext(ctx context.Context) (context.Context, func()) {
	op, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(pending.login, cancel)
	return op, func() {
		stop()
		cancel()
	}
}

func (pending *PendingLogin) expire() {
	pending.phaseMu.Lock()
	if pending.handedOff {
		pending.phaseMu.Unlock()
		close(pending.phaseDone)
		return
	}
	pending.phaseErr = pending.login.Err()
	pending.phaseMu.Unlock()
	pending.close()
	close(pending.phaseDone)
}

func (pending *PendingLogin) finish() error {
	pending.phaseMu.Lock()
	if pending.handedOff {
		pending.phaseMu.Unlock()
		return nil
	}
	if pending.phaseErr != nil {
		err := pending.phaseErr
		done := pending.phaseDone
		pending.phaseMu.Unlock()
		<-done
		return err
	}
	pending.handedOff = true
	pending.phaseMu.Unlock()
	if pending.stop() {
		pending.cancel()
		return nil
	}
	done := pending.phaseDone
	<-done
	pending.phaseMu.Lock()
	err := pending.phaseErr
	pending.phaseMu.Unlock()
	return err
}

func (pending *PendingLogin) close() {
	if pending != nil {
		_ = pending.stream.Close()
	}
}

func LoginClient(ctx context.Context, stream ClientPacketStream, identity Identity) (_ ClientEndpoint, err error) {
	if stream == nil {
		return nil, errors.New("network: nil client packet stream")
	}
	defer func() {
		if err != nil {
			_ = stream.Close()
		}
	}()

	handshake, cancelHandshake := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancelHandshake()
	if err := stream.Send(handshake, StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}); err != nil {
		return nil, err
	}
	packet, err := stream.Recv(handshake, StateHandshake)
	if err != nil {
		return nil, loginReceiveError(err)
	}
	switch hello := packet.(type) {
	case ServerHello:
		if hello.ProtocolVersion != ProtocolVersion {
			return nil, protocolViolation(errors.New("server handshake version mismatch"))
		}
	case HandshakeReject:
		return nil, &RemoteError{State: StateHandshake, Code: uint8(hello.Code), Message: hello.Message}
	default:
		return nil, protocolViolation(errors.New("unexpected server handshake packet"))
	}

	login, cancelLogin := context.WithTimeout(ctx, LoginTimeout)
	defer cancelLogin()
	if err := stream.Send(login, StateLogin, LoginStart{PlayerID: identity.PlayerID, DisplayName: identity.DisplayName}); err != nil {
		return nil, err
	}
	packet, err = stream.Recv(login, StateLogin)
	if err != nil {
		return nil, loginReceiveError(err)
	}
	switch result := packet.(type) {
	case LoginSuccess:
		if result.PlayerID != identity.PlayerID {
			return nil, protocolViolation(errors.New("login success player ID does not match request"))
		}
	case LoginReject:
		return nil, &RemoteError{State: StateLogin, Code: uint8(result.Code), Message: result.Message}
	default:
		return nil, protocolViolation(errors.New("unexpected server login packet"))
	}
	return newClientPlayEndpoint(stream), nil
}

func protocolViolation(cause error) error {
	return fmt.Errorf("network: protocol violation: %w", cause)
}

func loginReceiveError(err error) error {
	if errors.Is(err, ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return protocolViolation(err)
}

type gatedServerPlayEndpoint struct {
	stream     ServerPacketStream
	login      context.Context
	open       chan struct{}
	aborted    chan struct{}
	commitOnce sync.Once
	abortOnce  sync.Once
}

func newGatedServerPlayEndpoint(stream ServerPacketStream, login context.Context) *gatedServerPlayEndpoint {
	return &gatedServerPlayEndpoint{
		stream:  stream,
		login:   login,
		open:    make(chan struct{}),
		aborted: make(chan struct{}),
	}
}

func (endpoint *gatedServerPlayEndpoint) Send(ctx context.Context, message ServerMessage) error {
	if err := endpoint.wait(ctx); err != nil {
		return err
	}
	return newServerPlayEndpoint(endpoint.stream).Send(ctx, message)
}

func (endpoint *gatedServerPlayEndpoint) Recv(ctx context.Context) (ClientMessage, error) {
	if err := endpoint.wait(ctx); err != nil {
		return nil, err
	}
	return newServerPlayEndpoint(endpoint.stream).Recv(ctx)
}

func (endpoint *gatedServerPlayEndpoint) Close() error {
	return endpoint.stream.Close()
}

func (endpoint *gatedServerPlayEndpoint) wait(ctx context.Context) error {
	select {
	case <-endpoint.open:
		return nil
	default:
	}
	if err := endpoint.login.Err(); err != nil {
		return err
	}
	select {
	case <-endpoint.open:
		return nil
	case <-endpoint.aborted:
		return ErrClosed
	case <-endpoint.login.Done():
		select {
		case <-endpoint.open:
			return nil
		default:
		}
		return endpoint.login.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (endpoint *gatedServerPlayEndpoint) commit() {
	endpoint.commitOnce.Do(func() { close(endpoint.open) })
}

func (endpoint *gatedServerPlayEndpoint) abort() {
	endpoint.abortOnce.Do(func() { close(endpoint.aborted) })
}

var _ ClientEndpoint = (*clientPlayEndpoint)(nil)
var _ ServerEndpoint = (*serverPlayEndpoint)(nil)
var _ ServerEndpoint = (*gatedServerPlayEndpoint)(nil)
