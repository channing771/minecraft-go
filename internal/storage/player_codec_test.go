package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

var updateStorageFixtures = flag.Bool(
	"update-storage-fixtures", false, "rewrite committed storage fixtures",
)

func fixturePlayerID() core.PlayerID {
	return core.PlayerID{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x46, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
}

func fixturePlayerSave(id core.PlayerID, revision uint64) PlayerSave {
	safe := PlayerLocation{Dimension: core.Overworld, Position: [3]float32{1.5, 65, -2.5}}
	return PlayerSave{
		PlayerID: id, Revision: revision, DisplayName: "Chen",
		Current: PlayerLocation{Dimension: core.Overworld, Position: [3]float32{2.5, 70, -3.5}},
		Yaw:     1.25, Pitch: -0.5, Safe: &safe, Inventory: fixturePlayerInventory(),
		Health: 13,
		// 三层饥饿状态**全部取非初值**（初值是 20 / core.InitialSaturationMilli / 0）：
		// 任何一个字段在编码、迁移或接线里被漏写，读回来都会落在初值上，
		// 与这里的取值不同，往返与迁移用例因此才承重。饱和 2500 ≤ 12×1000，
		// 满足 validatePlayerDTO 的上界。
		Hunger: 12, SaturationMilli: 2500, ExhaustionMilli: 1750,
	}
}

func fixturePlayerInventory() core.Inventory {
	var inventory core.Inventory
	inventory.Hotbar.Selected = 3
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	ironFull, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	inventory.Hotbar.Slots[4] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}
	inventory.Hotbar.Slots[6] = core.ItemStack{Item: core.ItemGrass, Count: 1}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemDirt, Count: 12}
	inventory.Backpack[7] = core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: ironFull}
	inventory.Backpack[core.BackpackSlots-1] = core.ItemStack{Item: core.ItemStone, Count: 5}
	return inventory
}

