package network

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

func TestValidateClientPacket(t *testing.T) {
	validID := mustPlayerID(t)
	valid := []struct {
		name   string
		state  State
		packet ClientPacket
	}{
		{"hello", StateHandshake, ClientHello{ProtocolVersion: ProtocolVersion}},
		{"login start", StateLogin, LoginStart{PlayerID: validID, DisplayName: "Chen"}},
		{"input", StatePlay, PlayerInput{Yaw: 90, Pitch: -15, Mining: true}},
		{"place", StatePlay, PlaceBlock{Yaw: 90, Pitch: -15, Slot: 8}},
		{"select hotbar", StatePlay, SelectHotbar{Slot: 8}},
		{"move inventory stack", StatePlay, MoveInventoryStack{From: 0, To: core.InventorySlots - 1}},
		{"craft recipe", StatePlay, CraftRecipe{Recipe: core.RecipeStoneBricks}},
		{"resync", StatePlay, RequestChunkResync{}},
		{"keep alive reply", StatePlay, KeepAliveReply{Token: 1}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateClientPacket(tc.state, tc.packet); err != nil {
				t.Fatalf("valid packet rejected: %v", err)
			}
		})
	}

	invalid := []struct {
		name   string
		state  State
		packet ClientPacket
	}{
		{"unsupported protocol", StateHandshake, ClientHello{}},
		{"zero player ID", StateLogin, LoginStart{DisplayName: "Chen"}},
		{"non-v4 player ID", StateLogin, LoginStart{PlayerID: core.PlayerID{1}, DisplayName: "Chen"}},
		{"invalid display name", StateLogin, LoginStart{PlayerID: validID, DisplayName: "Chen\nName"}},
		{"zero keep alive token", StatePlay, KeepAliveReply{}},
		{"input NaN", StatePlay, PlayerInput{Yaw: float32(math.NaN())}},
		{"place NaN", StatePlay, PlaceBlock{Yaw: float32(math.NaN())}},
		{"place slot out of range", StatePlay, PlaceBlock{Slot: core.HotbarSlots}},
		{"select hotbar slot out of range", StatePlay, SelectHotbar{Slot: core.HotbarSlots}},
		{"inventory move out of range", StatePlay, MoveInventoryStack{To: core.InventorySlots}},
		{"inventory move same slot", StatePlay, MoveInventoryStack{From: 2, To: 2}},
		{"unknown craft recipe", StatePlay, CraftRecipe{}},
		{"resync outside overworld", StatePlay, RequestChunkResync{Dimension: core.DimensionID(1)}},
		{"play packet during handshake", StateHandshake, PlayerInput{}},
		{"play packet during login", StateLogin, PlayerInput{}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateClientPacket(tc.state, tc.packet); err == nil {
				t.Fatal("invalid packet accepted")
			}
		})
	}
}

