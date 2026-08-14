package client_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestCompanionPresentationHotPathAllocations(t *testing.T) {
	var companions client.Companions
	for last := byte(4); last > 0; last-- {
		spawn := companionSpawn(last, 1, string(rune('A'+last)), mgl32.Vec3{float32(last), 0, 0})
		if err := companions.ApplySpawn(spawn); err != nil {
			t.Fatal(err)
		}
		for tick := uint64(2); tick <= 3; tick++ {
			if err := companions.ApplyStates(network.CompanionStates{
				Tick: tick,
				States: []network.CompanionState{{
					ID: spawn.ID, Dimension: core.Overworld,
					Position: mgl32.Vec3{float32(last) + float32(tick), 0, 0},
				}},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	dst := make([]client.CompanionPresentation, 0, 4)
	companions.Advance(25 * time.Millisecond)
	dst = companions.AppendPresentations(dst)
	allocations := testing.AllocsPerRun(1000, func() {
		companions.Advance(25 * time.Millisecond)
		dst = companions.AppendPresentations(dst[:0])
	})
	if allocations != 0 {
		t.Fatalf("warmed Advance+AppendPresentations allocations=%v want=0", allocations)
	}
	if len(dst) != 4 {
		t.Fatalf("presentation count=%d want=4", len(dst))
	}
}

// Mutation killed: allocating a new presentation slice or using reflect-based
// sorting makes the warmed caller-owned path allocate on every frame.
func TestRemotePlayersAppendPresentationsReusesSortedIndependentStorage(t *testing.T) {
	players := client.NewRemotePlayers()
	names := [...]string{"星野", "月河", "云山", "海界", "星河", "月海", "云野"}
	for last := byte(len(names)); last > 0; last-- {
		spawn := remotePlayerSpawn(
			last,
			1,
			names[last-1],
			mgl32.Vec3{float32(last), float32(last + 10), -float32(last)},
		)
		spawn.Yaw = float32(last) * 0.1
		spawn.Pitch = -float32(last) * 0.01
		if err := players.Apply(spawn); err != nil {
			t.Fatalf("Apply spawn %d: %v", last, err)
		}
	}

	dst := make([]client.RemotePresentation, 0, len(names))
	dst = players.AppendPresentations(dst)
	allocations := testing.AllocsPerRun(1000, func() {
		dst = players.AppendPresentations(dst[:0])
	})
	if allocations != 0 {
		t.Fatalf("warmed AppendPresentations allocations=%v want=0", allocations)
	}

	want := make([]client.RemotePresentation, len(names))
	for index, name := range names {
		last := byte(index + 1)
		want[index] = client.RemotePresentation{
			PlayerID:    remotePlayerTestID(last),
			DisplayName: name,
			Dimension:   core.Overworld,
			Position:    mgl32.Vec3{float32(last), float32(last + 10), -float32(last)},
			Yaw:         float32(last) * 0.1,
			Pitch:       -float32(last) * 0.01,
		}
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("AppendPresentations=%+v want=%+v", dst, want)
	}
	if got, wantLen := len(dst), len(players.Presentations()); got != wantLen {
		t.Fatalf("AppendPresentations len=%d roster len=%d", got, wantLen)
	}

	dst[0].DisplayName = "mutated"
	dst[0].Position[0] = 99
	fresh := players.AppendPresentations(make([]client.RemotePresentation, 0, len(names)))
	if fresh[0].DisplayName == "mutated" || fresh[0].Position[0] == 99 {
		t.Fatalf("returned slice mutation wrote back to roster: %+v", fresh[0])
	}

	players.Reset()
	dst = players.AppendPresentations(dst[:0])
	if len(dst) != 0 {
		t.Fatalf("empty roster retained %d stale presentations", len(dst))
	}
}
