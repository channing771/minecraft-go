package network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"syscall"
	"time"
)

const acceptPollInterval = 50 * time.Millisecond

type tcpSocket interface {
	SetNoDelay(bool) error
	SetKeepAlive(bool) error
	SetKeepAlivePeriod(time.Duration) error
	Close() error
}

type tcpListener struct {
	listener *net.TCPListener
	addr     string
	accept   ownerGate

	closeOnce sync.Once
	closeErr  error
}

func DialTCP(ctx context.Context, address string) (ClientPacketStream, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, fmt.Errorf("network: dial TCP %q: %w", address, err)
	}
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("network: dial TCP %q returned %T", address, conn)
	}
	if err := configureTCPSocket(tcpConn); err != nil {
		return nil, fmt.Errorf("network: dial TCP %q: %w", address, err)
	}
	codec, err := NewCodec()
	if err != nil {
		_ = tcpConn.Close()
		return nil, err
	}
	return &tcpClientStream{stream: &tcpStream{conn: tcpConn, codec: codec}}, nil
}

func ListenTCP(address string) (Listener, error) {
	tcpAddress, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("network: resolve TCP address %q: %w", address, err)
	}
	listener, err := net.ListenTCP("tcp", tcpAddress)
	if err != nil {
		return nil, fmt.Errorf("network: listen TCP %q: %w", address, err)
	}
	return &tcpListener{listener: listener, addr: listener.Addr().String()}, nil
}

func (listener *tcpListener) Accept(ctx context.Context) (ServerPacketStream, error) {
	if err := listener.accept.acquire(ctx); err != nil {
		return nil, err
	}
	defer listener.accept.release()
	defer listener.listener.SetDeadline(time.Time{})

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(acceptPollInterval)
		if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
			deadline = contextDeadline
		}
		if err := listener.listener.SetDeadline(deadline); err != nil {
			if isTCPClosedError(err) {
				return nil, ErrClosed
			}
			return nil, fmt.Errorf("network: set TCP accept deadline: %w", err)
		}
		conn, err := listener.listener.AcceptTCP()
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			if isTCPClosedError(err) {
				return nil, ErrClosed
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return nil, fmt.Errorf("network: accept TCP: %w", err)
		}
		if err := configureTCPSocket(conn); err != nil {
			return nil, fmt.Errorf("network: accept TCP: %w", err)
		}
		codec, err := NewCodec()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return &tcpServerStream{
			stream: &tcpStream{conn: conn, codec: codec},
			peer:   conn.RemoteAddr().String(),
		}, nil
	}
}

func (listener *tcpListener) Addr() string {
	return listener.addr
}

func (listener *tcpListener) Close() error {
	if listener == nil {
		return nil
	}
	listener.closeOnce.Do(func() {
		listener.closeErr = listener.listener.Close()
		if isTCPClosedError(listener.closeErr) {
			listener.closeErr = nil
		}
	})
	return listener.closeErr
}

func configureTCPSocket(socket tcpSocket) error {
	if err := socket.SetNoDelay(true); err != nil {
		_ = socket.Close()
		return fmt.Errorf("set TCP_NODELAY: %w", err)
	}
	if err := socket.SetKeepAlive(true); err != nil {
		_ = socket.Close()
		return fmt.Errorf("set TCP keepalive: %w", err)
	}
	if err := socket.SetKeepAlivePeriod(30 * time.Second); err != nil {
		_ = socket.Close()
		return fmt.Errorf("set TCP keepalive period: %w", err)
	}
	return nil
}

func isTCPClosedError(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

func isTCPStreamClosedError(err error) bool {
	return isTCPClosedError(err) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) || errors.Is(err, syscall.EPIPE)
}

var _ Listener = (*tcpListener)(nil)
