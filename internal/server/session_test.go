package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

func TestSessionOutboxPreservesFIFO(t *testing.T) {
	endpoint := &recordingServerEndpoint{}
	var workers sync.WaitGroup
	session := newObserverSession(
		context.Background(), testSessionID, 1, endpoint, 4, &workers, nil,
	)

	for sequence := uint64(1); sequence <= 3; sequence++ {
		if !session.enqueue(network.CommandRejected{
			Sequence: sequence,
			Reason:   network.RejectNoTarget,
		}) {
			t.Fatalf("enqueue %d 失败", sequence)
		}
	}
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		if got := endpoint.sequences(); len(got) == 3 {
			if got[0] != 1 || got[1] != 2 || got[2] != 3 {
				t.Fatalf("发送顺序 = %v", got)
			}
			session.close()
			workers.Wait()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等待 FIFO writer 超时")
}

func TestSessionFullOutboxClosesWithoutBlocking(t *testing.T) {
	endpoint := newBlockingServerEndpoint()
	var workers sync.WaitGroup
	session := newObserverSession(
		context.Background(), testSessionID, 1, endpoint, 1, &workers, nil,
	)

	if !session.enqueue(network.CommandRejected{Sequence: 1}) {
		t.Fatal("第一次 enqueue 失败")
	}
	select {
	case <-endpoint.sendStarted:
	case <-time.After(waitDeadline):
		t.Fatal("writer 没有进入阻塞 Send")
	}
	if !session.enqueue(network.CommandRejected{Sequence: 2}) {
		t.Fatal("第二次 enqueue 没有填入 outbox")
	}

	started := time.Now()
	if session.enqueue(network.CommandRejected{Sequence: 3}) {
		t.Fatal("满 outbox 的第三次 enqueue 成功")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("满 outbox enqueue 阻塞了 %v", elapsed)
	}
	workers.Wait()
	if !session.closed() {
		t.Fatal("慢 session 没有关闭")
	}
}

type recordingServerEndpoint struct {
	mu       sync.Mutex
	messages []network.ServerMessage
}

func (endpoint *recordingServerEndpoint) Send(
	_ context.Context,
	message network.ServerMessage,
) error {
	endpoint.mu.Lock()
	endpoint.messages = append(endpoint.messages, message)
	endpoint.mu.Unlock()
	return nil
}

func (endpoint *recordingServerEndpoint) Recv(
	ctx context.Context,
) (network.ClientMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (endpoint *recordingServerEndpoint) Close() error {
	return nil
}

func (endpoint *recordingServerEndpoint) sequences() []uint64 {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	sequences := make([]uint64, len(endpoint.messages))
	for index, message := range endpoint.messages {
		sequences[index] = message.(network.CommandRejected).Sequence
	}
	return sequences
}

type blockingServerEndpoint struct {
	sendStarted chan struct{}
	startOnce   sync.Once
}

func newBlockingServerEndpoint() *blockingServerEndpoint {
	return &blockingServerEndpoint{sendStarted: make(chan struct{})}
}

func (endpoint *blockingServerEndpoint) Send(
	ctx context.Context,
	_ network.ServerMessage,
) error {
	endpoint.startOnce.Do(func() {
		close(endpoint.sendStarted)
	})
	<-ctx.Done()
	return ctx.Err()
}

func (endpoint *blockingServerEndpoint) Recv(
	ctx context.Context,
) (network.ClientMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (endpoint *blockingServerEndpoint) Close() error {
	return nil
}

var _ network.ServerEndpoint = (*recordingServerEndpoint)(nil)
var _ network.ServerEndpoint = (*blockingServerEndpoint)(nil)

func recordedDisconnect(endpoint *recordingServerEndpoint) (network.Disconnect, bool) {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	for _, message := range endpoint.messages {
		if disconnect, ok := message.(network.Disconnect); ok {
			return disconnect, true
		}
	}
	return network.Disconnect{}, false
}

func TestSessionFailSendsDisconnectForNamedCause(t *testing.T) {
	endpoint := &recordingServerEndpoint{}
	var workers sync.WaitGroup
	session := newObserverSession(
		context.Background(), testSessionID, 1, endpoint, 4, &workers, nil,
	)

	session.fail(errHeartbeatTimeout)
	workers.Wait()

	disconnect, ok := recordedDisconnect(endpoint)
	if !ok {
		t.Fatal("具名原因关闭时未发出 Disconnect")
	}
	if disconnect.Code != network.DisconnectTimeout {
		t.Fatalf("Disconnect.Code = %d, want %d", disconnect.Code, network.DisconnectTimeout)
	}
	if !strings.Contains(disconnect.Message, errHeartbeatTimeout.Error()) {
		t.Fatalf("Disconnect.Message = %q，未包含原因", disconnect.Message)
	}
}

func TestSessionFailSkipsDisconnectForClientGone(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"客户端已关闭", network.ErrClosed},
		{"上下文取消", context.Canceled},
		{"writer 写失败", errors.New("server: write failed")},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoint := &recordingServerEndpoint{}
			var workers sync.WaitGroup
			session := newObserverSession(
				context.Background(), testSessionID, 1, endpoint, 4, &workers, nil,
			)

			session.fail(test.err)
			workers.Wait()

			if disconnect, ok := recordedDisconnect(endpoint); ok {
				t.Fatalf("非白名单原因发出了 Disconnect: %+v", disconnect)
			}
		})
	}
}

