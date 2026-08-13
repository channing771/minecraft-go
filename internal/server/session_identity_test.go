package server

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

func TestSessionIdentityMetadataIsValidatedBeforeRegister(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	_, endpoint := network.NewMemoryPair(8)
	valid := registrySessionSpec(7, 1, endpoint)
	for _, mutate := range []func(*SessionSpec){
		func(spec *SessionSpec) { spec.PlayerID = core.PlayerID{} },
		func(spec *SessionSpec) { spec.DisplayName = "  Chen  " },
		func(spec *SessionSpec) { spec.DisplayName = "" },
	} {
		spec := valid
		mutate(&spec)
		if _, err := running.AttachSession(spec); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("AttachSession(%+v) error=%v", spec, err)
		}
		if _, ok := running.PlayerStateFor(spec.ID); ok {
			t.Fatal("invalid identity reached sim")
		}
	}
}

func TestSessionIdentityRejectsDuplicatePlayerIDAndAllowsReuseAfterDetach(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client7, endpoint7 := network.NewMemoryPair(8)
	client8, endpoint8 := network.NewMemoryPair(8)
	client9, endpoint9 := network.NewMemoryPair(8)
	t.Cleanup(func() {
		_ = client7.Close()
		_ = client8.Close()
		_ = client9.Close()
	})

	first := registrySessionSpec(7, 1, endpoint7)
	first.DisplayName = "陈 Chen"
	exit7, err := running.AttachSession(first)
	if err != nil {
		t.Fatal(err)
	}
	current := running.sessions[first.ID]
	if current.playerID != first.PlayerID || current.displayName != first.DisplayName {
		t.Fatalf("session identity=(%v,%q), want (%v,%q)",
			current.playerID, current.displayName, first.PlayerID, first.DisplayName)
	}

	duplicate := registrySessionSpec(8, 1, endpoint8)
	duplicate.PlayerID = first.PlayerID
	if _, err := running.AttachSession(duplicate); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate PlayerID error=%v, want %v", err, ErrSessionExists)
	}
	if _, ok := running.PlayerStateFor(duplicate.ID); ok {
		t.Fatal("duplicate identity reached sim")
	}

	if !running.DetachSession(first.ID, first.Generation, nil) {
		t.Fatal("detach first identity failed")
	}
	<-exit7
	reused := registrySessionSpec(9, 2, endpoint9)
	reused.PlayerID = first.PlayerID
	if _, err := running.AttachSession(reused); err != nil {
		t.Fatalf("reattach detached identity: %v", err)
	}
}

func TestSessionIdentityDetachPreservesReassignedIndex(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client, endpoint := network.NewMemoryPair(8)
	t.Cleanup(func() { _ = client.Close() })

	spec := registrySessionSpec(7, 1, endpoint)
	exit, err := running.AttachSession(spec)
	if err != nil {
		t.Fatal(err)
	}
	running.playerSessions[spec.PlayerID] = 8
	if !running.DetachSession(spec.ID, spec.Generation, nil) {
		t.Fatal("detach failed")
	}
	<-exit
	if got := running.playerSessions[spec.PlayerID]; got != 8 {
		t.Fatalf("reassigned identity index=%d, want 8", got)
	}
}

func TestSessionIdentityTrustedObserverHasNoStableIdentity(t *testing.T) {
	config := registryTestConfig()
	config.TrustedObserver = true
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client, endpoint := network.NewMemoryPair(8)
	t.Cleanup(func() { _ = client.Close() })

	if err := running.AttachTrustedObserver(endpoint); err != nil {
		t.Fatal(err)
	}
	if running.trustedObserver.playerID != (core.PlayerID{}) ||
		running.trustedObserver.displayName != "" {
		t.Fatalf("trusted observer identity=(%v,%q)",
			running.trustedObserver.playerID, running.trustedObserver.displayName)
	}
	if len(running.playerSessions) != 0 {
		t.Fatalf("trusted observer entered identity index: %+v", running.playerSessions)
	}
}

func TestSessionIdentityDoesNotLeakIntoSimPublicValues(t *testing.T) {
	for _, value := range []any{sim.PlayerUpdate{}, sim.PlayerSnapshot{}} {
		typeOf := reflect.TypeOf(value)
		for _, forbidden := range []string{"PlayerID", "DisplayName"} {
			if _, ok := typeOf.FieldByName(forbidden); ok {
				t.Fatalf("sim.%s unexpectedly exposes %s", typeOf.Name(), forbidden)
			}
		}
	}
}

func registrySessionSpec(
	id sim.SessionID,
	generation uint64,
	endpoint network.ServerEndpoint,
) SessionSpec {
	return registrySessionSpecWithRestore(id, generation, endpoint, testRestore())
}

func registrySessionSpecWithRestore(
	id sim.SessionID,
	generation uint64,
	endpoint network.ServerEndpoint,
	restore sim.PlayerRestore,
) SessionSpec {
	return SessionSpec{
		ID:          id,
		Generation:  generation,
		PlayerID:    core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, byte(id)},
		DisplayName: fmt.Sprintf("Player-%d", id),
		Endpoint:    endpoint,
		Restore:     restore,
	}
}
