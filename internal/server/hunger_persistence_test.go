package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

// 落盘用的三层饥饿状态夹具：**三个字段全部取非初值**（初值是 20 /
// core.InitialSaturationMilli / 0）。任何一个字段在保存或恢复路径上被漏写，
// 读回来都会落在初值上；三个全取初值的夹具下，"写了"与"没写"读数完全相同，
// 用例会全绿而什么都没测。饱和 2500 ≤ 12×1000，满足存档编码的上界校验。
const (
	savedHunger          uint8  = 12
	savedSaturationMilli uint16 = 2500
	savedExhaustionMilli uint16 = 1750
)

// hungerFlushDeadline 给落盘等待一个上界。Flush 在"缓存快照与写出的存档永远
// 不相等"时会一直重派保存（matchesSave 恒假 ⇒ 恒脏），而保存路径漏写任何一个
// 饥饿字段就正是这种状态：没有上界的话用例不是变红而是挂死，要等包级 timeout
// 才收场。一次临时目录写盘是毫秒量级，10 秒是三个数量级的余量。
const hungerFlushDeadline = 10 * time.Second

// flushHungerPersistence 在有界上下文里把脏玩家写盘。
func flushHungerPersistence(t *testing.T, persistence *playerPersistence) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hungerFlushDeadline)
	defer cancel()
	if err := persistence.Flush(ctx); err != nil {
		t.Fatalf("落盘: %v", err)
	}
}

// hungerTestSnapshot 在 testPlayerSnapshot 之上带上三层饥饿状态与一个非零生命值。
// 位置、朝向、安全点沿用既有夹具，"其余字段不变"因此有具体内容可比。
func hungerTestSnapshot(hunger uint8, saturation, exhaustion uint16) sim.PlayerSnapshot {
	snapshot := testPlayerSnapshot(10)
	snapshot.Health = 13
	snapshot.Hunger = hunger
	snapshot.SaturationMilli = saturation
	snapshot.ExhaustionMilli = exhaustion
	return snapshot
}

// openHungerDiskStore 打开（必要时创建）一个磁盘世界，并登记关闭。
func openHungerDiskStore(t *testing.T, root string, create bool) *storage.DiskStore {
	t.Helper()
	options := storage.OpenOptions{}
	if create {
		options.Create = storage.Metadata{
			FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
		}
	}
	store, err := storage.OpenDisk(context.Background(), root, options)
	if err != nil {
		t.Fatalf("打开磁盘世界: %v", err)
	}
	return store
}

// seedHungerPlayer 把一份玩家存档写进磁盘世界。载荷由生产路径的 save() 构造，
// 因此"播种的快照"与"重连时缓存到的快照"逐字段相同——用例断言的是读回来的值，
// 不是这一步写进去的值。
func seedHungerPlayer(
	t *testing.T,
	root string,
	id core.PlayerID,
	name string,
	snapshot sim.PlayerSnapshot,
) {
	t.Helper()
	store := openHungerDiskStore(t, root, true)
	player := &cachedPlayer{id: id, name: name, hasSnapshot: true, snapshot: snapshot}
	if _, err := store.SavePlayer(context.Background(), player.save(1)); err != nil {
		_ = store.Close()
		t.Fatalf("播种玩家存档: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭播种世界: %v", err)
	}
}

// assertRestoreHunger 断言一份 sim.PlayerRestore 的三层饥饿状态，并要求它确实
// 来自存档（HasHunger 为真）——否则 sim 会忽略这三个字段改用初值。
func assertRestoreHunger(
	t *testing.T,
	what string,
	restore sim.PlayerRestore,
	hunger uint8,
	saturation, exhaustion uint16,
) {
	t.Helper()
	if !restore.HasHunger {
		t.Fatalf("%s：PlayerRestore.HasHunger = false，存档里的饥饿状态不会生效", what)
	}
	if restore.Hunger != hunger || restore.SaturationMilli != saturation ||
		restore.ExhaustionMilli != exhaustion {
		t.Fatalf("%s 恢复的三层饥饿状态 = (%d, %d, %d)，想要 (%d, %d, %d)",
			what, restore.Hunger, restore.SaturationMilli, restore.ExhaustionMilli,
			hunger, saturation, exhaustion)
	}
}

