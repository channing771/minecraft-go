// companions.ai schema v4 的最近对话摘要持久化测试：v4 记录布局（v3 记录 +
// 可选摘要区）round-trip 与 golden、摘要 2,048/2,049 边界与空摘要语义（空串
// 不写摘要区、纯空摘要队列即空队列）、损坏矩阵（非法摘要长度前缀/零长摘要区/
// NUL/非法 UTF-8/变长错位/v5 未来版本/v3 保留位）、438,280 文件上界（含
// 4×2,050 摘要区推导）、v3/v2/v1 只读迁移摘要为空与首存 v4、inactive 去激活
// 丢弃摘要。全部用例不触盘（除显式 DiskStore 用例），失败注入均为字节级或
// 载荷级构造。
package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

// fixtureCompanionSummary 是 v4 golden 中 active 记录携带的最近对话摘要文本：
// 纯 UTF-8 中文（每 rune 3 bytes），锁定摘要区的逐字节落盘。
const fixtureCompanionSummary = "阿木记得玩家常在橡树旁停留，上次一起修好了北边的小路。"

// fixtureCompanionV4Queues 构造 v4 代表载荷：字节序最小的 active 记录（..01）
// 携带 v3 四 kind 任务、满 16 条 FIFO 与最近对话摘要；另一条 active 记录
// （..02）只有 FIFO、无摘要——锁定「active 无摘要记录不写摘要区」，其字节
// 位形与 v3 记录完全一致。去激活/未对话的记录不提供队列载荷（inactive 记录
// 在磁盘上即无摘要区）。
func fixtureCompanionV4Queues() []StoredCompanionQueue {
	queues := cloneStoredQueuesForTest(fixtureCompanionV3Queues())
	queues[0].Summary = fixtureCompanionSummary
	queues = append(queues, StoredCompanionQueue{
		ID:      fixtureCompanionID(2),
		Pending: []string{"v4仅排队甲", "v4仅排队乙"},
	})
	return queues
}

// TestCompanionCodecV4RoundTripAndGolden 锁定 schema v4 编码：current schema 与
// 文件上界常量、带摘要/无摘要 active 记录混排的 round-trip、golden 字节稳定
// （-update-storage-fixtures 重生成）与解码结果对输入字节的独立性。
func TestCompanionCodecV4RoundTripAndGolden(t *testing.T) {
	if currentCompanionSchema != 4 {
		t.Fatalf("current companion schema=%d，想要 4", currentCompanionSchema)
	}
	if maxCompanionFileLength != 438280 {
		t.Fatalf("max companion file length=%d，想要 438280", maxCompanionFileLength)
	}
	input := fixtureCompanionBodies()
	queues := fixtureCompanionV4Queues()
	bodiesSnapshot := append([]companion.Body(nil), input...)
	queuesSnapshot := cloneStoredQueuesForTest(queues)
	encoded, err := encodeCompanions(CompanionSave{Revision: 47, Records: input, Queues: queues})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, bodiesSnapshot) || !reflect.DeepEqual(queues, queuesSnapshot) {
		t.Fatalf("编码修改调用者载荷：records=%+v queues=%+v", input, queues)
	}
	if schema := binary.LittleEndian.Uint32(encoded[8:12]); schema != 4 {
		t.Fatalf("schema=%d，想要 4", schema)
	}

	// 文件总长按第一性原理核算：envelope 32 + 记录 ..01（221 身体 + 1 flags +
	// v3 任务区 + FIFO 区 + 摘要区 2+摘要字节）+ 记录 ..02（221 身体 + 1
	// flags + FIFO 区，无摘要区——active 无摘要不写摘要区，位形与 v3 对齐）。
	taskArea := 2 + len("去橡树旁挖一格垫一块再跟着我") + 2 + (13 + 13 + 15 + 17) + 4 + 1 + 1 + 8 + 8
	fifoAreaFirst := 2
	for index := range queuesSnapshot[0].Pending {
		fifoAreaFirst += 2 + len(queuesSnapshot[0].Pending[index])
	}
	fifoAreaSecond := 2 + (2 + len("v4仅排队甲")) + (2 + len("v4仅排队乙"))
	wantLength := 32 + (companionRecordLength + 1 + taskArea + fifoAreaFirst +
		companionSummaryPrefixLength + len(fixtureCompanionSummary)) +
		(companionRecordLength + 1 + fifoAreaSecond)
	if len(encoded) != wantLength {
		t.Fatalf("v4 文件长度=%d，想要 %d", len(encoded), wantLength)
	}

	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := []companion.Body{input[1], input[0]}
	if got.Revision != 47 || !reflect.DeepEqual(got.Records, wantRecords) {
		t.Fatalf("decode revision=%d records=%+v，想要 47/%+v", got.Revision, got.Records, wantRecords)
	}
	if !reflect.DeepEqual(got.Queues, queuesSnapshot) {
		t.Fatalf("decode queues=%+v，想要 %+v", got.Queues, queuesSnapshot)
	}
	if got.Queues[0].Summary != fixtureCompanionSummary || got.Queues[1].Summary != "" {
		t.Fatalf("摘要往返=%q/%q，想要精确保留与空摘要", got.Queues[0].Summary, got.Queues[1].Summary)
	}

	path := filepath.Join("testdata", "companions-v4.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if schema := binary.LittleEndian.Uint32(want[8:12]); schema != 4 {
		t.Fatalf("v4 golden schema=%d，想要 4（golden 字节冻结）", schema)
	}
	if !bytes.Equal(want, encoded) {
		t.Fatal("companions v4 fixture drift；需要显式 -update-storage-fixtures 重生成并评审字节")
	}
	clear(encoded)
	if !reflect.DeepEqual(got.Queues, queuesSnapshot) {
		t.Fatalf("修改输入 bytes 后 decode 结果=%+v，想要保持 %+v", got.Queues, queuesSnapshot)
	}
}

