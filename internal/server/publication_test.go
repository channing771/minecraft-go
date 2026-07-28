package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

func TestSnapshotPublicationHonorsChunkBudgetAndDistanceOrder(t *testing.T) {
	running, client, generator := newPublicationServer(t, 1, 2, 1<<20, false)
	prepareReadySquare(t, running, generator, core.ChunkPos{})

	first := recvServerMessage(t, client).(network.ChunkSnapshot)
	second := recvServerMessage(t, client).(network.ChunkSnapshot)
	if first.Chunk != (core.ChunkPos{}) ||
		second.Chunk != (core.ChunkPos{X: -1, Z: 0}) {
		t.Fatalf("前两个快照 = %+v, %+v", first.Chunk, second.Chunk)
	}
	assertNoServerMessage(t, client)
}

func TestSnapshotPublicationAllowsOneOversizedFirstChunk(t *testing.T) {
	running, client, generator := newPublicationServer(t, 1, 9, 1, false)
	prepareReadySquare(t, running, generator, core.ChunkPos{})

	message := recvServerMessage(t, client).(network.ChunkSnapshot)
	if message.Chunk != (core.ChunkPos{}) {
		t.Fatalf("首个 oversized 快照 = %+v", message.Chunk)
	}
	if message.PayloadBytes() <= 1 {
		t.Fatalf("测试快照没有超过 byte budget: %d", message.PayloadBytes())
	}
	assertNoServerMessage(t, client)
}

func TestInitialSnapshotCapturesSameTickChangesBeforeDelta(t *testing.T) {
	running, client, generator := newPublicationServer(t, 0, 4, 1<<20, true)
	running.incoming <- sim.Command{
		Session:   localSessionID,
		Sequence:  1,
		Kind:      sim.CommandSetViewCenter,
		Dimension: core.Overworld,
		Center:    core.ChunkPos{},
	}
	requested := running.Step()
	if len(requested.Generate) != 1 {
		t.Fatalf("Generate = %+v", requested.Generate)
	}
	running.engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     generator.chunk(core.ChunkPos{}),
	})
	running.incoming <- sim.Command{
		Session:   localSessionID,
		Sequence:  2,
		Kind:      sim.CommandBreakRay,
		Dimension: core.Overworld,
		Origin:    mgl32.Vec3{0.5, 2.5, 0.5},
		Direction: mgl32.Vec3{0, -1, 0},
	}
	result := running.Step()
	if len(result.Changes) != 1 {
		t.Fatalf("同 tick Changes = %+v", result.Changes)
	}

	snapshot := recvServerMessage(t, client).(network.ChunkSnapshot)
	if snapshot.Revision != 2 {
		t.Fatalf("初始 snapshot revision = %d，想要 2", snapshot.Revision)
	}
	assertNoServerMessage(t, client)
}

func TestPublishedDeltaIsContiguousAfterSnapshot(t *testing.T) {
	running, client, generator := newPublicationServer(t, 0, 4, 1<<20, true)
	prepareReadySquare(t, running, generator, core.ChunkPos{})
	snapshot := recvServerMessage(t, client).(network.ChunkSnapshot)
	if snapshot.Revision != 1 {
		t.Fatalf("初始 revision = %d", snapshot.Revision)
	}

	running.incoming <- sim.Command{
		Session:   localSessionID,
		Sequence:  2,
		Kind:      sim.CommandBreakRay,
		Dimension: core.Overworld,
		Origin:    mgl32.Vec3{0.5, 2.5, 0.5},
		Direction: mgl32.Vec3{0, -1, 0},
	}
	running.Step()
	delta := recvServerMessage(t, client).(network.BlockChanges)
	if delta.BaseRevision != 1 || delta.NewRevision != 2 {
		t.Fatalf("delta revision = %d→%d", delta.BaseRevision, delta.NewRevision)
	}
	if err := delta.Validate(); err != nil {
		t.Fatalf("发布了非法 delta: %v", err)
	}
}

func TestResyncSnapshotPrecedesOrdinaryPendingSnapshots(t *testing.T) {
	running, client, generator := newPublicationServer(t, 1, 1, 1<<20, false)
	prepareReadySquare(t, running, generator, core.ChunkPos{})
	first := recvServerMessage(t, client).(network.ChunkSnapshot)
	if first.Chunk != (core.ChunkPos{}) {
		t.Fatalf("首个快照 = %+v", first.Chunk)
	}

	running.incoming <- sim.Command{
		Session:      localSessionID,
		Sequence:     2,
		Kind:         sim.CommandResync,
		Dimension:    core.Overworld,
		Chunk:        core.ChunkPos{},
		HaveRevision: 0,
	}
	running.Step()
	resync := recvServerMessage(t, client).(network.ChunkSnapshot)
	if resync.Chunk != (core.ChunkPos{}) {
		t.Fatalf("resync 前发送了普通快照 %+v", resync.Chunk)
	}
}