// TestHungerSurvivesDiskRestart 覆盖 Scenario「饥饿状态跨重启保留」的服务端一半：
// 权威快照里的三层饥饿状态经 save() → 玩家 schema v7 字节 → 真实磁盘 → LoadPlayer
// → restore() 之后必须逐字段原值返回。
//
// 三个字段全部取非初值，且落盘前后各断言一次：只断言重连读数会漏掉"保存路径
// 写对了但恢复路径没接"与"恢复接了但保存漏写"这两类改法中的一半。
func TestHungerSurvivesDiskRestart(t *testing.T) {
	root := t.TempDir()
	id := playerID(0x71)
	const name = "Hungry"
	ctx := context.Background()
	seedHungerPlayer(t, root, id, name,
		hungerTestSnapshot(core.MaxHunger, core.InitialSaturationMilli, 0))

	// 第一次开服：观察到一份三层全非初值的权威快照并落盘。
	store := openHungerDiskStore(t, root, false)
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	if _, err := persistence.Prepare(ctx, id, name, testMetadata()); err != nil {
		t.Fatalf("首次登录: %v", err)
	}
	want := hungerTestSnapshot(savedHunger, savedSaturationMilli, savedExhaustionMilli)
	if err := persistence.Observe(id, name, want, 0, false); err != nil {
		t.Fatalf("观察权威快照: %v", err)
	}
	flushHungerPersistence(t, persistence)
	persistence.CloseWorker()
	if err := store.Close(); err != nil {
		t.Fatalf("关闭世界: %v", err)
	}

	// 落盘字节本身必须带上三层状态：这一步把"写盘漏字段"与"读盘漏字段"分开。
	stored := loadStoredPlayerForTest(t, root, id)
	if stored.Hunger != savedHunger || stored.SaturationMilli != savedSaturationMilli ||
		stored.ExhaustionMilli != savedExhaustionMilli {
		t.Fatalf("落盘的三层饥饿状态 = (%d, %d, %d)，想要 (%d, %d, %d)",
			stored.Hunger, stored.SaturationMilli, stored.ExhaustionMilli,
			savedHunger, savedSaturationMilli, savedExhaustionMilli)
	}

	// 重启：同一份磁盘世界重开并重连，三层状态必须原值恢复。
	restarted := openHungerDiskStore(t, root, false)
	defer func() {
		if err := restarted.Close(); err != nil {
			t.Fatalf("关闭重启后的世界: %v", err)
		}
	}()
	reopened := newPlayerPersistence(restarted, playerPersistenceTestConfig())
	defer reopened.CloseWorker()
	restore, err := reopened.Prepare(ctx, id, name, testMetadata())
	if err != nil {
		t.Fatalf("重连: %v", err)
	}
	assertRestoreHunger(t, "重启后", restore,
		savedHunger, savedSaturationMilli, savedExhaustionMilli)
	// 其余字段同样必须原值恢复：饥饿接线不得挤掉既有状态。
	assertRestoreMatchesSnapshot(t, "重启后", restore, want)
}

