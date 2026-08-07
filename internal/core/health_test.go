package core

import "testing"

func TestMaxHealthIsTwenty(t *testing.T) {
	if MaxHealth != 20 {
		t.Fatalf("MaxHealth = %d，想要 20", MaxHealth)
	}
}

func TestValidHealthAcceptsFullRange(t *testing.T) {
	for health := 0; health <= 20; health++ {
		if !ValidHealth(uint8(health)) {
			t.Fatalf("ValidHealth(%d) = false，想要 true", health)
		}
	}
}

func TestValidHealthRejectsAboveMax(t *testing.T) {
	for _, health := range []uint8{21, 22, 255} {
		if ValidHealth(health) {
			t.Fatalf("ValidHealth(%d) = true，想要 false", health)
		}
	}
}
