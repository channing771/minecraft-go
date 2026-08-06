package network

import (
	"context"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

var (
	benchmarkPacket    any
	benchmarkPayload   []byte
	benchmarkByteCount int
)

func BenchmarkSmallPacketCodec(b *testing.B) {
	id := core.PlayerID{0xb3, 0x13, 0x61, 0xa7, 0xc8, 0x62, 0x45, 0x97, 0x92, 0x13, 0xc3, 0xac, 0xd4, 0x68, 0xd1, 0x18}
	clientPackets := []struct {
		name  string
		state State
		value ClientPacket
	}{
		{"ClientHello", StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}},
		{"LoginStart", StateLogin, LoginStart{PlayerID: id, DisplayName: "Benchmark"}},
		{"PlayerInput", StatePlay, PlayerInput{Sequence: 1, MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.5, Pitch: -0.25, Mining: true}},
		{"PlaceBlock", StatePlay, PlaceBlock{Sequence: 3, Yaw: 0.5, Pitch: -0.25, Slot: 2}},
		{"RequestChunkResync", StatePlay, RequestChunkResync{Sequence: 4, Dimension: core.Overworld, Chunk: core.ChunkPos{X: -2, Z: 3}, HaveRevision: 7}},
		{"KeepAliveReply", StatePlay, KeepAliveReply{Token: 9}},
		{"DropSelectedItem", StatePlay, DropSelectedItem{Sequence: 12}},
	}
	serverPackets := []struct {
		name  string
		state State
		value ServerPacket
	}{
		{"ServerHello", StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}},
		{"HandshakeReject", StateHandshake, HandshakeReject{ServerProtocolVersion: ProtocolVersion, Code: HandshakeVersionMismatch, Message: "mismatch"}},
		{"LoginSuccess", StateLogin, LoginSuccess{PlayerID: id}},
		{"LoginReject", StateLogin, LoginReject{Code: LoginServerFull, Message: "full"}},
		{"BlockChanges", StatePlay, BlockChanges{Dimension: core.Overworld, Chunk: core.ChunkPos{}, BaseRevision: 1, NewRevision: 2, Changes: []BlockChange{{Position: core.BlockPos{X: 1, Y: 2, Z: 3}, Block: core.DirtID}}}},
		{"ForgetChunks", StatePlay, ForgetChunks{Dimension: core.Overworld, Chunks: []core.ChunkPos{{X: -1, Z: 2}}}},
		{"PlayerState", StatePlay, PlayerState{ServerTick: 5, LastInputSequence: 4, Dimension: core.Overworld, Position: mgl32.Vec3{1, 2, 3}, Velocity: mgl32.Vec3{0.1, 0.2, 0.3}, Ready: true, MiningActive: true, MiningTarget: core.BlockPos{X: 1, Y: 2, Z: 3}, MiningProgressTicks: 6, MiningRequiredTicks: 15, MiningHarvestable: true}},
		{"CommandRejected", StatePlay, CommandRejected{Sequence: 6, Reason: RejectNoTarget}},
		{"KeepAlive", StatePlay, KeepAlive{Token: 7}},
		{"Disconnect", StatePlay, Disconnect{Code: DisconnectServerShutdown, Message: "shutdown"}},
	}
	for _, fixture := range clientPackets {
		b.Run("EncodeClient/"+fixture.name, func(b *testing.B) {
			codec := benchmarkCodec(b)
			defer codec.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, payload, err := codec.EncodeClient(fixture.state, fixture.value)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkPayload = payload
			}
		})
		b.Run("DecodeClient/"+fixture.name, func(b *testing.B) {
			codec := benchmarkCodec(b)
			defer codec.Close()
			packetID, payload, err := codec.EncodeClient(fixture.state, fixture.value)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				packet, err := codec.DecodeClient(fixture.state, packetID, payload)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkPacket = packet
			}
		})
	}
	for _, fixture := range serverPackets {
		b.Run("EncodeServer/"+fixture.name, func(b *testing.B) {
			codec := benchmarkCodec(b)
			defer codec.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_, payload, err := codec.EncodeServer(fixture.state, fixture.value)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkPayload = payload
			}
		})
		b.Run("DecodeServer/"+fixture.name, func(b *testing.B) {
			codec := benchmarkCodec(b)
			defer codec.Close()
			packetID, payload, err := codec.EncodeServer(fixture.state, fixture.value)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				packet, err := codec.DecodeServer(fixture.state, packetID, payload)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkPacket = packet
			}
		})
	}
}

func BenchmarkRemotePlayerStateCodec(b *testing.B) {
	players := make([]RemotePlayerState, 7)
	for index := range players {
		players[index] = RemotePlayerState{
			PlayerID:  core.PlayerID{0, 0, 0, byte(index + 1), 0, 0, 0x40, byte(index + 1), 0x80, 0, 0, 0, 0, 0, 0, byte(index + 1)},
			Dimension: core.Overworld,
			Position:  mgl32.Vec3{float32(index), 80, float32(-index)},
			Yaw:       float32(index) * 0.1,
			Pitch:     float32(index) * -0.01,
		}
	}
	packet := RemotePlayerStates{ServerTick: 42, Players: players}
	codec := benchmarkCodec(b)
	defer codec.Close()
	packetID, payload, err := codec.EncodeServer(StatePlay, packet)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("Encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, encoded, err := codec.EncodeServer(StatePlay, packet)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkPayload = encoded
		}
	})
	b.Run("Decode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			decoded, err := codec.DecodeServer(StatePlay, packetID, payload)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkPacket = decoded
		}
	})
}