// assertRestoreMatchesSnapshot 逐字段比较恢复结果与落盘前的权威快照（饥饿之外
// 的部分）：位置、朝向、安全点、背包与生命值。
func assertRestoreMatchesSnapshot(
	t *testing.T,
	what string,
	restore sim.PlayerRestore,
	want sim.PlayerSnapshot,
) {
	t.Helper()
	if restore.Current == nil || *restore.Current != want.Current {
		t.Fatalf("%s 恢复的位置 = %+v，想要 %+v", what, restore.Current, want.Current)
	}
	if restore.Safe == nil || want.Safe == nil || *restore.Safe != *want.Safe {
		t.Fatalf("%s 恢复的安全点 = %+v，想要 %+v", what, restore.Safe, want.Safe)
	}
	if restore.Yaw != want.Yaw || restore.Pitch != want.Pitch {
		t.Fatalf("%s 恢复的朝向 = (%v, %v)，想要 (%v, %v)",
			what, restore.Yaw, restore.Pitch, want.Yaw, want.Pitch)
	}
	if restore.Inventory != want.Inventory {
		t.Fatalf("%s 恢复的物品状态 = %+v，想要 %+v", what, restore.Inventory, want.Inventory)
	}
	if restore.Health != want.Health {
		t.Fatalf("%s 恢复的生命值 = %d，想要 %d", what, restore.Health, want.Health)
	}
}

// TestLegacyPlayerFileMigratesToInitialHunger 覆盖 Scenario「旧存档按初值迁移」的
// 服务端一半：一份**冻结的 v6 玩家存档文件**放进磁盘世界，登录拿到的
// sim.PlayerRestore 必须带上固定初值，其余字段一个不变。
//
// 输入是 internal/storage/testdata/player-v6.bin 的原始字节，不是当前编码器
// 现场生成的存档：当前编码器已经写 v7，用它"生成 v6"只会得到一份带饥饿字段的
// v7 记录，迁移分支根本不会被执行。该 golden 是冻结的，字段取值不会漂移。
func TestLegacyPlayerFileMigratesToInitialHunger(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	if err := openHungerDiskStore(t, root, true).Close(); err != nil {
		t.Fatalf("创建磁盘世界: %v", err)
	}
	encoded, err := os.ReadFile(
		filepath.Join("..", "storage", "testdata", "player-v6.bin"),
	)
	if err != nil {
		t.Fatalf("读取 v6 golden: %v", err)
	}
	// MCPL 信封布局：magic 4 + 信封版本 4 + schema 4 + PlayerID 16。
	var id core.PlayerID
	if len(encoded) < 12+len(id) {
		t.Fatalf("v6 golden 只有 %d 字节，短于信封", len(encoded))
	}
	copy(id[:], encoded[12:12+len(id)])
	players := filepath.Join(root, "players")
	if err := os.MkdirAll(players, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(players, id.String()+".player"), encoded, 0o644,
	); err != nil {
		t.Fatal(err)
	}

	stored := loadStoredPlayerForTest(t, root, id)
	// 夹具自证：v6 golden 必须真的被解出来了（生命值 13 是 storage 夹具的取值，
	// 不是任何默认值），否则下面的"初值"断言在一份空存档上也会通过。
	if stored.Health != 13 || !stored.NeedsRewrite {
		t.Fatalf("v6 golden 解码结果 = %+v，想要生命值 13 且标记重写", stored)
	}

	store := openHungerDiskStore(t, root, false)
	defer func() {
		if err := store.Close(); err != nil {
			t.Fatalf("关闭世界: %v", err)
		}
	}()
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	defer persistence.CloseWorker()
	restore, err := persistence.Prepare(ctx, id, stored.DisplayName, testMetadata())
	if err != nil {
		t.Fatalf("旧存档登录: %v", err)
	}
	assertRestoreHunger(t, "v6 存档", restore, core.MaxHunger, core.InitialSaturationMilli, 0)
	// 其余字段逐字段不变：迁移与接线都不得顺手改动既有状态。
	assertRestoreMatchesSnapshot(t, "v6 存档", restore, sim.PlayerSnapshot{
		Current: sim.PlayerLocation{
			Dimension: stored.Current.Dimension,
			Position:  mgl32.Vec3(stored.Current.Position),
		},
		Safe: &sim.PlayerLocation{
			Dimension: stored.Safe.Dimension,
			Position:  mgl32.Vec3(stored.Safe.Position),
		},
		Yaw: stored.Yaw, Pitch: stored.Pitch,
		Inventory: stored.Inventory, Health: stored.Health,
	})
}