func TestPlayerCodecRoundTrip(t *testing.T) {
	if currentPlayerSchema != 7 {
		t.Fatalf("玩家 schema=%d，想要 7", currentPlayerSchema)
	}
	id := fixturePlayerID()
	want := fixturePlayerSave(id, 7)
	encoded, err := encodePlayer(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(id, encoded)
	if err != nil || got.PlayerID != want.PlayerID || got.Revision != want.Revision ||
		got.DisplayName != want.DisplayName || got.Current != want.Current ||
		got.Yaw != want.Yaw || got.Pitch != want.Pitch || got.Safe == nil || *got.Safe != *want.Safe ||
		got.Inventory != want.Inventory || got.Health != want.Health ||
		got.Hunger != want.Hunger || got.SaturationMilli != want.SaturationMilli ||
		got.ExhaustionMilli != want.ExhaustionMilli {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if got.NeedsRewrite {
		t.Fatal("v7 玩家意外需要重写")
	}
	got.Safe.Position[0] = 99
	if want.Safe.Position[0] == 99 {
		t.Fatal("decoded safe location aliases save")
	}
}

func TestPlayerSchemaV6RoundTripsNewBlockItems(t *testing.T) {
	id := fixturePlayerID()
	want := fixturePlayerSave(id, 8)
	want.Inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemLightBlock, Count: 17}
	want.Inventory.Backpack[3] = core.ItemStack{
		Item: core.ItemMossyCobblestone, Count: core.MaxStackCount,
	}
	encoded, err := encodePlayer(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(id, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inventory != want.Inventory || got.NeedsRewrite {
		t.Fatalf("新方块物品往返 inventory=%+v needsRewrite=%v", got.Inventory, got.NeedsRewrite)
	}
}

func TestPlayerSchemaV4DecodeKeepsWornDurability(t *testing.T) {
	save := fixturePlayerSave(fixturePlayerID(), 7)
	save.Inventory.Hotbar.Slots[4].Durability = 73
	save.Inventory.Backpack[7].Durability = 149

	encoded, err := encodePlayer(save)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(save.PlayerID, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inventory != save.Inventory {
		t.Fatalf("v4 磨损工具往返后 inventory=%+v，想要 %+v", got.Inventory, save.Inventory)
	}
}

// TestPlayerV4FixtureMigratesToFullHealth 把冻结的 v4 存档（没有生命值字段）
// 当作迁移输入：物品状态必须无损，生命值必须迁移为满血，且必须标记为需要重写。
func TestPlayerV4FixtureMigratesToFullHealth(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("testdata", "player-v4.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(fixturePlayerID(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inventory != fixturePlayerInventory() {
		t.Fatalf("v4 迁移改变了物品状态: %+v", got.Inventory)
	}
	if got.Health != core.MaxHealth {
		t.Fatalf("v4 迁移生命值 = %d，想要满血 %d", got.Health, core.MaxHealth)
	}
	if !got.NeedsRewrite {
		t.Fatal("v4 玩家必须标记为需要重写")
	}
}

// TestPlayerV5FixtureMigratesLosslessly 冻结 v5 负载布局，并验证 v6 identity migration。
func TestPlayerV5FixtureMigratesLosslessly(t *testing.T) {
	path := filepath.Join("testdata", "player-v5.bin")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := fixturePlayerSave(fixturePlayerID(), 19)
	got, err := decodePlayer(want.PlayerID, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.PlayerID != want.PlayerID || got.Revision != want.Revision ||
		got.DisplayName != want.DisplayName || got.Current != want.Current ||
		got.Yaw != want.Yaw || got.Pitch != want.Pitch || got.Safe == nil || *got.Safe != *want.Safe ||
		got.Inventory != want.Inventory || got.Health != want.Health || !got.NeedsRewrite {
		t.Fatalf("v5 identity migration = %+v", got)
	}
	// v5 同样没有饥饿字段：迁移链 v5→v6→v7 必须走到 v6 那一步的初值填充。
	assertMigratedToInitialHunger(t, "v5", got)
}

// assertMigratedToInitialHunger 断言一份迁移自 v7 之前 schema 的存档拿到了固定的
// 饥饿初值。它是"旧存档按初值迁移"这条 Scenario 的共用断言。
func assertMigratedToInitialHunger(t *testing.T, schema string, got StoredPlayer) {
	t.Helper()
	if got.Hunger != core.MaxHunger || got.SaturationMilli != core.InitialSaturationMilli ||
		got.ExhaustionMilli != 0 {
		t.Fatalf("%s 迁移后三层饥饿状态 = (%d, %d, %d)，想要初值 (%d, %d, 0)",
			schema, got.Hunger, got.SaturationMilli, got.ExhaustionMilli,
			core.MaxHunger, core.InitialSaturationMilli)
	}
}

// TestPlayerV6FixtureMigratesToInitialHunger 覆盖 Scenario「旧存档按初值迁移」。
//
// 输入是**冻结的 v6 字节**（testdata/player-v6.bin，本变更一字不改），不是当前
// 编码器现场生成的负载：当前编码器已经写 v7，用它"生成 v6"只会得到一份带饥饿
// 字段的 v7 记录，迁移分支根本不会被执行，用例会全绿而什么都没测。
//
// 断言分两半：三层饥饿必须是初值；**其余字段逐字段不变**（身份、修订号、昵称、
// 位置、朝向、安全点、背包与耐久、生命值），迁移不得顺手改动任何既有状态。
func TestPlayerV6FixtureMigratesToInitialHunger(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("testdata", "player-v6.bin"))
	if err != nil {
		t.Fatal(err)
	}
	want := fixturePlayerSave(fixturePlayerID(), 19)
	got, err := decodePlayer(want.PlayerID, encoded)
	if err != nil {
		t.Fatal(err)
	}
	assertMigratedToInitialHunger(t, "v6", got)
	if got.PlayerID != want.PlayerID {
		t.Fatalf("v6 迁移后身份 = %v，想要 %v", got.PlayerID, want.PlayerID)
	}
	if got.Revision != want.Revision {
		t.Fatalf("v6 迁移后修订号 = %d，想要 %d", got.Revision, want.Revision)
	}
	if got.DisplayName != want.DisplayName {
		t.Fatalf("v6 迁移后昵称 = %q，想要 %q", got.DisplayName, want.DisplayName)
	}
	if got.Current != want.Current {
		t.Fatalf("v6 迁移后位置 = %+v，想要 %+v", got.Current, want.Current)
	}
	if got.Yaw != want.Yaw || got.Pitch != want.Pitch {
		t.Fatalf("v6 迁移后朝向 = (%v, %v)，想要 (%v, %v)",
			got.Yaw, got.Pitch, want.Yaw, want.Pitch)
	}
	if got.Safe == nil || *got.Safe != *want.Safe {
		t.Fatalf("v6 迁移后安全点 = %+v，想要 %+v", got.Safe, want.Safe)
	}
	if got.Inventory != want.Inventory {
		t.Fatalf("v6 迁移后物品状态 = %+v，想要 %+v", got.Inventory, want.Inventory)
	}
	if got.Health != want.Health {
		t.Fatalf("v6 迁移后生命值 = %d，想要 %d", got.Health, want.Health)
	}
	if !got.NeedsRewrite {
		t.Fatal("v6 玩家必须标记为需要重写")
	}
}

// TestPlayerV7Fixture 冻结当前 schema 的编码结果，防止字节布局无声漂移。
//
// 冻结的 v6 golden（testdata/player-v6.bin）刻意保留在原处不再生成：它是
// "旧存档仍然可读"的唯一真实证据，见 TestPlayerV6FixtureMigratesToInitialHunger。
func TestPlayerV7Fixture(t *testing.T) {
	encoded, err := encodePlayer(fixturePlayerSave(fixturePlayerID(), 19))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "player-v7.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatal("v7 fixture drift; change schema version")
	}
}

func TestPlayerV3FixtureMigratesLosslessly(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("testdata", "player-v3.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(fixturePlayerID(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Inventory != fixturePlayerInventory() {
		t.Fatalf("v3 迁移改变了物品状态: %+v", got.Inventory)
	}
	if got.Health != core.MaxHealth {
		t.Fatalf("v3 迁移生命值 = %d，想要满血 %d", got.Health, core.MaxHealth)
	}
	if !got.NeedsRewrite {
		t.Fatal("v3 玩家必须标记为需要重写")
	}
}

func TestPlayerV1FixtureMigratesToEmptyHotbar(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("testdata", "player-v1.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodePlayer(fixturePlayerID(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := fixturePlayerSave(fixturePlayerID(), 19)
	if got.DisplayName != want.DisplayName || got.Current != want.Current ||
		got.Yaw != want.Yaw || got.Pitch != want.Pitch ||
		got.Safe == nil || *got.Safe != *want.Safe {
		t.Fatalf("v1 迁移改变了既有字段: %+v", got)
	}
	if got.Inventory != (core.Inventory{}) {
		t.Fatalf("v1 迁移物品状态 = %+v，想要空快捷栏与空背包", got.Inventory)
	}
	if got.Health != core.MaxHealth {
		t.Fatalf("v1 迁移生命值 = %d，想要满血 %d", got.Health, core.MaxHealth)
	}
	if !got.NeedsRewrite {
		t.Fatal("v1 存档必须标记为需要重写")
	}
}

func TestPlayerCodecRejectsInvalidHotbarPayload(t *testing.T) {
	id := fixturePlayerID()
	invalid := []struct {
		name   string
		mutate func(*core.Hotbar)
	}{
		{"选中栏位越界", func(h *core.Hotbar) { h.Selected = core.HotbarSlots }},
		{"数量超过上限", func(h *core.Hotbar) {
			h.Slots[1] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount + 1}
		}},
		// M4E 二进制的注册范围止于 9，因此同样会拒绝 ID 10/11；
		// 这里保留真正未知的 4242，不能复制一份冻结的旧 decoder。
		{"未知物品", func(h *core.Hotbar) {
			h.Slots[2] = core.ItemStack{Item: core.ItemID(4242), Count: 1}
		}},
		{"空物品非零数量", func(h *core.Hotbar) {
			h.Slots[3] = core.ItemStack{Item: core.ItemNone, Count: 5}
		}},
		{"非工具携带耐久", func(h *core.Hotbar) {
			h.Slots[5] = core.ItemStack{Item: core.ItemStone, Count: 1, Durability: 1}
		}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			save := fixturePlayerSave(id, 3)
			tc.mutate(&save.Inventory.Hotbar)
			if _, err := encodePlayer(save); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("encode error = %v，想要 ErrCorrupt", err)
			}
			encoded := playerWireWithHotbar(t, id, save.Inventory.Hotbar)
			if _, err := decodePlayer(id, encoded); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("decode error = %v，想要 ErrCorrupt", err)
			}
		})
	}
}

