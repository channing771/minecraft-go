package server

import (
	"reflect"
	"testing"
)

func TestMultiplayerMemoryTCPMiningCompetition(t *testing.T) {
	runEightPlayerMemoryTCPParity(t, 200)
}

func runEightPlayerMemoryTCPParity(t *testing.T, ticks uint64) {
	t.Helper()
	memory := runEightManualMultiplayer(t, "login-memory", ticks)
	tcp := runEightManualMultiplayer(t, "login-tcp", ticks)
	if !reflect.DeepEqual(memory, tcp) {
		t.Fatalf("memory/TCP business results differ\nmemory=%+v\ntcp=%+v", memory, tcp)
	}
}
