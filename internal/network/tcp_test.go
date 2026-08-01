package network

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
)

func TestTCPStreamRoundTripAndPeer(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()

	client, server := dialAndAccept(t, listener)
	defer client.Close()
	defer server.Close()

	wantClient := ClientHello{ProtocolVersion: ProtocolVersion}
	if err := client.Send(context.Background(), StateHandshake, wantClient); err != nil {
		t.Fatalf("client Send: %v", err)
	}
	gotClient, err := server.Recv(context.Background(), StateHandshake)
	if err != nil || gotClient != wantClient {
		t.Fatalf("server Recv = (%+v, %v), want (%+v, nil)", gotClient, err, wantClient)
	}
	if server.Peer() == "" {
		t.Fatal("Peer returned an empty address")
	}

	wantServer := ServerHello{ProtocolVersion: ProtocolVersion}
	if err := server.Send(context.Background(), StateHandshake, wantServer); err != nil {
		t.Fatalf("server Send: %v", err)
	}
	gotServer, err := client.Recv(context.Background(), StateHandshake)
	if err != nil || gotServer != wantServer {
		t.Fatalf("client Recv = (%+v, %v), want (%+v, nil)", gotServer, err, wantServer)
	}
}

func TestTCPDialCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stream, err := DialTCP(ctx, "127.0.0.1:1")
	if stream != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("DialTCP = (%v, %v), want (nil, context.Canceled)", stream, err)
	}
}

func TestTCPAcceptCanceledKeepsListenerReusable(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stream, err := listener.Accept(ctx)
	if stream != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Accept = (%v, %v), want (nil, context.DeadlineExceeded)", stream, err)
	}

	client, server := dialAndAccept(t, listener)
	defer client.Close()
	defer server.Close()
	if err := client.Send(context.Background(), StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}); err != nil {
		t.Fatalf("Send after canceled Accept: %v", err)
	}
	if _, err := server.Recv(context.Background(), StateHandshake); err != nil {
		t.Fatalf("Recv after canceled Accept: %v", err)
	}
}

func TestTCPRecvDeadlineDoesNotPoisonNextRecv(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	client, server := dialAndAccept(t, listener)
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if packet, err := server.Recv(ctx, StateHandshake); packet != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed Recv = (%v, %v), want (nil, context.DeadlineExceeded)", packet, err)
	}

	want := ClientHello{ProtocolVersion: ProtocolVersion}
	if err := client.Send(context.Background(), StateHandshake, want); err != nil {
		t.Fatalf("Send after Recv deadline: %v", err)
	}
	packet, err := server.Recv(context.Background(), StateHandshake)
	if err != nil || packet != want {
		t.Fatalf("Recv after deadline = (%+v, %v), want (%+v, nil)", packet, err, want)
	}
}

func TestTCPSendDeadlineAndSubsequentSend(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	client, server := dialAndAccept(t, listener)
	defer client.Close()
	defer server.Close()

	serverImpl := server.(*tcpServerStream)
	buffered, ok := serverImpl.stream.conn.(interface{ SetWriteBuffer(int) error })
	if !ok {
		t.Fatalf("accepted connection %T has no SetWriteBuffer", serverImpl.stream.conn)
	}
	if err := buffered.SetWriteBuffer(1024); err != nil {
		t.Fatalf("SetWriteBuffer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	packet := HandshakeReject{
		ServerProtocolVersion: ProtocolVersion,
		Code:                  HandshakeVersionMismatch,
		Message:               strings.Repeat("x", 256),
	}
	var sendErr error
	for sendErr == nil {
		sendErr = server.Send(ctx, StateHandshake, packet)
	}
	if !errors.Is(sendErr, context.DeadlineExceeded) {
		t.Fatalf("blocked Send error = %v, want context.DeadlineExceeded", sendErr)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("client Close: %v", err)
	}
	if err := server.Send(context.Background(), StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Send after peer close = %v, want ErrClosed", err)
	}
}

func TestTCPPeerCloseWakesBlockedRecv(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	client, server := dialAndAccept(t, listener)
	defer server.Close()

	result := make(chan error, 1)
	go func() {
		_, err := server.Recv(context.Background(), StateHandshake)
		result <- err
	}()
	time.Sleep(10 * time.Millisecond)
	if err := client.Close(); err != nil {
		t.Fatalf("client Close: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("blocked Recv = %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("peer close did not wake blocked Recv")
	}
}

func TestTCPCancelWakesStartedRecvAndDoesNotPoisonNextRecv(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	client, server := dialAndAccept(t, listener)
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := server.Recv(ctx, StateHandshake)
		result <- err
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled blocked Recv = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not wake blocked Recv")
	}

	want := ClientHello{ProtocolVersion: ProtocolVersion}
	if err := client.Send(context.Background(), StateHandshake, want); err != nil {
		t.Fatalf("Send after canceled Recv: %v", err)
	}
	got, err := server.Recv(context.Background(), StateHandshake)
	if err != nil || got != want {
		t.Fatalf("Recv after cancellation = (%+v, %v), want (%+v, nil)", got, err, want)
	}
}

func TestTCPListenerCloseWakesAccept(t *testing.T) {
	listener := listenLoopback(t)
	result := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := listener.Accept(context.Background())
		result <- err
	}()
	<-started
	time.Sleep(10 * time.Millisecond)
	if err := listener.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("Accept after listener Close = %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("listener Close did not wake blocked Accept")
	}
}

