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