func BenchmarkWorstLegalChunkSnapshot(b *testing.B) {
	snapshot := worstLegalBenchmarkSnapshot()
	logical, err := encodeLogicalSnapshot(snapshot)
	if err != nil {
		b.Fatal(err)
	}
	codec := benchmarkCodec(b)
	defer codec.Close()
	packetID, payload, err := codec.EncodeServer(StatePlay, snapshot)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("Encode", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.ReportMetric(float64(len(logical))/float64(len(payload)), "compression-ratio")
		b.ReportMetric(float64(len(logical)), "logical-bytes")
		b.ReportMetric(float64(len(payload)), "wire-bytes")
		for range b.N {
			_, encoded, err := codec.EncodeServer(StatePlay, snapshot)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkPayload = encoded
		}
	})
	b.Run("Decode", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		b.ReportMetric(float64(len(logical))/float64(len(payload)), "compression-ratio")
		b.ReportMetric(float64(len(logical)), "logical-bytes")
		b.ReportMetric(float64(len(payload)), "wire-bytes")
		for range b.N {
			decoded, err := codec.DecodeServer(StatePlay, packetID, payload)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkPacket = decoded
		}
	})
}

func BenchmarkTCPLoopbackPlayerInput(b *testing.B) {
	client, server, closePair := benchmarkTCPPair(b)
	defer closePair()
	ctx := context.Background()
	packet := PlayerInput{Sequence: 1, MoveX: 1, MoveZ: -1, Yaw: 0.5, Pitch: -0.25, Mining: true}
	received := make(chan error, 1)
	go func() {
		for range b.N {
			value, err := server.Recv(ctx, StatePlay)
			if err != nil {
				received <- err
				return
			}
			benchmarkPacket = value
		}
		received <- nil
	}()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := client.Send(ctx, StatePlay, packet); err != nil {
			b.Fatal(err)
		}
		packet.Sequence++
	}
	if err := <-received; err != nil {
		b.Fatal(err)
	}
}

func BenchmarkTCPLoopbackChunkSnapshot(b *testing.B) {
	client, server, closePair := benchmarkTCPPair(b)
	defer closePair()
	ctx := context.Background()
	snapshot := worstLegalBenchmarkSnapshot()
	codec := benchmarkCodec(b)
	_, payload, err := codec.EncodeServer(StatePlay, snapshot)
	_ = codec.Close()
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(payload)))
	received := make(chan error, 1)
	go func() {
		for range b.N {
			value, err := client.Recv(ctx, StatePlay)
			if err != nil {
				received <- err
				return
			}
			benchmarkPacket = value
		}
		received <- nil
	}()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := server.Send(ctx, StatePlay, snapshot); err != nil {
			b.Fatal(err)
		}
	}
	if err := <-received; err != nil {
		b.Fatal(err)
	}
}

func benchmarkCodec(b *testing.B) *Codec {
	b.Helper()
	codec, err := NewCodec()
	if err != nil {
		b.Fatal(err)
	}
	return codec
}

func worstLegalBenchmarkSnapshot() ChunkSnapshot {
	sections := make([]SectionData, core.SectionsPerChunk)
	valuesPerWord := 64 / 15
	words := (core.BlocksPerSection + valuesPerWord - 1) / valuesPerWord
	for index := range sections {
		packed := make([]uint64, words)
		for word := range packed {
			packed[word] = (uint64(index+1)*0x0101010101010101 ^
				uint64(word)*0x9e3779b97f4a7c15) & ((uint64(1) << 60) - 1)
		}
		sections[index] = SectionData{Y: int32(index), Storage: SectionDirect, Bits: 15, Packed: packed}
	}
	return ChunkSnapshot{Dimension: core.Overworld, Chunk: core.ChunkPos{X: -3, Z: 7}, Revision: 19, Sections: sections}
}

func benchmarkTCPPair(b *testing.B) (ClientPacketStream, ServerPacketStream, func()) {
	b.Helper()
	listener, err := ListenTCP("127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	type acceptResult struct {
		stream ServerPacketStream
		err    error
	}
	accepted := make(chan acceptResult, 1)
	go func() {
		stream, err := listener.Accept(context.Background())
		accepted <- acceptResult{stream: stream, err: err}
	}()
	client, err := DialTCP(context.Background(), listener.Addr())
	if err != nil {
		_ = listener.Close()
		b.Fatal(err)
	}
	server := <-accepted
	if server.err != nil {
		_ = client.Close()
		_ = listener.Close()
		b.Fatal(server.err)
	}
	return client, server.stream, func() {
		_ = client.Close()
		_ = server.stream.Close()
		_ = listener.Close()
	}
}