func TestSessionFailKeepsExistingSemantics(t *testing.T) {
	endpoint := &recordingServerEndpoint{}
	var workers sync.WaitGroup
	detached := make(chan error, 4)
	session := newObserverSession(
		context.Background(), testSessionID, 1, endpoint, 4, &workers,
		func(_ sim.SessionID, _ uint64, cause error) bool {
			detached <- cause
			return true
		},
	)

	session.fail(errHeartbeatTimeout)
	session.fail(errUnknownClientMessage) // failOnce：第二次必须无效
	workers.Wait()

	select {
	case cause := <-detached:
		if !errors.Is(cause, errHeartbeatTimeout) {
			t.Fatalf("detach cause = %v，想要首个原因", cause)
		}
	case <-time.After(waitDeadline):
		t.Fatal("detach 未被触发")
	}
	select {
	case cause := <-detached:
		t.Fatalf("failOnce 失效，detach 被触发第二次: %v", cause)
	case <-time.After(200 * time.Millisecond):
	}
	if !session.closed() {
		t.Fatal("fail 之后 session 未标记为已关闭")
	}
}

func TestDisconnectCodeForMapsNamedCauses(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want network.DisconnectCode
	}{
		{"心跳超时", errHeartbeatTimeout, network.DisconnectTimeout},
		{"心跳回复无效", errInvalidHeartbeatReply, network.DisconnectProtocolViolation},
		{"未知客户端消息", errUnknownClientMessage, network.DisconnectProtocolViolation},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, ok := disconnectCodeFor(test.err)
			if !ok {
				t.Fatalf("具名原因 %v 未被映射", test.err)
			}
			if code != test.want {
				t.Fatalf("code = %d, want %d", code, test.want)
			}
		})
	}
}

func TestDisconnectCodeForRejectsEverythingElse(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"客户端已关闭", network.ErrClosed},
		{"上下文取消", context.Canceled},
		{"包装了 ErrClosed 的错误", fmt.Errorf("writer: %w", network.ErrClosed)},
		{"writer 写失败", errors.New("server: write failed")},
		{"writer panic", errors.New("server: session writer panic")},
		{"outbox 已满（在发布热路径上，见 GoDoc）", errSessionOutboxFull},
	} {
		t.Run(test.name, func(t *testing.T) {
			if code, ok := disconnectCodeFor(test.err); ok {
				t.Fatalf("非白名单原因 %v 被映射成 code=%d", test.err, code)
			}
		})
	}
}

func TestDisconnectCodeForUnwrapsNamedCauses(t *testing.T) {
	wrapped := fmt.Errorf("session %d: %w", testSessionID, errHeartbeatTimeout)
	code, ok := disconnectCodeFor(wrapped)
	if !ok || code != network.DisconnectTimeout {
		t.Fatalf("包装后的具名原因未被识别: code=%d ok=%v", code, ok)
	}
}
