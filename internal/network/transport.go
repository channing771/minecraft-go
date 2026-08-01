package network

import (
	"context"
	"errors"
)

var ErrClosed = errors.New("network: transport closed")

type ClientEndpoint interface {
	Send(context.Context, ClientMessage) error
	Recv(context.Context) (ServerMessage, error)
	Close() error
}

type ServerEndpoint interface {
	Send(context.Context, ServerMessage) error
	Recv(context.Context) (ClientMessage, error)
	Close() error
}

type clientPlayEndpoint struct {
	stream ClientPacketStream
}

func newClientPlayEndpoint(stream ClientPacketStream) ClientEndpoint {
	return &clientPlayEndpoint{stream: stream}
}

func (endpoint *clientPlayEndpoint) Send(ctx context.Context, message ClientMessage) error {
	packet, ok := message.(ClientPacket)
	if !ok {
		return errors.New("network: client message is not a packet")
	}
	return endpoint.stream.Send(ctx, StatePlay, packet)
}

func (endpoint *clientPlayEndpoint) Recv(ctx context.Context) (ServerMessage, error) {
	for {
		packet, err := endpoint.stream.Recv(ctx, StatePlay)
		if err != nil {
			return nil, err
		}
		switch packet := packet.(type) {
		case KeepAlive:
			if err := endpoint.stream.Send(ctx, StatePlay, KeepAliveReply{Token: packet.Token}); err != nil {
				return nil, err
			}
		case Disconnect:
			_ = endpoint.stream.Close()
			return nil, &RemoteError{State: StatePlay, Code: uint8(packet.Code), Message: packet.Message}
		default:
			message, ok := packet.(ServerMessage)
			if !ok {
				_ = endpoint.stream.Close()
				return nil, protocolViolation(errors.New("unexpected server play packet"))
			}
			return message, nil
		}
	}
}

func (endpoint *clientPlayEndpoint) Close() error {
	return endpoint.stream.Close()
}

type serverPlayEndpoint struct {
	stream ServerPacketStream
}

func newServerPlayEndpoint(stream ServerPacketStream) ServerEndpoint {
	return &serverPlayEndpoint{stream: stream}
}

func (endpoint *serverPlayEndpoint) Send(ctx context.Context, message ServerMessage) error {
	packet, ok := message.(ServerPacket)
	if !ok {
		return errors.New("network: server message is not a packet")
	}
	return endpoint.stream.Send(ctx, StatePlay, packet)
}

func (endpoint *serverPlayEndpoint) Recv(ctx context.Context) (ClientMessage, error) {
	packet, err := endpoint.stream.Recv(ctx, StatePlay)
	if err != nil {
		return nil, err
	}
	message, ok := packet.(ClientMessage)
	if !ok {
		_ = endpoint.stream.Close()
		return nil, protocolViolation(errors.New("unexpected client play packet"))
	}
	return message, nil
}

func (endpoint *serverPlayEndpoint) Close() error {
	return endpoint.stream.Close()
}
