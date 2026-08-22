package network

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
)

func TestMemoryLoginTransitionsToPlay(t *testing.T) {
	clientStream, serverStream := NewMemoryStreamPair(16)
	id := core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, 15}
	serverDone := make(chan error, 1)
	go func() {
		pending, err := BeginServerLogin(context.Background(), serverStream, 0)
		if err != nil {
			serverDone <- err
			return
		}
		if pending.Identity() != (Identity{PlayerID: id, DisplayName: "Chen"}) {
			serverDone <- fmt.Errorf("identity=%+v", pending.Identity())
			return
		}
		var endpoint ServerEndpoint
		err = pending.Accept(context.Background(), func(attached ServerEndpoint) error {
			endpoint = attached
			return nil
		})
		if err == nil {
			err = endpoint.Send(context.Background(), PlayerState{Ready: false})
		}
		serverDone <- err
	}()
	client, err := LoginClient(context.Background(), clientStream, Identity{PlayerID: id, DisplayName: "Chen"})
	if err != nil {
		t.Fatal(err)
	}
	if message, err := client.Recv(context.Background()); err != nil || message.(PlayerState).Ready {
		t.Fatalf("message=%+v err=%v", message, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestBeginServerLoginCanonicalizesDisplayName(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	identity := testIdentity(21)
	identity.DisplayName = "  Chen  "

	pending := beginMemoryLogin(t, client, server, identity)
	if got := pending.Identity().DisplayName; got != "Chen" {
		t.Fatalf("pending display name = %q, want canonical %q", got, "Chen")
	}
	if err := pending.Reject(context.Background(), LoginServerFull, "done"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
}

func TestLoginClientReportsHandshakeVersionMismatch(t *testing.T) {
	client, server := NewMemoryStreamPair(4)
	t.Cleanup(func() { _ = client.Close() })

	serverDone := make(chan error, 1)
	go func() {
		if _, err := server.Recv(context.Background(), StateHandshake); err != nil {
			serverDone <- err
			return
		}
		serverDone <- server.Send(context.Background(), StateHandshake, HandshakeReject{
			ServerProtocolVersion: 2,
			Code:                  HandshakeVersionMismatch,
			Message:               "upgrade required",
		})
	}()

	_, err := LoginClient(context.Background(), client, testIdentity(1))
	assertRemoteError(t, err, StateHandshake, uint8(HandshakeVersionMismatch), "upgrade required")
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestBeginServerLoginRejectsOutdatedClientHelloWithProtocolV8(t *testing.T) {
	for _, version := range []uint32{1, 2, 3, 4, 5, 6, 7} {
		stream := &staticClientHelloStream{version: version}
		if _, err := BeginServerLogin(context.Background(), stream, 0); err == nil {
			t.Fatalf("v%d ClientHello accepted", version)
		}
		reject, ok := stream.sent.(HandshakeReject)
		if !ok || stream.sentState != StateHandshake ||
			reject.ServerProtocolVersion != ProtocolVersion ||
			reject.Code != HandshakeVersionMismatch {
			t.Fatalf("v%d rejection = %#v in state %d, want v8 HandshakeReject",
				version, stream.sent, stream.sentState)
		}
	}
}

func TestBeginServerLoginFutureClientHelloReturnsV2MismatchOverTCP(t *testing.T) {
	listener := listenLoopback(t)
	t.Cleanup(func() { _ = listener.Close() })
	client, server := dialAndAccept(t, listener)
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	serverDone := make(chan error, 1)
	go func() {
		_, err := BeginServerLogin(ctx, server, 0)
		serverDone <- err
	}()

	writeRawClientFrame(t, client, 0, []byte{byte(ProtocolVersion + 1)})
	packet, err := client.Recv(ctx, StateHandshake)
	if err != nil {
		t.Fatalf("future ClientHello response: %v", err)
	}
	reject, ok := packet.(HandshakeReject)
	if err != nil || !ok || reject.ServerProtocolVersion != ProtocolVersion || reject.Code != HandshakeVersionMismatch {
		t.Fatalf("future ClientHello rejection=(%#v, %v), want protocol %d HandshakeVersionMismatch", packet, err, ProtocolVersion)
	}
	if err := <-serverDone; err == nil || !strings.Contains(err.Error(), "protocol violation") {
		t.Fatalf("BeginServerLogin error=%v, want protocol violation after rejection", err)
	}
}

func TestLoginClientReportsStableLoginRejectCodes(t *testing.T) {
	for _, reject := range []LoginReject{
		{Code: LoginServerFull, Message: "server full"},
		{Code: LoginInvalidIdentity, Message: "identity rejected"},
	} {
		t.Run(fmt.Sprintf("code-%d", reject.Code), func(t *testing.T) {
			client, server := NewMemoryStreamPair(4)
			t.Cleanup(func() { _ = client.Close() })
			serverDone := make(chan error, 1)
			go func() {
				if _, err := server.Recv(context.Background(), StateHandshake); err != nil {
					serverDone <- err
					return
				}
				if err := server.Send(context.Background(), StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}); err != nil {
					serverDone <- err
					return
				}
				if _, err := server.Recv(context.Background(), StateLogin); err != nil {
					serverDone <- err
					return
				}
				serverDone <- server.Send(context.Background(), StateLogin, reject)
			}()

			_, err := LoginClient(context.Background(), client, testIdentity(2))
			assertRemoteError(t, err, StateLogin, uint8(reject.Code), reject.Message)
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPendingLoginCanOnlyBeDecidedOnce(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })

	pending := beginMemoryLogin(t, client, server, testIdentity(3))
	if err := pending.Reject(context.Background(), LoginServerFull, "server full"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if err := pending.Accept(context.Background(), func(ServerEndpoint) error { return nil }); err == nil {
		t.Fatal("Accept after Reject succeeded")
	}
	if packet, err := client.Recv(context.Background(), StateLogin); err != nil || packet != (LoginReject{Code: LoginServerFull, Message: "server full"}) {
		t.Fatalf("login reject = (%+v, %v)", packet, err)
	}
}

func TestPendingLoginRejectClosesUnownedStream(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	pending := beginMemoryLogin(t, client, server, testIdentity(14))

	if err := pending.Reject(context.Background(), LoginServerFull, "server full"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if packet, err := client.Recv(context.Background(), StateLogin); err != nil || packet != (LoginReject{Code: LoginServerFull, Message: "server full"}) {
		t.Fatalf("login reject = (%+v, %v)", packet, err)
	}
	if err := client.Send(context.Background(), StatePlay, PlayerInput{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("stream after successful Reject = %v, want ErrClosed", err)
	}
}

func TestPendingLoginParentCancellationAfterBeginClosesStream(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := beginMemoryLoginWithContext(t, parent, client, server, testIdentity(15))

	cancel()
	if err := pending.Accept(context.Background(), func(ServerEndpoint) error {
		t.Fatal("Accept callback ran after parent cancellation")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Accept after parent cancellation = %v, want context.Canceled", err)
	}
	closed, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer stop()
	if packet, err := client.Recv(closed, StateLogin); packet != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("stream after parent cancellation = (%+v, %v), want (nil, ErrClosed)", packet, err)
	}
}

func TestPendingLoginCancellationDuringAcceptClosesStream(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := beginMemoryLoginWithContext(t, parent, client, server, testIdentity(16))

	entered := make(chan struct{})
	release := make(chan struct{})
	accepted := make(chan error, 1)
	acceptReturned := false
	go func() {
		accepted <- pending.Accept(context.Background(), func(ServerEndpoint) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Accept did not enter attach callback")
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		if !acceptReturned {
			select {
			case <-accepted:
			case <-time.After(250 * time.Millisecond):
				t.Error("Accept did not return after attach release")
			}
		}
	})

	cancel()
	closed, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer stop()
	if packet, err := client.Recv(closed, StateLogin); packet != nil || !errors.Is(err, ErrClosed) {
		t.Fatalf("stream after cancellation racing Accept = (%+v, %v), want (nil, ErrClosed)", packet, err)
	}
	close(release)
	if err := <-accepted; !errors.Is(err, context.Canceled) {
		t.Fatalf("Accept after cancellation = %v, want context.Canceled", err)
	}
	acceptReturned = true
}

func TestPendingLoginLoginPhaseDeadlineSurvivesBegin(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	before := time.Now()
	pending := beginMemoryLogin(t, client, server, testIdentity(17))

	deadline, ok := pending.login.Deadline()
	if !ok || deadline.Before(before.Add(LoginTimeout-100*time.Millisecond)) || deadline.After(time.Now().Add(LoginTimeout+100*time.Millisecond)) {
		t.Fatalf("pending Login deadline = (%v, %t), want approximately %s from Begin", deadline, ok, LoginTimeout)
	}
	if err := pending.Reject(context.Background(), LoginServerFull, "server full"); err != nil {
		t.Fatalf("Reject: %v", err)
	}
}

func TestPendingLoginDecisionSendHonorsCallerContext(t *testing.T) {
	for _, test := range []struct {
		name   string
		decide func(*PendingLogin, context.Context) error
	}{
		{
			name: "accept",
			decide: func(pending *PendingLogin, ctx context.Context) error {
				return pending.Accept(ctx, func(ServerEndpoint) error { return nil })
			},
		},
		{
			name: "reject",
			decide: func(pending *PendingLogin, ctx context.Context) error {
				return pending.Reject(ctx, LoginServerFull, "server full")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server := NewMemoryStreamPair(8)
			t.Cleanup(func() { _ = client.Close() })
			pending := beginMemoryLogin(t, client, server, testIdentity(18))
			caller, cancel := context.WithCancel(context.Background())
			cancel()

			if err := test.decide(pending, caller); !errors.Is(err, context.Canceled) {
				t.Fatalf("decision with canceled caller = %v, want context.Canceled", err)
			}
			closed, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer stop()
			if packet, err := client.Recv(closed, StateLogin); packet != nil || !errors.Is(err, ErrClosed) {
				t.Fatalf("stream after canceled caller = (%+v, %v), want (nil, ErrClosed)", packet, err)
			}
		})
	}
}

func TestPendingLoginSuccessfulAcceptStopsLoginWatcher(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := beginMemoryLoginWithContext(t, parent, client, server, testIdentity(19))

	var endpoint ServerEndpoint
	if err := pending.Accept(context.Background(), func(attached ServerEndpoint) error {
		endpoint = attached
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if packet, err := client.Recv(context.Background(), StateLogin); err != nil || packet != (LoginSuccess{PlayerID: testIdentity(19).PlayerID}) {
		t.Fatalf("login success = (%+v, %v)", packet, err)
	}
	cancel()
	if err := endpoint.Send(context.Background(), PlayerState{Ready: true}); err != nil {
		t.Fatalf("Play Send after parent cancellation = %v", err)
	}
	if packet, err := client.Recv(context.Background(), StatePlay); err != nil || packet != (PlayerState{Ready: true}) {
		t.Fatalf("play packet after parent cancellation = (%+v, %v)", packet, err)
	}
}

func TestGatedServerPlayEndpointCommittedHandoffWinsLoginCancellation(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)

	for iteration := 0; iteration < 128; iteration++ {
		client, server := NewMemoryStreamPair(1)
		login := &simultaneousHandoffContext{
			done:     make(chan struct{}),
			observed: make(chan struct{}),
			release:  make(chan struct{}),
		}
		endpoint := newGatedServerPlayEndpoint(server, login)
		result := make(chan error, 1)
		go func() { result <- endpoint.wait(context.Background()) }()

		<-login.observed
		endpoint.commit()
		close(login.done)
		close(login.release)
		runtime.Gosched()
		if err := <-result; err != nil {
			_ = client.Close()
			t.Fatalf("committed handoff iteration %d selected canceled login: %v", iteration, err)
		}
		if err := client.Close(); err != nil {
			t.Fatalf("close handoff stream iteration %d: %v", iteration, err)
		}
	}
}

func TestGatedServerPlayEndpointCommittedHandoffWinsEarlyLoginCancellation(t *testing.T) {
	loginContext, cancel := context.WithCancel(context.Background())
	login := &handoffDuringErrContext{
		Context:  loginContext,
		observed: make(chan struct{}),
	}
	endpoint := newGatedServerPlayEndpoint(nil, login)
	result := make(chan error, 1)
	go func() { result <- endpoint.wait(context.Background()) }()

	<-login.observed
	endpoint.commit()
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("committed handoff selected canceled login: %v", err)
	}
}

func TestPendingLoginCancellationAtSuccessHandoffDoesNotReturnDeadEndpoint(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending := beginMemoryLoginWithContext(t, parent, client, server, testIdentity(20))

	originalStop := pending.stop
	pending.stop = func() bool {
		cancel()
		select {
		case <-pending.phaseDone:
		case <-time.After(250 * time.Millisecond):
			t.Fatal("Login phase watcher did not finish")
		}
		return originalStop()
	}
	var endpoint ServerEndpoint
	err := pending.Accept(context.Background(), func(attached ServerEndpoint) error {
		endpoint = attached
		return nil
	})
	if endpoint == nil {
		t.Fatal("Accept did not attach an endpoint before the handoff")
	}
	sendErr := endpoint.Send(context.Background(), PlayerState{Ready: true})
	switch {
	case err == nil:
		if sendErr != nil {
			t.Fatalf("successful Accept returned a dead endpoint: %v", sendErr)
		}
	case errors.Is(err, context.Canceled):
		if !errors.Is(sendErr, ErrClosed) {
			t.Fatalf("canceled Accept endpoint = %v, want ErrClosed", sendErr)
		}
	default:
		t.Fatalf("Accept after cancellation at success handoff = %v, want nil or context.Canceled", err)
	}
}

func TestPendingLoginAcceptGatesPlayUntilLoginSuccess(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	t.Cleanup(func() { _ = client.Close() })
	pending := beginMemoryLogin(t, client, server, testIdentity(8))

	var endpoint ServerEndpoint
	if err := pending.Accept(context.Background(), func(attached ServerEndpoint) error {
		endpoint = attached
		preCommit, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer stop()
		if err := endpoint.Send(preCommit, PlayerState{Ready: true}); !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("pre-commit Play Send = %v, want context.DeadlineExceeded", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if packet, err := client.Recv(context.Background(), StateLogin); err != nil || packet != (LoginSuccess{PlayerID: testIdentity(8).PlayerID}) {
		t.Fatalf("login success = (%+v, %v)", packet, err)
	}
	if err := endpoint.Send(context.Background(), PlayerState{Ready: true}); err != nil {
		t.Fatalf("post-commit Play Send = %v", err)
	}
	if packet, err := client.Recv(context.Background(), StatePlay); err != nil || packet != (PlayerState{Ready: true}) {
		t.Fatalf("play packet = (%+v, %v)", packet, err)
	}
}

func TestPendingLoginAttachFailureRejectsAndCloses(t *testing.T) {
	client, server := NewMemoryStreamPair(8)
	pending := beginMemoryLogin(t, client, server, testIdentity(9))
	want := errors.New("attach failed")
	if err := pending.Accept(context.Background(), func(ServerEndpoint) error { return want }); !errors.Is(err, want) {
		t.Fatalf("Accept error = %v, want attach error", err)
	}
	packet, err := client.Recv(context.Background(), StateLogin)
	if err != nil || packet != (LoginReject{Code: LoginInternalError, Message: "服务端无法建立会话"}) {
		t.Fatalf("attach rejection = (%+v, %v)", packet, err)
	}
	if err := client.Send(context.Background(), StatePlay, PlayerInput{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("stream after attach failure = %v, want ErrClosed", err)
	}
}

func TestLoginClientClosesOnMismatchedSuccessIdentity(t *testing.T) {
	client, server := NewMemoryStreamPair(4)
	serverDone := make(chan error, 1)
	go func() {
		if _, err := server.Recv(context.Background(), StateHandshake); err != nil {
			serverDone <- err
			return
		}
		if err := server.Send(context.Background(), StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}); err != nil {
			serverDone <- err
			return
		}
		if _, err := server.Recv(context.Background(), StateLogin); err != nil {
			serverDone <- err
			return
		}
		serverDone <- server.Send(context.Background(), StateLogin, LoginSuccess{PlayerID: testIdentity(5).PlayerID})
	}()

	_, err := LoginClient(context.Background(), client, testIdentity(4))
	if err == nil || !strings.Contains(err.Error(), "player ID") {
		t.Fatalf("LoginClient mismatch error = %v", err)
	}
	if err := client.Send(context.Background(), StatePlay, PlayerInput{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("stream after mismatched success = %v, want ErrClosed", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestLoginClientRejectsEarlyPlayPacket(t *testing.T) {
	client, server := NewMemoryStreamPair(4)
	serverDone := make(chan error, 1)
	go func() {
		if _, err := server.Recv(context.Background(), StateHandshake); err != nil {
			serverDone <- err
			return
		}
		serverDone <- server.Send(context.Background(), StatePlay, PlayerState{})
	}()

	_, err := LoginClient(context.Background(), client, testIdentity(6))
	if err == nil || !strings.Contains(err.Error(), "protocol violation") {
		t.Fatalf("early play error = %v", err)
	}
	if err := client.Send(context.Background(), StatePlay, PlayerInput{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("stream after early play = %v, want ErrClosed", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestLoginClientKeepsPlayControlPacketsOutOfMirror(t *testing.T) {
	clientStream, serverStream := NewMemoryStreamPair(8)
	serverDone := make(chan error, 1)
	go func() {
		pending, err := BeginServerLogin(context.Background(), serverStream, 0)
		if err != nil {
			serverDone <- err
			return
		}
		var endpoint ServerEndpoint
		if err := pending.Accept(context.Background(), func(attached ServerEndpoint) error {
			endpoint = attached
			return nil
		}); err != nil {
			serverDone <- err
			return
		}
		if err := endpoint.Send(context.Background(), KeepAlive{Token: 77}); err != nil {
			serverDone <- err
			return
		}
		if err := endpoint.Send(context.Background(), PlayerState{Ready: true}); err != nil {
			serverDone <- err
			return
		}
		packet, err := endpoint.Recv(context.Background())
		if err != nil {
			serverDone <- err
			return
		}
		if packet != (KeepAliveReply{Token: 77}) {
			serverDone <- fmt.Errorf("keep alive reply=%+v", packet)
			return
		}
		serverDone <- endpoint.Send(context.Background(), Disconnect{Code: DisconnectTimeout, Message: "idle"})
	}()

	endpoint, err := LoginClient(context.Background(), clientStream, testIdentity(7))
	if err != nil {
		t.Fatal(err)
	}
	packet, err := endpoint.Recv(context.Background())
	if err != nil || packet != (PlayerState{Ready: true}) {
		t.Fatalf("application packet = (%+v, %v)", packet, err)
	}
	_, err = endpoint.Recv(context.Background())
	assertRemoteError(t, err, StatePlay, uint8(DisconnectTimeout), "idle")
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func beginMemoryLogin(t *testing.T, client ClientPacketStream, server ServerPacketStream, identity Identity) *PendingLogin {
	return beginMemoryLoginWithContext(t, context.Background(), client, server, identity)
}

func beginMemoryLoginWithContext(t *testing.T, ctx context.Context, client ClientPacketStream, server ServerPacketStream, identity Identity) *PendingLogin {
	t.Helper()
	if err := client.Send(context.Background(), StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	pendingDone := make(chan struct {
		pending *PendingLogin
		err     error
	}, 1)
	go func() {
		pending, err := BeginServerLogin(ctx, server, 0)
		pendingDone <- struct {
			pending *PendingLogin
			err     error
		}{pending, err}
	}()
	if packet, err := client.Recv(context.Background(), StateHandshake); err != nil || packet != (ServerHello{ProtocolVersion: ProtocolVersion}) {
		t.Fatalf("server hello = (%+v, %v)", packet, err)
	}
	if err := client.Send(context.Background(), StateLogin, LoginStart{PlayerID: identity.PlayerID, DisplayName: identity.DisplayName}); err != nil {
		t.Fatal(err)
	}
	result := <-pendingDone
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result.pending
}

func assertRemoteError(t *testing.T, err error, state State, code uint8, message string) {
	t.Helper()
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.State != state || remote.Code != code || remote.Message != message {
		t.Fatalf("remote error = %#v, want state=%d code=%d message=%q", err, state, code, message)
	}
}

type simultaneousHandoffContext struct {
	done     chan struct{}
	observed chan struct{}
	release  chan struct{}
}

func (*simultaneousHandoffContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (ctx *simultaneousHandoffContext) Done() <-chan struct{} {
	close(ctx.observed)
	<-ctx.release
	return ctx.done
}

func (ctx *simultaneousHandoffContext) Err() error {
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}

func (*simultaneousHandoffContext) Value(any) any { return nil }

type handoffDuringErrContext struct {
	context.Context
	observed chan struct{}
}

func (ctx *handoffDuringErrContext) Err() error {
	close(ctx.observed)
	<-ctx.Done()
	return ctx.Context.Err()
}

func testIdentity(last byte) Identity {
	return Identity{
		PlayerID:    core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, last},
		DisplayName: "Chen",
	}
}

type staticClientHelloStream struct {
	version   uint32
	sent      ServerPacket
	sentState State
}

func (stream *staticClientHelloStream) Send(_ context.Context, state State, packet ServerPacket) error {
	stream.sentState = state
	stream.sent = packet
	return nil
}

func (stream *staticClientHelloStream) Recv(context.Context, State) (ClientPacket, error) {
	return ClientHello{ProtocolVersion: stream.version}, nil
}

func (*staticClientHelloStream) Peer() string { return "test" }
func (*staticClientHelloStream) Close() error { return nil }