func TestForgetRemovesPendingSnapshotsAndSortsChunks(t *testing.T) {
	running, client, generator := newPublicationServer(t, 1, 1, 1<<20, false)
	prepareReadySquare(t, running, generator, core.ChunkPos{})
	_ = recvServerMessage(t, client).(network.ChunkSnapshot)

	running.incoming <- sim.Command{
		Session:   localSessionID,
		Sequence:  2,
		Kind:      sim.CommandSetViewCenter,
		Dimension: core.Overworld,
		Center:    core.ChunkPos{X: 10, Z: 0},
	}
	running.Step()
	forgotten := recvServerMessage(t, client).(network.ForgetChunks)
	want := []core.ChunkPos{
		{X: -1, Z: -1}, {X: -1, Z: 0}, {X: -1, Z: 1},
		{X: 0, Z: -1}, {X: 0, Z: 0}, {X: 0, Z: 1},
		{X: 1, Z: -1}, {X: 1, Z: 0}, {X: 1, Z: 1},
	}
	if !reflect.DeepEqual(forgotten.Chunks, want) {
		t.Fatalf("ForgetChunks = %+v，想要 %+v", forgotten.Chunks, want)
	}
	assertNoServerMessage(t, client)
}

func newPublicationServer(
	t *testing.T,
	radius, snapshotChunks, snapshotBytes int,
	flat bool,
) (*Server, network.ClientEndpoint, *gatedGenerator) {
	t.Helper()
	client, endpoint := network.NewMemoryPair(64)
	generator := &gatedGenerator{
		release: make(chan struct{}),
		flat:    flat,
	}
	config := DefaultConfig(1)
	config.ViewRadius = radius
	config.Workers = 1
	config.SnapshotChunks = snapshotChunks
	config.SnapshotBytes = snapshotBytes
	config.OutboxCapacity = 64
	running := New(config, endpoint, generator)
	t.Cleanup(func() {
		close(generator.release)
		running.Close()
	})
	return running, client, generator
}

func prepareReadySquare(
	t *testing.T,
	running *Server,
	generator *gatedGenerator,
	center core.ChunkPos,
) {
	t.Helper()
	running.incoming <- sim.Command{
		Session:   localSessionID,
		Sequence:  1,
		Kind:      sim.CommandSetViewCenter,
		Dimension: core.Overworld,
		Center:    center,
	}
	requested := running.Step()
	if len(requested.Generate) == 0 {
		t.Fatal("没有 generation requests")
	}
	for _, key := range requested.Generate {
		running.engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     generator.chunk(key.Pos),
		})
	}
	ready := running.Step()
	if len(ready.Ready) != len(requested.Generate) {
		t.Fatalf("Ready = %d，想要 %d", len(ready.Ready), len(requested.Generate))
	}
}

func recvServerMessage(
	t *testing.T,
	client network.ClientEndpoint,
) network.ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := client.Recv(ctx)
	if err != nil {
		t.Fatalf("接收 server message: %v", err)
	}
	return message
}

func assertNoServerMessage(
	t *testing.T,
	client network.ClientEndpoint,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if message, err := client.Recv(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("意外 server message = %#v, err=%v", message, err)
	}
}

type gatedGenerator struct {
	release chan struct{}
	flat    bool
}

func (generator *gatedGenerator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	<-generator.release
	return generator.chunk(pos)
}

func (generator *gatedGenerator) BaseBlockAt(pos core.BlockPos) core.BlockID {
	if !generator.flat {
		return core.AirID
	}
	return publicationBaseBlock(pos)
}

func (generator *gatedGenerator) chunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	if !generator.flat {
		return chunk
	}
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			for y := int32(core.MinY); y <= 0; y++ {
				worldPos := core.BlockPos{
					X: pos.X<<core.SectionShift + int32(x),
					Y: y,
					Z: pos.Z<<core.SectionShift + int32(z),
				}
				chunk.SetBlock(x, y, z, publicationBaseBlock(worldPos))
			}
		}
	}
	chunk.Compact()
	return chunk
}

func publicationBaseBlock(pos core.BlockPos) core.BlockID {
	switch {
	case pos.Y < core.MinY || pos.Y >= core.MaxY:
		return core.AirID
	case pos.Y == core.MinY:
		return core.BedrockID
	case pos.Y < 0:
		return core.StoneID
	case pos.Y == 0:
		return core.GrassID
	default:
		return core.AirID
	}
}
