package sim

import (
	"math"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
)

func TestEngineAppliesOnlyLatestPlayerInputOncePerTick(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	before, _ := engine.Player(session)
	engine.Enqueue(Command{Session: session, Sequence: 2, Kind: CommandPlayerInput, MoveZ: 1})
	engine.Enqueue(Command{Session: session, Sequence: 3, Kind: CommandPlayerInput, MoveX: 1})

	after := onlyMovementPlayer(t, engine.Step())
	if after.LastInputSequence != 3 {
		t.Fatalf("ack=%d，想要 3", after.LastInputSequence)
	}
	if after.State.Position.Z() != before.State.Position.Z() {
		t.Fatalf("较早 MoveZ 被执行: before=%v after=%v", before.State, after.State)
	}
	if after.State.Position.X() <= before.State.Position.X() {
		t.Fatalf("最新 MoveX 未执行: before=%v after=%v", before.State, after.State)
	}
}

func TestInvalidLatestInputIsAckedAndNeutral(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	engine.Enqueue(Command{Session: session, Sequence: 2, Kind: CommandPlayerInput, MoveX: 1})
	moving := onlyMovementPlayer(t, engine.Step())

	engine.Enqueue(Command{Session: session, Sequence: 3, Kind: CommandPlayerInput, MoveZ: 1})
	engine.Enqueue(Command{Session: session, Sequence: 4, Kind: CommandPlayerInput, MoveZ: 2})
	result := engine.Step()
	after := onlyMovementPlayer(t, result)
	if after.LastInputSequence != 4 || len(result.Rejected) != 1 ||
		result.Rejected[0] != (Rejection{Session: session, Sequence: 4, Reason: RejectInvalidInput}) {
		t.Fatalf("result=%+v", result)
	}
	if after.State.Position != moving.State.Position || after.State.Velocity != (mgl32.Vec3{}) {
		t.Fatalf("非法最新输入没有改用中立状态: moving=%+v after=%+v", moving.State, after.State)
	}

	held := onlyMovementPlayer(t, engine.Step())
	if held.State != after.State || held.LastInputSequence != 4 {
		t.Fatalf("非法输入后的 held 状态不是中立: after=%+v held=%+v", after, held)
	}
}

func TestEngineReusesHeldPlayerInputWithoutNewCommand(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	engine.Enqueue(Command{Session: session, Sequence: 2, Kind: CommandPlayerInput, MoveX: 1})
	first := onlyMovementPlayer(t, engine.Step())
	second := onlyMovementPlayer(t, engine.Step())

	if second.LastInputSequence != 2 {
		t.Fatalf("没有新输入时 ack=%d，想要 2", second.LastInputSequence)
	}
	if second.State.Position.X() <= first.State.Position.X() {
		t.Fatalf("没有沿用 MoveX held state: first=%+v second=%+v", first.State, second.State)
	}
}

func TestPlayerInputValidationBoundaries(t *testing.T) {
	maxPitch := float32(math.Pi/2 - 0.01)
	invalid := []struct {
		name  string
		input Command
	}{
		{name: "MoveX below", input: Command{MoveX: -2}},
		{name: "MoveX above", input: Command{MoveX: 2}},
		{name: "MoveZ below", input: Command{MoveZ: -2}},
		{name: "MoveZ above", input: Command{MoveZ: 2}},
		{name: "yaw NaN", input: Command{Yaw: float32(math.NaN())}},
		{name: "yaw positive infinity", input: Command{Yaw: float32(math.Inf(1))}},
		{name: "pitch NaN", input: Command{Pitch: float32(math.NaN())}},
		{name: "pitch negative infinity", input: Command{Pitch: float32(math.Inf(-1))}},
		{name: "pitch below", input: Command{Pitch: math.Nextafter32(-maxPitch, float32(math.Inf(-1)))}},
		{name: "pitch above", input: Command{Pitch: math.Nextafter32(maxPitch, float32(math.Inf(1)))}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			engine, session := readyMovementPlayer(t)
			command := tc.input
			command.Session = session
			command.Sequence = 2
			command.Kind = CommandPlayerInput
			engine.Enqueue(command)

			result := engine.Step()
			player := onlyMovementPlayer(t, result)
			if player.LastInputSequence != 2 || len(result.Rejected) != 1 ||
				result.Rejected[0].Reason != RejectInvalidInput {
				t.Fatalf("player=%+v rejected=%+v", player, result.Rejected)
			}
		})
	}

	for _, pitch := range []float32{-maxPitch, maxPitch} {
		engine, session := readyMovementPlayer(t)
		engine.Enqueue(Command{
			Session: session, Sequence: 2, Kind: CommandPlayerInput,
			MoveX: -1, MoveZ: 1, Yaw: float32(3 * math.Pi), Pitch: pitch,
		})
		result := engine.Step()
		player := onlyMovementPlayer(t, result)
		if len(result.Rejected) != 0 || player.Pitch != pitch ||
			player.Yaw < -float32(math.Pi) || player.Yaw >= float32(math.Pi) ||
			math.Abs(float64(player.Yaw+float32(math.Pi))) > 1e-5 {
			t.Fatalf("合法边界未接受或 yaw 未归一化: player=%+v rejected=%+v", player, result.Rejected)
		}
	}
}

