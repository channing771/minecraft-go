package client_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestCompanionsSpawnStatesInterpolateDespawnAndReset(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 10, "阿木", mgl32.Vec3{})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	for tick := uint64(11); tick <= 13; tick++ {
		if err := companions.ApplyStates(network.CompanionStates{
			Tick: tick,
			States: []network.CompanionState{{
				ID: spawn.ID, Dimension: core.Overworld,
				Position: mgl32.Vec3{float32(tick - 10), 0, 0},
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	companions.Advance(25 * time.Millisecond)
	got := companions.AppendPresentations(nil)
	if len(got) != 1 || got[0].Position != (mgl32.Vec3{1.5, 0, 0}) {
		t.Fatalf("interpolated presentations = %+v", got)
	}
	if err := companions.ApplyDespawn(network.CompanionDespawn{ID: spawn.ID}); err != nil {
		t.Fatal(err)
	}
	if got := companions.AppendPresentations(nil); len(got) != 0 {
		t.Fatalf("despawn left presentations: %+v", got)
	}
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	companions.Reset()
	if got := companions.AppendPresentations(nil); len(got) != 0 {
		t.Fatalf("Reset left presentations: %+v", got)
	}
}

func TestCompanionsRejectDuplicateUnknownStaleAndFiveAtomically(t *testing.T) {
	var companions client.Companions
	first := companionSpawn(1, 10, "甲", mgl32.Vec3{1, 0, 0})
	second := companionSpawn(2, 10, "乙", mgl32.Vec3{2, 0, 0})
	for _, spawn := range []network.CompanionSpawn{first, second} {
		if err := companions.ApplySpawn(spawn); err != nil {
			t.Fatal(err)
		}
	}
	want := companions.AppendPresentations(nil)
	invalidSpawn := first
	invalidSpawn.ID = companion.ID{}
	if err := companions.ApplySpawn(invalidSpawn); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("invalid spawn error = %v", err)
	}
	if err := companions.ApplySpawn(first); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("duplicate spawn error = %v", err)
	}
	unknown := network.CompanionStates{Tick: 11, States: []network.CompanionState{
		{ID: first.ID, Dimension: core.Overworld, Position: mgl32.Vec3{10, 0, 0}},
		{ID: companionTestID(3), Dimension: core.Overworld, Position: mgl32.Vec3{30, 0, 0}},
	}}
	if err := companions.ApplyStates(unknown); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("unknown state error = %v", err)
	}
	stale := network.CompanionStates{Tick: 10, States: []network.CompanionState{{
		ID: first.ID, Dimension: core.Overworld, Position: mgl32.Vec3{11, 0, 0},
	}}}
	if err := companions.ApplyStates(stale); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("stale state error = %v", err)
	}
	five := network.CompanionStates{Tick: 11}
	for last := byte(1); last <= 5; last++ {
		five.States = append(five.States, network.CompanionState{
			ID: companionTestID(last), Dimension: core.Overworld,
		})
	}
	if err := companions.ApplyStates(five); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("five-state error = %v", err)
	}
	if err := companions.ApplyDespawn(network.CompanionDespawn{ID: companionTestID(3)}); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("unknown despawn error = %v", err)
	}
	if err := companions.ApplyDespawn(network.CompanionDespawn{}); !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("invalid despawn error = %v", err)
	}
	if got := companions.AppendPresentations(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("rejected messages mutated companions: got %+v want %+v", got, want)
	}
}

func TestCompanionsRejectStateAtSpawnTickAtomically(t *testing.T) {
	var companions client.Companions
	spawn := companionSpawn(1, 7, "阿木", mgl32.Vec3{1, 0, 0})
	if err := companions.ApplySpawn(spawn); err != nil {
		t.Fatal(err)
	}
	want := companions.AppendPresentations(nil)
	err := companions.ApplyStates(network.CompanionStates{Tick: spawn.Tick, States: []network.CompanionState{{
		ID: spawn.ID, Dimension: core.Overworld, Position: mgl32.Vec3{9, 0, 0},
	}}})
	if !errors.Is(err, client.ErrCompanionProtocol) {
		t.Fatalf("same-tick state error = %v", err)
	}
	if got := companions.AppendPresentations(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("same-tick batch mutated companions: got %+v want %+v", got, want)
	}
}

func TestCompanionsPresentInIDOrder(t *testing.T) {
	var companions client.Companions
	for _, last := range []byte{4, 1, 3, 2} {
		if err := companions.ApplySpawn(companionSpawn(last, 1, string(rune('A'+last)), mgl32.Vec3{float32(last), 0, 0})); err != nil {
			t.Fatal(err)
		}
	}
	dst := make([]client.CompanionPresentation, 0, companion.MaxActive)
	dst = companions.AppendPresentations(dst)
	for index := range dst {
		if dst[index].ID != companionTestID(byte(index+1)) {
			t.Fatalf("presentation order = %+v", dst)
		}
	}
}

func TestChatEventsKeepLatestThirtyTwoInEventOrder(t *testing.T) {
	var events client.ChatEvents
	for eventID := uint64(1); eventID <= 40; eventID++ {
		if err := events.Apply(chatEvent(eventID)); err != nil {
			t.Fatal(err)
		}
	}
	dst := make([]network.ChatEvent, 0, 32)
	dst = events.Events(dst)
	if len(dst) != 32 || dst[0].EventID != 9 || dst[31].EventID != 40 {
		t.Fatalf("events = %+v", dst)
	}
}

func TestChatEventsRejectDuplicateOrStaleWithoutMutation(t *testing.T) {
	var events client.ChatEvents
	for _, eventID := range []uint64{5, 8} {
		if err := events.Apply(chatEvent(eventID)); err != nil {
			t.Fatal(err)
		}
	}
	want := events.Events(nil)
	for _, eventID := range []uint64{8, 7} {
		if err := events.Apply(chatEvent(eventID)); !errors.Is(err, client.ErrChatEventProtocol) {
			t.Fatalf("Apply event %d error = %v", eventID, err)
		}
	}
	if got := events.Events(nil); !reflect.DeepEqual(got, want) {
		t.Fatalf("stale event mutated ring: got %+v want %+v", got, want)
	}
}

func companionSpawn(last byte, tick uint64, name string, position mgl32.Vec3) network.CompanionSpawn {
	return network.CompanionSpawn{
		ID: companionTestID(last), Name: name, Tick: tick,
		Dimension: core.Overworld, Position: position,
	}
}

func companionTestID(last byte) companion.ID {
	return companion.ID{0: 0x12, 6: 0x40, 8: 0x80, 15: last}
}

func chatEvent(eventID uint64) network.ChatEvent {
	return network.ChatEvent{
		EventID: eventID, PlayerID: core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: 1},
		PlayerName: "Chen", CompanionID: companionTestID(1), CompanionName: "阿木",
		Kind: network.ChatEventAccepted, Command: "挖石头",
	}
}