func TestProtocolV1StateAndErrorCodesAreFrozen(t *testing.T) {
	states := []struct {
		name string
		got  State
		want State
	}{
		{"handshake", StateHandshake, 1},
		{"login", StateLogin, 2},
		{"play", StatePlay, 3},
	}
	for _, tc := range states {
		if tc.got != tc.want {
			t.Fatalf("%s state = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	if ProtocolVersion != 12 {
		t.Fatalf("protocol version = %d, want 12", ProtocolVersion)
	}

	codes := []struct {
		name string
		got  uint8
		want uint8
	}{
		{"handshake version mismatch", uint8(HandshakeVersionMismatch), 1},
		{"login server full", uint8(LoginServerFull), 1},
		{"login invalid identity", uint8(LoginInvalidIdentity), 2},
		{"login player data corrupt", uint8(LoginPlayerDataCorrupt), 3},
		{"login store unavailable", uint8(LoginStoreUnavailable), 4},
		{"login protocol violation", uint8(LoginProtocolViolation), 5},
		{"login internal error", uint8(LoginInternalError), 6},
		{"login already online", uint8(LoginAlreadyOnline), 7},
		{"disconnect protocol violation", uint8(DisconnectProtocolViolation), 1},
		{"disconnect timeout", uint8(DisconnectTimeout), 2},
		{"disconnect server shutdown", uint8(DisconnectServerShutdown), 3},
		{"disconnect slow client", uint8(DisconnectSlowClient), 4},
		{"disconnect internal error", uint8(DisconnectInternalError), 5},
	}
	for _, tc := range codes {
		if tc.got != tc.want {
			t.Fatalf("%s code = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestValidateServerPacket(t *testing.T) {
	validID := mustPlayerID(t)
	valid := []struct {
		name   string
		state  State
		packet ServerPacket
	}{
		{"hello", StateHandshake, ServerHello{ProtocolVersion: ProtocolVersion}},
		{"handshake reject", StateHandshake, HandshakeReject{Code: HandshakeVersionMismatch}},
		{"login success", StateLogin, LoginSuccess{PlayerID: validID}},
		{"login reject", StateLogin, LoginReject{Code: LoginServerFull}},
		{"snapshot", StatePlay, validPacketSnapshot()},
		{"block changes", StatePlay, validPacketBlockChanges()},
		{"forget chunks", StatePlay, ForgetChunks{Chunks: []core.ChunkPos{{}}}},
		{"player state", StatePlay, PlayerState{Position: mgl32.Vec3{1, 2, 3}, Velocity: mgl32.Vec3{4, 5, 6}, Yaw: 90, Pitch: -15, MiningActive: true, MiningTarget: core.BlockPos{X: 1, Y: 2, Z: 3}, MiningProgressTicks: 6, MiningRequiredTicks: 15, MiningHarvestable: true}},
		{"command reject", StatePlay, CommandRejected{Reason: RejectInvalidRay}},
		{"keep alive", StatePlay, KeepAlive{Token: 1}},
		{"disconnect", StatePlay, Disconnect{Code: DisconnectTimeout}},
		{"inventory state", StatePlay, InventoryState{}},
		{"item drop upserts", StatePlay, ItemDropUpserts{Drops: []ItemDrop{{
			ID:   core.DropID{Dimension: core.Overworld, Slot: 0, Generation: 1},
			Item: core.ItemStone, Count: 1,
		}}}},
		{"item drop removes", StatePlay, ItemDropRemoves{IDs: []core.DropID{{
			Dimension: core.Overworld, Slot: 0, Generation: 1,
		}}}},
	}
	for _, tc := range valid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateServerPacket(tc.state, tc.packet); err != nil {
				t.Fatalf("valid packet rejected: %v", err)
			}
		})
	}

	invalid := []struct {
		name   string
		state  State
		packet ServerPacket
	}{
		{"unsupported protocol", StateHandshake, ServerHello{}},
		{"zero handshake code", StateHandshake, HandshakeReject{}},
		{"unknown handshake code", StateHandshake, HandshakeReject{Code: HandshakeRejectCode(2)}},
		{"invalid login success ID", StateLogin, LoginSuccess{}},
		{"zero login code", StateLogin, LoginReject{}},
		{"unknown login code", StateLogin, LoginReject{Code: LoginRejectCode(8)}},
		{"zero keep alive token", StatePlay, KeepAlive{}},
		{"zero disconnect code", StatePlay, Disconnect{}},
		{"unknown disconnect code", StatePlay, Disconnect{Code: DisconnectCode(6)}},
		{"duplicate chunks", StatePlay, ForgetChunks{Chunks: []core.ChunkPos{{}, {}}}},
		{"no chunks", StatePlay, ForgetChunks{}},
		{"too many chunks", StatePlay, ForgetChunks{Chunks: tooManyUniqueChunks()}},
		{"too many block changes", StatePlay, tooManyValidBlockChanges()},
		{"player state NaN", StatePlay, PlayerState{Position: mgl32.Vec3{float32(math.NaN()), 0, 0}}},
		{"player state infinity", StatePlay, PlayerState{Velocity: mgl32.Vec3{0, float32(math.Inf(1)), 0}}},
		{"inactive player state has target", StatePlay, PlayerState{MiningTarget: core.BlockPos{X: 1}}},
		{"inactive player state has progress", StatePlay, PlayerState{MiningProgressTicks: 1}},
		{"inactive player state has required", StatePlay, PlayerState{MiningRequiredTicks: 1}},
		{"inactive player state is harvestable", StatePlay, PlayerState{MiningHarvestable: true}},
		{"active player state has zero progress", StatePlay, PlayerState{MiningActive: true, MiningRequiredTicks: 1}},
		{"active player state completes", StatePlay, PlayerState{MiningActive: true, MiningProgressTicks: 1, MiningRequiredTicks: 1}},
		{"active player state exceeds completion", StatePlay, PlayerState{MiningActive: true, MiningProgressTicks: 2, MiningRequiredTicks: 1}},
		{"unknown command rejection", StatePlay, CommandRejected{Reason: RejectReason("other")}},
		{"inventory state out of range", StatePlay, InventoryState{Inventory: core.Inventory{Hotbar: core.Hotbar{Selected: core.HotbarSlots}}}},
		{"empty drop upserts", StatePlay, ItemDropUpserts{}},
		{"empty drop removes", StatePlay, ItemDropRemoves{}},
		{"play packet during handshake", StateHandshake, KeepAlive{Token: 1}},
		{"play packet during login", StateLogin, KeepAlive{Token: 1}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateServerPacket(tc.state, tc.packet); err == nil {
				t.Fatal("invalid packet accepted")
			}
		})
	}
}

func mustPlayerID(t *testing.T) core.PlayerID {
	t.Helper()
	id, err := core.ParsePlayerID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatalf("ParsePlayerID: %v", err)
	}
	return id
}

func validPacketSnapshot() ChunkSnapshot {
	sections := make([]SectionData, core.SectionsPerChunk)
	for index := range sections {
		sections[index] = SectionData{Y: int32(index), Storage: SectionSingle, Single: core.AirID}
	}
	return ChunkSnapshot{Revision: 1, Sections: sections}
}

func validPacketBlockChanges() BlockChanges {
	return BlockChanges{
		BaseRevision: 1,
		NewRevision:  2,
		Changes: []BlockChange{{
			Position: core.BlockPos{Y: core.MinY},
			Block:    core.StoneID,
		}},
	}
}

func tooManyUniqueChunks() []core.ChunkPos {
	chunks := make([]core.ChunkPos, 4097)
	for index := range chunks {
		chunks[index] = core.ChunkPos{X: int32(index)}
	}
	return chunks
}

func tooManyValidBlockChanges() BlockChanges {
	changes := make([]BlockChange, 4097)
	for index := range changes {
		changes[index] = BlockChange{
			Position: core.BlockPos{
				X: int32(index % core.SectionSize),
				Y: core.MinY + int32(index/(core.SectionSize*core.SectionSize)),
				Z: int32((index / core.SectionSize) % core.SectionSize),
			},
			Block: core.StoneID,
		}
	}
	return BlockChanges{BaseRevision: 1, NewRevision: 2, Changes: changes}
}