func TestTCPClosedStreamReturnsErrClosedBeforeSnapshotCodec(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	client, server := dialAndAccept(t, listener)
	defer client.Close()

	if err := server.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	snapshot := fixtureSnapshot(core.ChunkPos{X: 1, Z: 2}, 3)
	if err := server.Send(context.Background(), StatePlay, snapshot); !errors.Is(err, ErrClosed) {
		t.Fatalf("snapshot Send after Close = %v, want ErrClosed", err)
	}
}

func TestTCPPreCanceledOperationsAndDeadlineCleanup(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	client, server := dialAndAccept(t, listener)
	defer client.Close()
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Send(ctx, StateHandshake, ClientHello{ProtocolVersion: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Send = %v, want context.Canceled", err)
	}
	if packet, err := server.Recv(ctx, StateHandshake); packet != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Recv = (%v, %v), want (nil, context.Canceled)", packet, err)
	}

	want := ClientHello{ProtocolVersion: ProtocolVersion}
	if err := client.Send(context.Background(), StateHandshake, want); err != nil {
		t.Fatalf("Send after cancellation: %v", err)
	}
	got, err := server.Recv(context.Background(), StateHandshake)
	if err != nil || got != want {
		t.Fatalf("Recv after cancellation = (%+v, %v), want (%+v, nil)", got, err, want)
	}
}

func TestTCPRecvOwnerWaitHonorsContext(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		want    error
	}{
		{
			name: "deadline",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 20*time.Millisecond)
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "cancel",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			want: context.Canceled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			listener := listenLoopback(t)
			defer listener.Close()
			client, server := dialAndAccept(t, listener)
			serverImpl := server.(*tcpServerStream)
			enteredRead := make(chan struct{})
			serverImpl.stream.conn = &readStartedConn{
				streamConn: serverImpl.stream.conn,
				started:    enteredRead,
			}

			firstResult := make(chan error, 1)
			go func() {
				_, err := server.Recv(context.Background(), StateHandshake)
				firstResult <- err
			}()
			<-enteredRead

			ctx, cancel := test.context()
			secondStarted := make(chan struct{})
			secondResult := make(chan error, 1)
			go func() {
				close(secondStarted)
				_, err := server.Recv(ctx, StateHandshake)
				secondResult <- err
			}()
			<-secondStarted
			if errors.Is(test.want, context.Canceled) {
				cancel()
			} else {
				defer cancel()
			}

			select {
			case err := <-secondResult:
				if !errors.Is(err, test.want) {
					t.Fatalf("second Recv = %v, want %v", err, test.want)
				}
			case <-time.After(250 * time.Millisecond):
				_ = client.Close()
				<-firstResult
				<-secondResult
				t.Fatalf("second Recv did not return %v while read owner was held", test.want)
			}

			if err := client.Close(); err != nil {
				t.Fatalf("client Close: %v", err)
			}
			if err := <-firstResult; !errors.Is(err, ErrClosed) {
				t.Fatalf("first Recv after peer Close = %v, want ErrClosed", err)
			}
			if err := server.Close(); err != nil {
				t.Fatalf("server Close: %v", err)
			}
		})
	}
}