func TestPlayerRecoveryResetsFallAndNonFiniteState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*playerState)
	}{
		{
			name: "feet below recovery floor",
			mutate: func(player *playerState) {
				player.state.Position[1] = float32(core.MinY - 17)
			},
		},
		{
			name: "non-finite velocity",
			mutate: func(player *playerState) {
				player.state.Velocity[0] = float32(math.NaN())
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine, session := readyMovementPlayer(t)
			tc.mutate(engine.sessions[session].player)

			player := onlyMovementPlayer(t, engine.Step())
			wantPosition := mgl32.Vec3{0.5, core.MaxY + 1, 0.5}
			if player.Ready || player.Reset || player.State.Position != wantPosition ||
				!physics.ValidState(player.State) {
				t.Fatalf("不安全状态没有进入有限 PendingSpawn: %+v", player)
			}
		})
	}
}

func TestPlayerRecoveryKeepsInputAckAcrossRespawn(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	engine.Enqueue(Command{Session: session, Sequence: 2, Kind: CommandPlayerInput, MoveX: 1})
	if player := onlyMovementPlayer(t, engine.Step()); player.LastInputSequence != 2 {
		t.Fatalf("移动输入 ack=%d，想要 2", player.LastInputSequence)
	}
	engine.sessions[session].player.state.Position[1] = float32(core.MinY - 17)
	if pending := onlyMovementPlayer(t, engine.Step()); pending.Ready || pending.LastInputSequence != 2 {
		t.Fatalf("恢复 PendingSpawn 丢失 ack: %+v", pending)
	}
	if respawned := onlyMovementPlayer(t, engine.Step()); !respawned.Ready ||
		respawned.LastInputSequence != 2 {
		t.Fatalf("重新出生后 ack 回退: %+v", respawned)
	}
}

func TestPlayerRecoveryUsesFirstFreeSixteenth(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	player := engine.sessions[session].player
	player.state.Position[1] = 15.0 / 16.0
	before := player.state.Position.Y()
	if !engine.tryUnstick(player, engine.dimensions[core.Overworld]) {
		t.Fatal("可向上解除的卡入被拒绝")
	}
	if player.state.Position.Y()-before != 1.0/16.0 {
		t.Fatalf("没有采用第一个无碰撞 1/16 位置: before=%v after=%v", before, player.state.Position.Y())
	}

	after := onlyMovementPlayer(t, engine.Step())
	if !after.Ready || after.Reset || after.State.Position.Y() != 1 {
		t.Fatalf("解除卡入后没有保持 Active: %+v", after)
	}
}

func TestPlayerRecoveryResetsUnresolvedOrUnknownOverlap(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*playerState)
	}{
		{
			name: "still intersecting after one block",
			mutate: func(player *playerState) {
				player.state.Position[1] = -1.0 / 16.0
			},
		},
		{
			name: "unknown neighbor",
			mutate: func(player *playerState) {
				player.state.Position[0] = 15.75
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine, session := readyMovementPlayer(t)
			tc.mutate(engine.sessions[session].player)

			player := onlyMovementPlayer(t, engine.Step())
			if player.Ready || player.Reset ||
				player.State.Position != (mgl32.Vec3{0.5, core.MaxY + 1, 0.5}) {
				t.Fatalf("无法解析的卡入没有重置: %+v", player)
			}
		})
	}
}

