package client_test

import (
	"testing"
	"time"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/worldgen"
)

func TestStreamerProducesSectionsAroundCenter(t *testing.T) {
	s := client.NewStreamer(worldgen.New(42), assets.NewRegistry(), 4)
	defer s.Close()

	s.SetCenter(core.ChunkPos{X: 0, Z: 0}, 2)
	waitForResults(t, s, 1, 30*time.Second)
}

func TestStreamerSurvivesPanickingWork(t *testing.T) {
	s := client.NewStreamer(worldgen.New(1), assets.NewRegistry(), 2)
	defer s.Close()

	s.InjectPanicForTest(core.ChunkPos{X: 1, Z: 1})
	s.SetCenter(core.ChunkPos{X: 0, Z: 0}, 2)
	waitForResults(t, s, 5, 30*time.Second)
}

func TestStreamerCacheRemainsBoundedWhileTravelling(t *testing.T) {
	s := client.NewStreamer(worldgen.New(7), assets.NewRegistry(), 4)
	defer s.Close()

	const radius = 1
	for x := int32(0); x < 20; x++ {
		center := core.ChunkPos{X: x, Z: 0}
		s.SetCenter(center, radius)
		results := waitForResults(t, s, 1, 30*time.Second)
		for _, result := range results {
			dx := result.Pos.X - center.X
			if dx < 0 {
				dx = -dx
			}
			dz := result.Pos.Z - center.Z
			if dz < 0 {
				dz = -dz
			}
			if dx > radius || int(dz) > radius {
				t.Fatalf("Drain 返回不可见旧结果 %+v，当前中心 %+v", result.Pos, center)
			}
		}
	}

	limit := (2*(radius+1) + 1) * (2*(radius+1) + 1)
	if got := s.Stats().CachedChunks; got > limit {
		t.Fatalf("缓存区块数 = %d，超过 halo 上限 %d", got, limit)
	}
}

func waitForResults(t *testing.T, s *client.Streamer, want int, timeout time.Duration) []client.MeshedSection {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out []client.MeshedSection
	for len(out) < want {
		if time.Now().After(deadline) {
			t.Fatalf("%s 内只产出了 %d 个区段，想要至少 %d；状态=%+v",
				timeout, len(out), want, s.Stats())
		}
		out = append(out, s.Drain(64)...)
		time.Sleep(time.Millisecond)
	}
	return out
}