func TestTCPSendOwnerWaitHonorsContext(t *testing.T) {
	tests := []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		want    error
	}{
		{
			name: "deadline",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 20*time.Millisecond)
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "cancel",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			want: context.Canceled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn := newBlockingWriteConn()
			codec := mustNewCodec(t)
			client := &tcpClientStream{stream: &tcpStream{conn: conn, codec: codec}}

			firstResult := make(chan error, 1)
			go func() {
				firstResult <- client.Send(
					context.Background(), StateHandshake,
					ClientHello{ProtocolVersion: ProtocolVersion},
				)
			}()
			<-conn.started

			ctx, cancel := test.context()
			secondStarted := make(chan struct{})
			secondResult := make(chan error, 1)
			go func() {
				close(secondStarted)
				secondResult <- client.Send(
					ctx, StateHandshake,
					ClientHello{ProtocolVersion: ProtocolVersion},
				)
			}()
			<-secondStarted
			if errors.Is(test.want, context.Canceled) {
				cancel()
			} else {
				defer cancel()
			}

			select {
			case err := <-secondResult:
				if !errors.Is(err, test.want) {
					t.Fatalf("second Send = %v, want %v", err, test.want)
				}
			case <-time.After(250 * time.Millisecond):
				conn.unblock()
				<-firstResult
				<-secondResult
				t.Fatalf("second Send did not return %v while write owner was held", test.want)
			}

			conn.unblock()
			if err := <-firstResult; err != nil {
				t.Fatalf("first Send after unblock: %v", err)
			}
			if err := client.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
		})
	}
}

func TestTCPAcceptOwnerWaitHonorsDeadline(t *testing.T) {
	listener := listenLoopback(t)
	listenerImpl := listener.(*tcpListener)
	if err := listenerImpl.accept.acquire(context.Background()); err != nil {
		t.Fatalf("acquire test setup owner: %v", err)
	}
	firstResult := make(chan error, 1)
	go func() {
		_, err := listener.Accept(context.Background())
		firstResult <- err
	}()
	listenerImpl.accept.release()
	ownerDeadline := time.Now().Add(time.Second)
	for len(listenerImpl.accept.token) != 0 {
		if time.Now().After(ownerDeadline) {
			_ = listener.Close()
			t.Fatal("first Accept did not acquire accept owner")
		}
		runtime.Gosched()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	secondResult := make(chan error, 1)
	go func() {
		_, err := listener.Accept(ctx)
		secondResult <- err
	}()
	select {
	case err := <-secondResult:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("second Accept = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(250 * time.Millisecond):
		_ = listener.Close()
		<-firstResult
		<-secondResult
		t.Fatal("second Accept did not honor its deadline while accept owner was held")
	}

	if err := listener.Close(); err != nil {
		t.Fatalf("listener Close: %v", err)
	}
	if err := <-firstResult; !errors.Is(err, ErrClosed) {
		t.Fatalf("first Accept after Close = %v, want ErrClosed", err)
	}
}

func TestTCPPreCanceledContextDoesNotEnterAvailableOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn := newBlockingWriteConn()
	codec := mustNewCodec(t)
	client := &tcpClientStream{stream: &tcpStream{conn: conn, codec: codec}}
	if err := client.Send(ctx, StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Send = %v, want context.Canceled", err)
	}
	select {
	case <-conn.started:
		t.Fatal("pre-canceled Send entered Write")
	default:
	}
	if _, err := client.Recv(ctx, StateHandshake); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Recv = %v, want context.Canceled", err)
	}
	select {
	case <-conn.readStarted:
		t.Fatal("pre-canceled Recv entered Read")
	default:
	}
	conn.unblock()
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestConcurrentTCPClose(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		listener := listenLoopback(t)
		client, server := dialAndAccept(t, listener)
		blocked := make(chan error, 1)
		go func() {
			_, err := server.Recv(context.Background(), StateHandshake)
			blocked <- err
		}()

		var closeGroup sync.WaitGroup
		for _, closeFn := range []func() error{
			client.Close,
			server.Close,
			server.(*tcpServerStream).stream.conn.Close,
		} {
			closeGroup.Add(1)
			go func(closeFn func() error) {
				defer closeGroup.Done()
				for call := 0; call < 3; call++ {
					_ = closeFn()
				}
			}(closeFn)
		}
		closeGroup.Wait()
		select {
		case err := <-blocked:
			if !errors.Is(err, ErrClosed) {
				t.Fatalf("iteration %d: blocked Recv = %v, want ErrClosed", iteration, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("iteration %d: concurrent Close did not wake Recv", iteration)
		}
		if err := listener.Close(); err != nil {
			t.Fatalf("iteration %d: listener Close: %v", iteration, err)
		}
	}
}

func TestTCPBadFrameClosesOnlyConnection(t *testing.T) {
	listener := listenLoopback(t)
	defer listener.Close()
	client, server := dialAndAccept(t, listener)

	clientImpl := client.(*tcpClientStream)
	if _, err := clientImpl.stream.conn.Write([]byte{0}); err != nil {
		t.Fatalf("write malformed frame: %v", err)
	}
	if packet, err := server.Recv(context.Background(), StateHandshake); packet != nil || err == nil || errors.Is(err, ErrClosed) {
		t.Fatalf("bad-frame Recv = (%v, %v), want non-closed protocol error", packet, err)
	}
	if _, err := client.Recv(context.Background(), StateHandshake); !errors.Is(err, ErrClosed) {
		t.Fatalf("peer Recv after protocol error = %v, want ErrClosed", err)
	}

	client2, server2 := dialAndAccept(t, listener)
	defer client2.Close()
	defer server2.Close()
	want := ClientHello{ProtocolVersion: ProtocolVersion}
	if err := client2.Send(context.Background(), StateHandshake, want); err != nil {
		t.Fatalf("second connection Send: %v", err)
	}
	got, err := server2.Recv(context.Background(), StateHandshake)
	if err != nil || got != want {
		t.Fatalf("second connection Recv = (%+v, %v), want (%+v, nil)", got, err, want)
	}
}

func TestTCPSocketOptionFailuresCloseSocket(t *testing.T) {
	tests := []struct {
		name string
		fail string
		want string
	}{
		{name: "no delay", fail: "TCP_NODELAY", want: "TCP_NODELAY"},
		{name: "keepalive", fail: "keepalive", want: "keepalive"},
		{name: "keepalive period", fail: "keepalive period", want: "keepalive period"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			socket := &failingTCPSocket{fail: test.fail}
			err := configureTCPSocket(socket)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("configureTCPSocket error = %v, want context %q", err, test.want)
			}
			if socket.closeCalls != 1 {
				t.Fatalf("Close calls = %d, want 1", socket.closeCalls)
			}
		})
	}
}