func TestPlayerReplayProducesIdenticalPlayerAndWorldHashes(t *testing.T) {
	type replayState struct {
		playerHash [32]byte
		chunkHash  [32]byte
		revision   uint64
	}
	run := func() replayState {
		engine, session := readyMovementPlayer(t)
		script := [][]Command{
			{{Session: session, Sequence: 2, Kind: CommandPlayerInput, MoveX: 1, Yaw: 0.25, Pitch: -0.1}},
			nil,
			{
				{Session: session, Sequence: 3, Kind: CommandPlayerInput, MoveZ: 1, Yaw: -0.75, Pitch: 0.2},
				{Session: session, Sequence: 4, Kind: CommandPlayerInput, MoveX: -1, Yaw: 1.25, Pitch: 0.3},
			},
		}
		for _, commands := range script {
			for _, command := range commands {
				engine.Enqueue(command)
			}
			engine.Step()
		}
		playerHash, ok := engine.PlayerHash(session)
		if !ok {
			t.Fatal("权威玩家 hash 不可用")
		}
		chunkHash, revision, ok := engine.ChunkHash(core.ChunkKey{Dimension: core.Overworld})
		if !ok {
			t.Fatal("权威区块 hash 不可用")
		}
		return replayState{playerHash: playerHash, chunkHash: chunkHash, revision: revision}
	}

	if first, second := run(), run(); !reflect.DeepEqual(first, second) {
		t.Fatalf("两次玩家移动 replay 不同: %v != %v", first, second)
	}
}

func TestPlayerHashCoversEveryAuthoritativeField(t *testing.T) {
	engine, sessionID := readyMovementPlayer(t)
	session := engine.sessions[sessionID]
	originalPlayer := *session.player
	originalDimension := session.dimension
	baseline, ok := engine.PlayerHash(sessionID)
	if !ok {
		t.Fatal("权威玩家 hash 不可用")
	}
	if _, ok := engine.PlayerHash(999); ok {
		t.Fatal("未知 session 返回了玩家 hash")
	}

	tests := []struct {
		name   string
		mutate func(*sessionState, *playerState)
	}{
		{name: "dimension", mutate: func(s *sessionState, _ *playerState) { s.dimension = 7 }},
		{name: "lifecycle", mutate: func(_ *sessionState, p *playerState) { p.lifecycle = PlayerPendingSpawn }},
		{name: "position x", mutate: func(_ *sessionState, p *playerState) { p.state.Position[0] += 0.25 }},
		{name: "position y", mutate: func(_ *sessionState, p *playerState) { p.state.Position[1] += 0.25 }},
		{name: "position z", mutate: func(_ *sessionState, p *playerState) { p.state.Position[2] += 0.25 }},
		{name: "velocity x", mutate: func(_ *sessionState, p *playerState) { p.state.Velocity[0] = 1 }},
		{name: "velocity y", mutate: func(_ *sessionState, p *playerState) { p.state.Velocity[1] = 1 }},
		{name: "velocity z", mutate: func(_ *sessionState, p *playerState) { p.state.Velocity[2] = 1 }},
		{name: "on ground", mutate: func(_ *sessionState, p *playerState) { p.state.OnGround = false }},
		{name: "yaw", mutate: func(_ *sessionState, p *playerState) { p.yaw = 1 }},
		{name: "pitch", mutate: func(_ *sessionState, p *playerState) { p.pitch = 1 }},
		{name: "input x", mutate: func(_ *sessionState, p *playerState) { p.input.MoveX = 1 }},
		{name: "input z", mutate: func(_ *sessionState, p *playerState) { p.input.MoveZ = 1 }},
		{name: "input jump", mutate: func(_ *sessionState, p *playerState) { p.input.Jump = true }},
		{name: "input yaw", mutate: func(_ *sessionState, p *playerState) { p.input.Yaw = 1 }},
		{name: "last input sequence", mutate: func(_ *sessionState, p *playerState) { p.lastInputSequence = 1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			player := originalPlayer
			session.player = &player
			session.dimension = originalDimension
			tc.mutate(session, &player)
			got, ok := engine.PlayerHash(sessionID)
			if !ok || got == baseline {
				t.Fatalf("字段变化没有改变 hash: ok=%v hash=%x", ok, got)
			}
		})
	}
	session.player = &originalPlayer
	session.dimension = originalDimension
}

