//go:build darwin

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"minecraft-go/internal/network"
)

type canonicalCountingServerStream struct {
	inner network.ServerPacketStream
	codec *network.Codec
	bytes *atomic.Uint64
	epoch *benchmarkServerEpoch
	once  sync.Once
}

func (stream *canonicalCountingServerStream) Send(
	ctx context.Context,
	state network.State,
	packet network.ServerPacket,
) error {
	packetID, payload, err := stream.codec.EncodeServer(state, packet)
	if err != nil {
		return err
	}
	packetBytes := uvarintBytes(uint64(packetID)) + len(payload)
	logicalBytes := uvarintBytes(uint64(packetBytes)) + packetBytes
	measured := stream.epoch == nil || stream.epoch.measuring()
	if err := stream.inner.Send(ctx, state, packet); err != nil {
		return err
	}
	if measured {
		stream.bytes.Add(uint64(logicalBytes))
	}
	return nil
}

func (stream *canonicalCountingServerStream) Recv(
	ctx context.Context,
	state network.State,
) (network.ClientPacket, error) {
	return stream.inner.Recv(ctx, state)
}

func (stream *canonicalCountingServerStream) Peer() string { return stream.inner.Peer() }

func (stream *canonicalCountingServerStream) Close() error {
	var err error
	stream.once.Do(func() {
		err = stream.inner.Close()
		stream.codec.Close()
	})
	return err
}

func uvarintBytes(value uint64) int {
	var storage [binary.MaxVarintLen64]byte
	return binary.PutUvarint(storage[:], value)
}

type multiplayerServerClient struct {
	endpoint   network.ClientEndpoint
	serverDone <-chan error
	drainDone  <-chan error
}

func sendMultiplayerBenchmarkInputs(
	ctx context.Context,
	clients []multiplayerServerClient,
	sequence uint64,
) error {
	for index, connected := range clients {
		sendCtx, cancelSend := context.WithTimeout(ctx, time.Second)
		err := connected.endpoint.Send(sendCtx, network.PlayerInput{
			Sequence: sequence,
			MoveX:    int8((index+int(sequence))%3 - 1),
			MoveZ:    int8((index*2+int(sequence))%3 - 1),
			Jump:     sequence%40 == uint64(index),
			Yaw:      float32(sequence%360) * math.Pi / 180,
			Pitch:    float32(index-3) * 0.03,
		})
		cancelSend()
		if err != nil {
			return fmt.Errorf("发送固定脚本 player %d tick %d: %w", index, sequence, err)
		}
	}
	return nil
}