type failingTCPSocket struct {
	fail       string
	closeCalls int
}

type readStartedConn struct {
	streamConn
	started chan struct{}
	once    sync.Once
}

func (conn *readStartedConn) Read(data []byte) (int, error) {
	conn.once.Do(func() { close(conn.started) })
	return conn.streamConn.Read(data)
}

type blockingWriteConn struct {
	started     chan struct{}
	readStarted chan struct{}
	release     chan struct{}
	writeOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingWriteConn() *blockingWriteConn {
	return &blockingWriteConn{
		started:     make(chan struct{}),
		readStarted: make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (conn *blockingWriteConn) Read([]byte) (int, error) {
	select {
	case <-conn.readStarted:
	default:
		close(conn.readStarted)
	}
	return 0, io.EOF
}

func (conn *blockingWriteConn) Write(data []byte) (int, error) {
	conn.writeOnce.Do(func() {
		close(conn.started)
		<-conn.release
	})
	return len(data), nil
}

func (conn *blockingWriteConn) Close() error {
	conn.unblock()
	return nil
}

func (conn *blockingWriteConn) SetReadDeadline(time.Time) error  { return nil }
func (conn *blockingWriteConn) SetWriteDeadline(time.Time) error { return nil }

func (conn *blockingWriteConn) unblock() {
	conn.releaseOnce.Do(func() { close(conn.release) })
}

func (socket *failingTCPSocket) SetNoDelay(bool) error {
	return socket.optionError("TCP_NODELAY")
}

func (socket *failingTCPSocket) SetKeepAlive(bool) error {
	return socket.optionError("keepalive")
}

func (socket *failingTCPSocket) SetKeepAlivePeriod(time.Duration) error {
	return socket.optionError("keepalive period")
}

func (socket *failingTCPSocket) Close() error {
	socket.closeCalls++
	return nil
}

func (socket *failingTCPSocket) optionError(option string) error {
	if socket.fail == option {
		return errors.New("injected failure")
	}
	return nil
}

func listenLoopback(t *testing.T) Listener {
	t.Helper()
	listener, err := ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}
	if listener.Addr() == "" || strings.HasSuffix(listener.Addr(), ":0") {
		t.Fatalf("listener Addr = %q, want actual bound address", listener.Addr())
	}
	return listener
}

func dialAndAccept(t *testing.T, listener Listener) (ClientPacketStream, ServerPacketStream) {
	t.Helper()
	client, err := DialTCP(context.Background(), listener.Addr())
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	server, err := listener.Accept(context.Background())
	if err != nil {
		client.Close()
		t.Fatalf("Accept: %v", err)
	}
	return client, server
}

func writeRawClientFrame(t *testing.T, client ClientPacketStream, packetID uint32, payload []byte) {
	t.Helper()
	stream, ok := client.(*tcpClientStream)
	if !ok {
		t.Fatalf("client stream = %T, want *tcpClientStream", client)
	}
	if err := WriteFrame(stream.stream.conn, packetID, payload); err != nil {
		t.Fatalf("write raw client frame: %v", err)
	}
}