// TestRespawnHungerResetIsPersisted 覆盖 Scenario「重生后饥饿回满」的服务端一半：
// 死亡前的三层状态已经落盘，重生把它们置回初值之后，这次**只有饥饿变化**的
// 快照必须被判为脏并写回磁盘。
//
// 除饥饿外的字段刻意与存档完全相同：变更检测漏掉任意一层，这次重生就会被当成
// "无变化"，磁盘上永远留着死亡前的饥饿状态。
func TestRespawnHungerResetIsPersisted(t *testing.T) {
	root := t.TempDir()
	id := playerID(0x72)
	const name = "Reborn"
	ctx := context.Background()
	dying := hungerTestSnapshot(3, 0, 3000)
	seedHungerPlayer(t, root, id, name, dying)

	store := openHungerDiskStore(t, root, false)
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	restore, err := persistence.Prepare(ctx, id, name, testMetadata())
	if err != nil {
		t.Fatalf("登录: %v", err)
	}
	// 夹具自证：死亡前的状态确实非初值。
	assertRestoreHunger(t, "死亡前", restore, 3, 0, 3000)

	respawned := dying
	respawned.Hunger = core.MaxHunger
	respawned.SaturationMilli = core.InitialSaturationMilli
	respawned.ExhaustionMilli = 0
	if err := persistence.Observe(id, name, respawned, 0, false); err != nil {
		t.Fatalf("观察重生后的权威快照: %v", err)
	}
	flushHungerPersistence(t, persistence)
	persistence.CloseWorker()
	if err := store.Close(); err != nil {
		t.Fatalf("关闭世界: %v", err)
	}

	stored := loadStoredPlayerForTest(t, root, id)
	if stored.Hunger != core.MaxHunger ||
		stored.SaturationMilli != core.InitialSaturationMilli ||
		stored.ExhaustionMilli != 0 {
		t.Fatalf("重生后落盘的三层饥饿状态 = (%d, %d, %d)，想要初值 (%d, %d, 0)",
			stored.Hunger, stored.SaturationMilli, stored.ExhaustionMilli,
			core.MaxHunger, core.InitialSaturationMilli)
	}
}

// TestPlayerPersistenceDirtyDetectionIncludesHunger 覆盖"只有饥饿变化也必须被
// 持久化"：三层状态是玩家原地不动时唯一会自行变化的权威状态，快照比较与存档
// 比较都必须把它们**逐个**算进去。
//
// 逐字段单独改：三个字段一起改的用例只能证明"至少有一个参与了比较"。
func TestPlayerPersistenceDirtyDetectionIncludesHunger(t *testing.T) {
	full := hungerTestSnapshot(core.MaxHunger, core.InitialSaturationMilli, 0)
	cases := []struct {
		name  string
		apply func(*sim.PlayerSnapshot)
	}{
		{"饥饿", func(s *sim.PlayerSnapshot) { s.Hunger = savedHunger }},
		{"饱和", func(s *sim.PlayerSnapshot) { s.SaturationMilli = savedSaturationMilli }},
		{"疲劳", func(s *sim.PlayerSnapshot) { s.ExhaustionMilli = savedExhaustionMilli }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			changed := full
			testCase.apply(&changed)
			if playerSnapshotsEqual(full, changed) {
				t.Fatalf("快照比较忽略了%s", testCase.name)
			}
			player := &cachedPlayer{hasSnapshot: true, snapshot: full}
			save := player.save(1)
			if !player.matchesSave(save) {
				t.Fatal("同值存档必须匹配缓存快照")
			}
			player.snapshot = changed
			if player.matchesSave(save) {
				t.Fatalf("存档比较忽略了%s", testCase.name)
			}
		})
	}
}