func TestEngineMovesBeforeReconcilingAndExecutingInteractions(t *testing.T) {
	engine, sessionID := readyMovementPlayer(t)
	nextChunk := core.ChunkPos{X: 1}
	loadMovementChunk(t, engine.dimensions[core.Overworld], movementFlatChunk(nextChunk))
	player := engine.sessions[sessionID].player
	player.state = physics.State{
		Position: mgl32.Vec3{15.9, 1, 0.5},
		Velocity: mgl32.Vec3{physics.WalkSpeed, 0, 0},
		OnGround: true,
	}
	engine.Enqueue(Command{
		Session: sessionID, Sequence: 2, Kind: CommandPlayerInput, MoveX: 1,
	})
	engine.Enqueue(Command{
		Session: sessionID, Sequence: 3, Kind: CommandBreakBlock,
		Pitch: -float32(math.Pi)/2 + 0.01,
	})

	result := engine.Step()
	after := onlyMovementPlayer(t, result)
	if after.ViewCenter != nextChunk || after.State.Position.X() < 16 {
		t.Fatalf("订阅中心没有使用本 tick 权威移动结果: %+v", after)
	}
	if len(result.Rejected) != 0 || len(result.Changes) != 1 {
		t.Fatalf("移动后的新订阅没有在交互前生效: %+v", result)
	}
	chunk, _, ok := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld, Pos: nextChunk})
	if !ok || chunk.BlockAt(0, 0, 0) != core.AirID {
		t.Fatalf("新订阅区块交互未执行: ok=%v chunk=%v", ok, chunk)
	}
}

func TestPlayerCenterDerivationAlsoRunsWhenTrustedObserverChanges(t *testing.T) {
	engine, sessionID := readyMovementPlayer(t)
	loadMovementChunk(t, engine.dimensions[core.Overworld], movementFlatChunk(core.ChunkPos{X: 1}))
	player := engine.sessions[sessionID].player
	player.state = physics.State{
		Position: mgl32.Vec3{15.9, 1, 0.5},
		Velocity: mgl32.Vec3{physics.WalkSpeed, 0, 0},
		OnGround: true,
	}
	engine.Enqueue(Command{
		Session: sessionID, Sequence: 2, Kind: CommandPlayerInput, MoveX: 1,
	})
	engine.Enqueue(Command{
		Session: 2, Sequence: 1, Kind: CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: core.ChunkPos{X: 8},
	})

	playerUpdate := onlyMovementPlayer(t, engine.Step())
	if playerUpdate.ViewCenter != (core.ChunkPos{X: 1}) {
		t.Fatalf("legacy view 变化阻止了玩家中心派生: %+v", playerUpdate)
	}
}

func readyMovementPlayer(t *testing.T) (*Engine, SessionID) {
	t.Helper()
	engine := NewEngine(movementFlatBaseBlock, 0)
	session := SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	requested := engine.Step()
	wantKey := core.ChunkKey{Dimension: core.Overworld}
	if !reflect.DeepEqual(requested.Generate, []core.ChunkKey{wantKey}) {
		t.Fatalf("Generate=%+v，想要 %+v", requested.Generate, wantKey)
	}
	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Chunk:     movementFlatChunk(core.ChunkPos{}),
	})
	spawned := onlyMovementPlayer(t, engine.Step())
	if !spawned.Ready || spawned.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("玩家没有在 flat world 激活: %+v", spawned)
	}
	return engine, session
}

func onlyMovementPlayer(t *testing.T, result TickResult) PlayerUpdate {
	t.Helper()
	if len(result.Players) != 1 {
		t.Fatalf("Players=%+v，想要恰好一个", result.Players)
	}
	return result.Players[0]
}

func movementFlatChunk(position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	for x := 0; x < core.SectionSize; x++ {
		for z := 0; z < core.SectionSize; z++ {
			chunk.SetBlock(x, 0, z, core.GrassID)
		}
	}
	return chunk
}

func movementFlatBaseBlock(position core.BlockPos) core.BlockID {
	if position.Y == 0 {
		return core.GrassID
	}
	return core.AirID
}

func loadMovementChunk(t *testing.T, dimension *Dimension, chunk *world.Chunk) {
	t.Helper()
	if !dimension.BeginGeneration(chunk.Pos) {
		t.Fatalf("区块 %+v 未开始生成", chunk.Pos)
	}
	if err := dimension.ApplyGenerated(chunk.Pos, chunk); err != nil {
		t.Fatal(err)
	}
}