// playerWireWithHotbar 用合法存档换掉快捷栏负载并修正 CRC，绕过编码器校验。
func playerWireWithHotbar(t *testing.T, id core.PlayerID, hotbar core.Hotbar) []byte {
	t.Helper()
	encoded, err := encodePlayer(fixturePlayerSave(id, 3))
	if err != nil {
		t.Fatal(err)
	}
	wire := bytes.Clone(encoded)
	// v5 起负载在快捷栏/背包之后追加了 1 字节生命值，v7 起再追加三层饥饿状态，
	// 从末尾定位快捷栏的偏移量必须跳过这两段尾巴。这里写成具名常量而不是字面
	// 数字：F2 追加 Health 时就有一条 len(payload)-1 的断言静默改指了新字段。
	offset := len(wire) - playerHungerBytes - playerHealthBytes -
		playerBackpackBytes - playerHotbarBytes
	wire[offset] = hotbar.Selected
	offset++
	for _, stack := range hotbar.Slots {
		binary.LittleEndian.PutUint16(wire[offset:], uint16(stack.Item))
		wire[offset+2] = stack.Count
		binary.LittleEndian.PutUint16(wire[offset+3:], stack.Durability)
		offset += 5
	}
	hasher := crc32.New(playerCRCTable)
	_, _ = hasher.Write(wire[8:40])
	_, _ = hasher.Write(wire[playerEnvelopeLength:])
	binary.LittleEndian.PutUint32(wire[40:], hasher.Sum32())
	return wire
}