// TestCompanionCodecV4SummaryBoundariesAndEmptySemantics 锁定摘要的载荷边界与
// 空串语义：2,048 bytes 合法、2,049 拒绝；空摘要不写摘要区（文件与无摘要
// 载荷同长）；只有空摘要的队列即空队列拒绝；NUL 与非法 UTF-8 在编码边界
// 拒绝。
func TestCompanionCodecV4SummaryBoundariesAndEmptySemantics(t *testing.T) {
	maxSummary := strings.Repeat("木", 682) + "ab" // 682×3 + 2 = 2,048 bytes
	if len(maxSummary) != MaxCompanionSummaryBytes {
		t.Fatalf("构造的边界摘要长度=%d，想要 %d", len(maxSummary), MaxCompanionSummaryBytes)
	}
	queue := StoredCompanionQueue{ID: fixtureCompanionID(2), Pending: []string{"排队"}, Summary: maxSummary}
	encoded, err := encodeCompanions(CompanionSave{
		Revision: 11,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{queue},
	})
	if err != nil {
		t.Fatalf("2,048-byte 摘要 encode error=%v，想要成功", err)
	}
	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Queues[0].Summary != maxSummary {
		t.Fatalf("上界摘要往返=%d bytes，想要 %d", len(got.Queues[0].Summary), len(maxSummary))
	}

	over := queue
	over.Summary = maxSummary + "c"
	if _, err := encodeCompanions(CompanionSave{
		Revision: 12,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{over},
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("2,049-byte 摘要 encode error=%v，想要 ErrCorrupt", err)
	}

	// 空摘要语义：空串等价于「无摘要」，不写摘要区——文件长度与 v3 位形
	// 一致（仅 flags 后跟 FIFO 区）。
	empty := queue
	empty.Summary = ""
	encodedEmpty, err := encodeCompanions(CompanionSave{
		Revision: 13,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{empty},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := 32 + companionRecordLength + 1 + (2 + 2 + len("排队")); len(encodedEmpty) != want {
		t.Fatalf("空摘要文件长度=%d，想要 %d（无摘要区）", len(encodedEmpty), want)
	}
	decodedEmpty, err := decodeCompanions(encodedEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if decodedEmpty.Queues[0].Summary != "" {
		t.Fatalf("空摘要往返=%q，想要空串", decodedEmpty.Queues[0].Summary)
	}

	// 只有空摘要的队列即空队列：空串不构成载荷，按既有空队列规则拒绝。
	if _, err := encodeCompanions(CompanionSave{
		Revision: 14,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{{ID: queue.ID, Summary: ""}},
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("纯空摘要队列 encode error=%v，想要 ErrCorrupt（空队列）", err)
	}

	withNUL := queue
	withNUL.Summary = "记忆\x00注入"
	if _, err := encodeCompanions(CompanionSave{
		Revision: 15,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{withNUL},
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("含 NUL 摘要 encode error=%v，想要 ErrCorrupt", err)
	}

	invalidUTF8 := queue
	invalidUTF8.Summary = "记忆\xff碎片"
	if _, err := encodeCompanions(CompanionSave{
		Revision: 16,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{invalidUTF8},
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("非法 UTF-8 摘要 encode error=%v，想要 ErrCorrupt", err)
	}
}

// TestCompanionCodecV4RejectsCorruptSummaryBytes 锁定摘要区的解码侧拒绝矩阵：
// 非法摘要长度前缀（超过 2,048、零长、缩小错位、放大截断）、摘要字节含
// NUL、非法 UTF-8、未来 schema v5 与 v3 文件的摘要保留位。全部种子修复
// CRC，迫使解码深入摘要区校验。
func TestCompanionCodecV4RejectsCorruptSummaryBytes(t *testing.T) {
	// 单记录 summary-only 载荷：flags 仅摘要位，摘要区紧随 flags 之后，
	// 偏移可按第一性原理稳定推导（32 envelope + 221 身体 + 1 flags）。
	queue := StoredCompanionQueue{ID: fixtureCompanionID(2), Summary: "summarytext"}
	valid, err := encodeCompanions(CompanionSave{
		Revision: 21,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{queue},
	})
	if err != nil {
		t.Fatal(err)
	}
	summaryPrefixOffset := companionHeaderLength + companionRecordLength + 1
	tests := []struct {
		name   string
		mutate func([]byte)
		want   error
	}{
		{"summary length exceeds limit", func(payload []byte) {
			binary.LittleEndian.PutUint16(payload[summaryPrefixOffset:], MaxCompanionSummaryBytes+1)
		}, ErrCorrupt},
		{"summary length zero", func(payload []byte) {
			binary.LittleEndian.PutUint16(payload[summaryPrefixOffset:], 0)
		}, ErrCorrupt},
		// 缩小摘要长度：文本仍合法，但 payload 内残留 1 byte 未消费——
		// 变长错位必须被拒绝，不得静默接受非规范字节。
		{"summary length shrunk misaligns", func(payload []byte) {
			binary.LittleEndian.PutUint16(payload[summaryPrefixOffset:], uint16(len(queue.Summary)-1))
		}, ErrCorrupt},
		// 放大摘要长度（仍在 2,048 内）：take 越过文件末尾，按截断拒绝。
		{"summary length grown truncates", func(payload []byte) {
			binary.LittleEndian.PutUint16(payload[summaryPrefixOffset:], uint16(len(queue.Summary)+1))
		}, ErrCorrupt},
		{"summary contains NUL", func(payload []byte) {
			payload[summaryPrefixOffset+2] = 0
		}, ErrCorrupt},
		{"summary invalid UTF-8", func(payload []byte) {
			payload[summaryPrefixOffset+2] = 0xff
		}, ErrCorrupt},
		{"future schema v5", func(payload []byte) {
			binary.LittleEndian.PutUint32(payload[8:], currentCompanionSchema+1)
		}, ErrFutureVersion},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Clone(valid)
			tc.mutate(payload)
			if tc.name != "future schema v5" {
				repairCompanionCRC(payload)
			}
			if _, err := decodeCompanions(payload); !errors.Is(err, tc.want) {
				t.Fatalf("decode error=%v，想要 %v", err, tc.want)
			}
		})
	}

	// v3 文件的摘要位（bit2）是保留位：v3 布局没有摘要区，置位即损坏。
	v3Golden, err := os.ReadFile(filepath.Join("testdata", "companions-v3.bin"))
	if err != nil {
		t.Fatal(err)
	}
	reserved := bytes.Clone(v3Golden)
	reserved[companionHeaderLength+companionRecordLength] |= companionFlagHasSummary
	repairCompanionCRC(reserved)
	if _, err := decodeCompanions(reserved); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("v3 摘要保留位 decode error=%v，想要 ErrCorrupt", err)
	}

	// 超过 438,280 bytes 的文件必须在任何解析与分配之前被拒绝。
	oversized := bytes.Repeat([]byte{0x5a}, maxCompanionFileLength+1)
	if _, err := decodeCompanions(oversized); !errors.Is(err, ErrCorrupt) ||
		!strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("oversized error=%v，想要分配前长度门禁", err)
	}
}

// TestCompanionCodecV4FileBoundIncludesSummary 锁定 v4 文件上界的摘要推导：
// 同一组合法前缀（5 条满 FIFO + 大计划任务、无摘要）必须落在 (430,080,
// 438,280] 内成功落盘——v3 上界会拒绝它；为每条记录追加 2,048-byte 摘要后
// 总长越过 438,280，编码边界必须拒绝而不是写出不可读文件。
func TestCompanionCodecV4FileBoundIncludesSummary(t *testing.T) {
	buildQueues := func(summary string) []StoredCompanionQueue {
		steps := make([]companion.PlanStep, 4662)
		for index := range steps {
			steps[index] = companion.PlanStep{
				Kind: companion.PlanStepPlace, X: int32(index % 64), Y: 64, Z: int32(index / 64),
				Block: core.OakPlanksID,
			}
		}
		steps = append(steps, companion.PlanStep{Kind: companion.PlanStepFollow, PlayerID: fixtureFollowPlayerID()})
		queues := make([]StoredCompanionQueue, 0, 5)
		for record := 0; record < 5; record++ {
			queue := StoredCompanionQueue{
				ID:         fixtureCompanionID(byte(record + 1)),
				HasCurrent: true,
				Current: StoredCompanionTask{
					Command:   strings.Repeat("a", MaxCompanionTaskCommandBytes),
					PlanSteps: steps,
					StepIndex: len(steps) - 1,
					State:     companion.TaskRunning,
					StartTick: 1,
				},
				Pending: make([]string, MaxCompanionFIFOEntries),
				Summary: summary,
			}
			for index := range queue.Pending {
				queue.Pending[index] = strings.Repeat("b", MaxCompanionTaskCommandBytes)
			}
			queues = append(queues, queue)
		}
		return queues
	}
	records := make([]companion.Body, 5)
	for index := range records {
		records[index] = fixtureCompanionBodies()[0]
		records[index].ID = fixtureCompanionID(byte(index + 1))
	}

	withoutSummary, err := encodeCompanions(CompanionSave{
		Revision: 31, Records: records, Queues: buildQueues(""),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(withoutSummary) <= 430080 || len(withoutSummary) > maxCompanionFileLength {
		t.Fatalf("无摘要前缀长度=%d，想要落在 (430080, %d]", len(withoutSummary), maxCompanionFileLength)
	}
	withSummary := buildQueues(strings.Repeat("s", MaxCompanionSummaryBytes))
	if _, err := encodeCompanions(CompanionSave{
		Revision: 32, Records: records, Queues: withSummary,
	}); !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("含摘要超界 encode error=%v，想要 ErrCorrupt 长度门禁", err)
	}
}

// TestCompanionRestoreV4MigrationSummariesEmptyAndFirstSaveV4 锁定 v3/v2/v1
// 只读迁移的摘要语义：旧 schema 文件读入后全部摘要为空，首次保存写出 v4，
// golden 文件本身字节零改动（仍是冻结的迁移输入）。
func TestCompanionRestoreV4MigrationSummariesEmptyAndFirstSaveV4(t *testing.T) {
	for _, schema := range []struct {
		name  string
		file  string
		value uint32
	}{
		{"v3", "companions-v3.bin", 3},
		{"v2", "companions-v2.bin", 2},
		{"v1", "companions-v1.bin", 1},
	} {
		t.Run(schema.name, func(t *testing.T) {
			golden, err := os.ReadFile(filepath.Join("testdata", schema.file))
			if err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(golden[8:12]); got != schema.value {
				t.Fatalf("%s golden schema=%d，想要 %d（字节冻结零改动）", schema.name, got, schema.value)
			}
			decoded, err := decodeCompanions(golden)
			if err != nil {
				t.Fatal(err)
			}
			for _, queue := range decoded.Queues {
				if queue.Summary != "" {
					t.Fatalf("%s 迁移后摘要=%q，想要空", schema.name, queue.Summary)
				}
			}
			encoded, err := encodeCompanions(CompanionSave{
				Revision: decoded.Revision,
				Records:  decoded.Records,
				Queues:   decoded.Queues,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := binary.LittleEndian.Uint32(encoded[8:12]); got != currentCompanionSchema {
				t.Fatalf("%s 首次保存 schema=%d，想要 %d", schema.name, got, currentCompanionSchema)
			}
			reloaded, err := decodeCompanions(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reloaded.Records, decoded.Records) ||
				!reflect.DeepEqual(reloaded.Queues, decoded.Queues) {
				t.Fatalf("%s→v4 重写后载荷漂移：%+v", schema.name, reloaded)
			}
		})
	}

	// v3 golden 的磁盘路径：加载（摘要为空）→ 首存 v4 → 再加载。
	root := t.TempDir()
	store := openCompanionDisk(t, root)
	path := filepath.Join(root, "companions.ai")
	v3Golden, err := os.ReadFile(filepath.Join("testdata", "companions-v3.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, v3Golden, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, queue := range loaded.Queues {
		if queue.Summary != "" {
			t.Fatalf("v3 磁盘迁移摘要=%q，想要空", queue.Summary)
		}
	}
	if err := store.SaveCompanions(context.Background(), CompanionSave{
		Revision: loaded.Revision + 1,
		Records:  loaded.Records,
		Queues:   loaded.Queues,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(after[8:12]); got != currentCompanionSchema {
		t.Fatalf("首次保存 schema=%d，想要 %d", got, currentCompanionSchema)
	}
	reloaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != loaded.Revision+1 ||
		!reflect.DeepEqual(reloaded.Queues, loaded.Queues) {
		t.Fatalf("v3→v4 磁盘重写后=%+v，想要载荷保持", reloaded)
	}
}

// TestCompanionRestoreV4DeactivationDropsSummary 锁定去激活丢弃摘要：存档中
// 带摘要的记录被移出当前配置（保存时不再提供其队列载荷）后，该记录作为
// inactive 保留身体字段且不带摘要区，其余 active 记录的摘要不受影响。
func TestCompanionRestoreV4DeactivationDropsSummary(t *testing.T) {
	root := t.TempDir()
	store := openCompanionDisk(t, root)
	inactive := fixtureCompanionBodies()[1]
	inactive.ID = fixtureCompanionID(3)
	// 按 ID 升序构造（..01 active 带摘要、..02 active 带摘要、..03 inactive），
	// 保存端排序后字节序与构造序一致，便于逐字段比对。
	records := []companion.Body{fixtureCompanionBodies()[1], fixtureCompanionBodies()[0], inactive}
	withSummary := StoredCompanionQueue{ID: fixtureCompanionID(1), Summary: "一号伙伴的近期记忆"}
	alsoSummary := cloneStoredQueuesForTest(fixtureCompanionV4Queues())[1]
	alsoSummary.Summary = "二号伙伴的近期记忆"
	save := CompanionSave{
		Revision: 51,
		Records:  records,
		Queues:   []StoredCompanionQueue{withSummary, alsoSummary},
	}
	if err := store.SaveCompanions(context.Background(), save); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Records) != 3 || len(loaded.Queues) != 2 ||
		loaded.Queues[0].Summary != withSummary.Summary ||
		loaded.Queues[1].Summary != alsoSummary.Summary {
		t.Fatalf("保存后 loaded=%+v，想要两条带摘要 active 队列", loaded)
	}

	// 去激活：二号伙伴移出配置（无队列载荷即 inactive），一号保持 active。
	deactivated := CompanionSave{
		Revision: 52,
		Records:  records,
		Queues:   []StoredCompanionQueue{withSummary},
	}
	if err := store.SaveCompanions(context.Background(), deactivated); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Records) != 3 {
		t.Fatalf("去激活后记录数=%d，想要保留 3 条身体", len(reloaded.Records))
	}
	wantBodies := append([]companion.Body(nil), records...)
	if !reflect.DeepEqual(reloaded.Records, wantBodies) {
		t.Fatalf("去激活后身体=%+v，想要保持 %+v", reloaded.Records, wantBodies)
	}
	if len(reloaded.Queues) != 1 || reloaded.Queues[0].ID != withSummary.ID ||
		reloaded.Queues[0].Summary != withSummary.Summary {
		t.Fatalf("去激活后 queues=%+v，想要只保留一号伙伴的摘要", reloaded.Queues)
	}
}
