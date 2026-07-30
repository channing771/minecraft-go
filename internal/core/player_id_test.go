package core_test

import (
	"strings"
	"testing"

	"minecraft-go/internal/core"
)

func TestPlayerIDCanonicalRoundTrip(t *testing.T) {
	want := "00112233-4455-4677-8899-aabbccddeeff"
	id, err := core.ParsePlayerID(want)
	if err != nil || !id.Valid() || id.String() != want {
		t.Fatalf("round trip = %q valid=%v err=%v", id.String(), id.Valid(), err)
	}
	for _, bad := range []string{
		"", "00112233445546778899aabbccddeeff",
		"00112233-4455-3677-8899-aabbccddeeff",
		"00112233-4455-4677-0899-aabbccddeeff",
		"00112233-4455-4677-8899-AABBCCDDEEFF",
		"00112233-4455-4677-8899-aabbccddee--",
	} {
		if _, err := core.ParsePlayerID(bad); err == nil {
			t.Fatalf("accepted non-canonical ID %q", bad)
		}
	}
}

func TestNormalizeDisplayName(t *testing.T) {
	got, err := core.NormalizeDisplayName("  陈 Chen  ")
	if err != nil || got != "陈 Chen" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, bad := range []string{"", "   ", "a\nb", strings.Repeat("界", 33)} {
		if _, err := core.NormalizeDisplayName(bad); err == nil {
			t.Fatalf("accepted name %q", bad)
		}
	}
}
