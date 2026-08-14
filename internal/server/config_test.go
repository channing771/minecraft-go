package server

import (
	"context"
	"strconv"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
)

func TestDefaultConfigUsesEightMaxPlayers(t *testing.T) {
	if got := DefaultConfig(42).MaxPlayers; got != 8 {
		t.Fatalf("DefaultConfig MaxPlayers = %d, want 8", got)
	}
}

func TestNewHostNormalizesZeroMaxPlayers(t *testing.T) {
	config := DefaultConfig(42)
	config.MaxPlayers = 0
	host := mustNewHost(t, config, flatTestGenerator{}, newHostTestStore())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host cleanup Shutdown: %v", err)
		}
	})
	if got := host.config.MaxPlayers; got != 8 {
		t.Fatalf("Host config MaxPlayers = %d, want normalized 8", got)
	}
}

func TestNewHostRejectsOutOfRangeMaxPlayers(t *testing.T) {
	for _, maxPlayers := range []int{-1, 9} {
		t.Run(strconv.Itoa(maxPlayers), func(t *testing.T) {
			config := DefaultConfig(42)
			config.MaxPlayers = maxPlayers
			defer func() {
				if recover() == nil {
					t.Fatalf("NewHost accepted MaxPlayers = %d", maxPlayers)
				}
			}()
			_, _ = NewHost(context.Background(), config, flatTestGenerator{}, newHostTestStore())
		})
	}
}

func TestServerConfigCompanionsValidatesDefinitions(t *testing.T) {
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig(42)
	config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	config.validate()

	config.Companions = append(config.Companions, companion.Definition{ID: id, Name: "另一个"})
	defer func() {
		if recover() == nil {
			t.Fatal("server.Config 接受重复伙伴 ID")
		}
	}()
	config.validate()
}

func TestNewWorldRejectsEnabledCompanions(t *testing.T) {
	config := hostTestConfig()
	config.Companions = []companion.Definition{{
		ID:   companion.ID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 1},
		Name: "阿木",
	}}
	defer func() {
		if recover() == nil {
			t.Fatal("NewWorld accepted enabled companions")
		}
	}()
	_ = NewWorld(config, flatTestGenerator{}, newHostTestStore())
}