func TestPlayerCodecRejectsInvalidSave(t *testing.T) {
	valid := fixturePlayerSave(fixturePlayerID(), 1)
	tests := []struct {
		name   string
		mutate func(*PlayerSave)
	}{
		{"invalid player ID", func(save *PlayerSave) { save.PlayerID = core.PlayerID{} }},
		{"zero revision", func(save *PlayerSave) { save.Revision = 0 }},
		{"unnormalized name", func(save *PlayerSave) { save.DisplayName = " Chen " }},
		{"invalid dimension", func(save *PlayerSave) { save.Current.Dimension = 1 }},
		{"nonfinite current position", func(save *PlayerSave) { save.Current.Position[0] = float32(math.Inf(1)) }},
		{"nonfinite yaw", func(save *PlayerSave) { save.Yaw = float32(math.NaN()) }},
		{"nonfinite pitch", func(save *PlayerSave) { save.Pitch = float32(math.Inf(-1)) }},
		{"pitch too high", func(save *PlayerSave) { save.Pitch = float32(math.Pi/2) + 0.01 }},
		{"invalid safe dimension", func(save *PlayerSave) { save.Safe.Dimension = 1 }},
		{"health above max", func(save *PlayerSave) { save.Health = core.MaxHealth + 1 }},
		{"hunger above max", func(save *PlayerSave) { save.Hunger = core.MaxHunger + 1 }},
		{"saturation above hunger", func(save *PlayerSave) {
			save.SaturationMilli = uint16(save.Hunger)*core.SaturationMilliPerPoint + 1
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			save := valid
			if valid.Safe != nil {
				safe := *valid.Safe
				save.Safe = &safe
			}
			tc.mutate(&save)
			if _, err := encodePlayer(save); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("encode error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestPlayerCodecRejectsCorruptEnvelope(t *testing.T) {
	id := fixturePlayerID()
	encoded, err := encodePlayer(fixturePlayerSave(id, 19))
	if err != nil {
		t.Fatal(err)
	}
	badFloat := func(offset int) []byte {
		payload := bytes.Clone(encoded)
		binary.LittleEndian.PutUint32(payload[offset:], math.Float32bits(float32(math.NaN())))
		repairPlayerCRC(payload)
		return payload
	}
	tests := []struct {
		name    string
		payload func() []byte
		want    error
	}{
		{"magic", func() []byte { p := bytes.Clone(encoded); p[0] ^= 1; return p }, ErrCorrupt},
		{"old envelope", func() []byte { p := bytes.Clone(encoded); binary.LittleEndian.PutUint32(p[4:], 0); return p }, ErrCorrupt},
		{"future envelope", func() []byte { p := bytes.Clone(encoded); binary.LittleEndian.PutUint32(p[4:], 2); return p }, ErrFutureVersion},
		{"invalid schema", func() []byte { p := bytes.Clone(encoded); binary.LittleEndian.PutUint32(p[8:], 0); return p }, ErrCorrupt},
		{"future schema", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[8:], currentPlayerSchema+1)
			return p
		}, ErrFutureVersion},
		{"invalid player ID", func() []byte { p := bytes.Clone(encoded); clear(p[12:28]); repairPlayerCRC(p); return p }, ErrCorrupt},
		{"mismatched player ID", func() []byte { p := bytes.Clone(encoded); p[27] ^= 1; repairPlayerCRC(p); return p }, ErrCorrupt},
		{"zero revision", func() []byte { p := bytes.Clone(encoded); clear(p[28:36]); repairPlayerCRC(p); return p }, ErrCorrupt},
		{"payload length mismatch", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[36:], uint32(len(p)))
			return p
		}, ErrCorrupt},
		{"CRC", func() []byte { p := bytes.Clone(encoded); p[40] ^= 1; return p }, ErrCorrupt},
		{"invalid nickname", func() []byte { p := bytes.Clone(encoded); p[48] = '\n'; repairPlayerCRC(p); return p }, ErrCorrupt},
		{"current dimension", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[52:], 1)
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"current x", func() []byte { return badFloat(56) }, ErrCorrupt},
		{"current y", func() []byte { return badFloat(60) }, ErrCorrupt},
		{"current z", func() []byte { return badFloat(64) }, ErrCorrupt},
		{"yaw", func() []byte { return badFloat(68) }, ErrCorrupt},
		{"pitch", func() []byte { return badFloat(72) }, ErrCorrupt},
		{"pitch outside range", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[72:], math.Float32bits(2))
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"safe flag", func() []byte { p := bytes.Clone(encoded); p[76] = 2; repairPlayerCRC(p); return p }, ErrCorrupt},
		{"safe dimension", func() []byte {
			p := bytes.Clone(encoded)
			binary.LittleEndian.PutUint32(p[77:], 1)
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"safe x", func() []byte { return badFloat(81) }, ErrCorrupt},
		{"safe y", func() []byte { return badFloat(85) }, ErrCorrupt},
		{"safe z", func() []byte { return badFloat(89) }, ErrCorrupt},
		{"invalid health", func() []byte {
			p := bytes.Clone(encoded)
			// 生命值不再是末字节：v7 在它之后追加了三层饥饿状态。写成
			// len(p)-playerHungerBytes-1 而不是 len(p)-1，否则这条断言会静默
			// 改指疲劳值高字节，"生命值越界被拒"就不再被任何用例覆盖。
			p[len(p)-playerHungerBytes-playerHealthBytes] = core.MaxHealth + 1
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"invalid hunger", func() []byte {
			p := bytes.Clone(encoded)
			p[len(p)-playerHungerBytes] = core.MaxHunger + 1
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"saturation above hunger", func() []byte {
			p := bytes.Clone(encoded)
			// 饱和度紧随饥饿值，取 hunger×1000 + 1 恰好越过上界一个千分位。
			binary.LittleEndian.PutUint16(
				p[len(p)-playerHungerBytes+1:],
				uint16(p[len(p)-playerHungerBytes])*core.SaturationMilliPerPoint+1,
			)
			repairPlayerCRC(p)
			return p
		}, ErrCorrupt},
		{"trailing byte", func() []byte { return append(bytes.Clone(encoded), 0) }, ErrCorrupt},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodePlayer(id, tc.payload())
			if !errors.Is(err, tc.want) {
				t.Fatalf("decode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPlayerCodecRejectsPayloadOverLimitBeforeAllocation(t *testing.T) {
	id := fixturePlayerID()
	payload := make([]byte, playerEnvelopeLength)
	copy(payload, "MCPL")
	binary.LittleEndian.PutUint32(payload[4:], playerEnvelopeVersion)
	binary.LittleEndian.PutUint32(payload[8:], currentPlayerSchema)
	copy(payload[12:28], id[:])
	binary.LittleEndian.PutUint64(payload[28:], 1)
	binary.LittleEndian.PutUint32(payload[36:], maxPlayerPayload+1)
	if _, err := decodePlayer(id, payload); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("decode error = %v, want ErrCorrupt", err)
	}
}

func repairPlayerCRC(payload []byte) {
	hasher := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	_, _ = hasher.Write(payload[8:40])
	_, _ = hasher.Write(payload[playerEnvelopeLength:])
	binary.LittleEndian.PutUint32(payload[40:], hasher.Sum32())
}
