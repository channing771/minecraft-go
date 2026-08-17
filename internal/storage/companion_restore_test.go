// companions.ai schema v3 的任务区/FIFO 持久化与恢复测试：v3 变长步骤
// round-trip 与 golden（四 kind 覆盖）、v2/v1 只读迁移且首次保存写 v3、
// 任务与 FIFO 跨重启精确恢复、损坏矩阵（CRC/future/截断/超 430,080
// bytes/非法任务状态/变长步长错位/follow 携带 deadline）与 5,000 步骤、
// 16 条 FIFO 边界。全部用例不触盘（除显式 DiskStore 用例），失败注入均为
// 字节级或载荷级构造。
package storage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

// fixtureCompanionQueues 构造 v2 代表任务载荷：字节序最小的 active 记录
// （..01）携带三步 Running 任务与满 16 条 FIFO；其余记录保持 inactive。
func fixtureCompanionQueues() []StoredCompanionQueue {
	queue := StoredCompanionQueue{
		ID:         fixtureCompanionID(1),
		HasCurrent: true,
		Current: StoredCompanionTask{
			Command: "先去那棵橡树再看一眼",
			PlanSteps: []companion.PlanStep{
				{Kind: companion.PlanStepGoTo, X: -8, Y: 70, Z: 6},
				{Kind: companion.PlanStepGoTo, X: -4, Y: 70, Z: 9},
				{Kind: companion.PlanStepGoTo, X: 0, Y: 71, Z: 12},
			},
			StepIndex:     1,
			State:         companion.TaskRunning,
			StartTick:     1200,
			DeadlineTicks: 3600,
		},
		Pending: make([]string, MaxCompanionFIFOEntries),
	}
	for index := range queue.Pending {
		queue.Pending[index] = fmt.Sprintf("排队指令第%d条", index+1)
	}
	return []StoredCompanionQueue{queue}
}

// cloneStoredQueuesForTest 深拷贝任务载荷（含计划步骤与 FIFO），供编码后
// 的不变量比对。
func cloneStoredQueuesForTest(queues []StoredCompanionQueue) []StoredCompanionQueue {
	cloned := make([]StoredCompanionQueue, len(queues))
	for index := range queues {
		cloned[index] = queues[index]
		cloned[index].Current.PlanSteps = append(
			[]companion.PlanStep(nil), queues[index].Current.PlanSteps...,
		)
		cloned[index].Pending = append([]string(nil), queues[index].Pending...)
	}
	return cloned
}

// fixtureFollowPlayerID 返回合法 UUIDv4 形状的目标玩家 ID（版本/变体位与
// fixtureCompanionID 同一构造纪律），供 follow 步骤载荷使用。
func fixtureFollowPlayerID() core.PlayerID {
	return core.PlayerID{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x46, 0x77,
		0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x05,
	}
}

// fixtureCompanionV3Queues 构造 v3 代表任务载荷：字节序最小的 active 记录
// （..01）携带覆盖全部四种 kind 的 Running 任务（follow 按结构约束居末且
// deadline 保持零值——持续跟随不保存 deadline）与满 16 条 FIFO；另一条记录
// 保持 inactive（golden 要求四 kind active 任务 + 满 FIFO + inactive 记录）。
func fixtureCompanionV3Queues() []StoredCompanionQueue {
	queue := StoredCompanionQueue{
		ID:         fixtureCompanionID(1),
		HasCurrent: true,
		Current: StoredCompanionTask{
			Command: "去橡树旁挖一格垫一块再跟着我",
			PlanSteps: []companion.PlanStep{
				{Kind: companion.PlanStepGoTo, X: -8, Y: 70, Z: 6},
				{Kind: companion.PlanStepMine, X: -7, Y: 69, Z: 6},
				{Kind: companion.PlanStepPlace, X: -6, Y: 69, Z: 6, Block: core.OakPlanksID},
				{Kind: companion.PlanStepFollow, PlayerID: fixtureFollowPlayerID()},
			},
			StepIndex: 2,
			State:     companion.TaskRunning,
			StartTick: 2400,
		},
		Pending: make([]string, MaxCompanionFIFOEntries),
	}
	for index := range queue.Pending {
		queue.Pending[index] = fmt.Sprintf("v3排队第%d条", index+1)
	}
	return []StoredCompanionQueue{queue}
}

// TestCompanionCodecV3RoundTripAndGolden 锁定 schema v3 变长步骤编码：四 kind
// 步骤按 13/13/15/17 落盘（follow 只携带玩家 ID、place 追加 block uint16）、
// round-trip 精确保留、golden 字节稳定，以及文件上界常量 430,080。
func TestCompanionCodecV3RoundTripAndGolden(t *testing.T) {
	if maxCompanionFileLength != 430080 {
		t.Fatalf("max companion file length=%d，想要 430080", maxCompanionFileLength)
	}
	input := fixtureCompanionBodies()
	queues := fixtureCompanionV3Queues()
	bodiesSnapshot := append([]companion.Body(nil), input...)
	queuesSnapshot := cloneStoredQueuesForTest(queues)
	encoded, err := encodeCompanions(CompanionSave{Revision: 43, Records: input, Queues: queues})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, bodiesSnapshot) || !reflect.DeepEqual(queues, queuesSnapshot) {
		t.Fatalf("编码修改调用者载荷：records=%+v queues=%+v", input, queues)
	}
	if currentCompanionSchema != 3 {
		t.Fatalf("current companion schema=%d，想要 3", currentCompanionSchema)
	}
	if schema := binary.LittleEndian.Uint32(encoded[8:12]); schema != 3 {
		t.Fatalf("schema=%d，想要 3", schema)
	}

	// 文件总长按第一性原理核算：envelope 32 + active 记录（221 身体 + 1
	// flags + 任务区 + FIFO 区）+ inactive 记录（221 身体 + 1 flags）。任务
	// 区 = 指令前缀 2 + 指令字节 + 步骤数 2 + (13+13+15+17) + 步骤索引 4 +
	// 状态 1 + 失败原因 1 + 开始 tick 8 + deadline 8；FIFO 区 = 计数 2 +
	// 每条（前缀 2 + 字节）。place 相对 go_to 基长 +2、follow 用 kind+16-byte
	// 玩家 ID 共 17——变长布局由本断言逐字节锁定。
	taskArea := 2 + len("去橡树旁挖一格垫一块再跟着我") + 2 + (13 + 13 + 15 + 17) + 4 + 1 + 1 + 8 + 8
	fifoArea := 2
	for index := range queuesSnapshot[0].Pending {
		fifoArea += 2 + len(queuesSnapshot[0].Pending[index])
	}
	wantLength := 32 + (companionRecordLength + 1 + taskArea + fifoArea) + (companionRecordLength + 1)
	if len(encoded) != wantLength {
		t.Fatalf("v3 文件长度=%d，想要 %d", len(encoded), wantLength)
	}

	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := []companion.Body{input[1], input[0]}
	if got.Revision != 43 || !reflect.DeepEqual(got.Records, wantRecords) {
		t.Fatalf("decode revision=%d records=%+v，想要 43/%+v", got.Revision, got.Records, wantRecords)
	}
	if !reflect.DeepEqual(got.Queues, queuesSnapshot) {
		t.Fatalf("decode queues=%+v，想要 %+v", got.Queues, queuesSnapshot)
	}
	current := got.Queues[0].Current
	if current.DeadlineTicks != 0 || len(current.PlanSteps) != 4 ||
		current.PlanSteps[2].Block != core.OakPlanksID ||
		current.PlanSteps[3].PlayerID != fixtureFollowPlayerID() {
		t.Fatalf("v3 任务字段=%+v，想要变长载荷精确保留且 follow 零 deadline", current)
	}

	path := filepath.Join("testdata", "companions-v3.bin")
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
		t.Fatal("companions v3 fixture drift；需要显式 -update-storage-fixtures 重生成并评审字节")
	}
	clear(encoded)
	if !reflect.DeepEqual(got.Queues, queuesSnapshot) {
		t.Fatalf("修改输入 bytes 后 decode 结果=%+v，想要保持 %+v", got.Queues, queuesSnapshot)
	}
}

// TestCompanionRestoreV2ReadOnlyMigrationAndV3Rewrite 锁定 v2 只读迁移：既有
// golden（go_to 固定 13B 布局）无损读入，首次保存只写 v3，v2→v3 重写后载荷
// 不漂移；磁盘路径与内存路径同一套编解码。
func TestCompanionRestoreV2ReadOnlyMigrationAndV3Rewrite(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "companions-v2.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if schema := binary.LittleEndian.Uint32(golden[8:12]); schema != 2 {
		t.Fatalf("v2 golden schema=%d，想要 2（golden 字节冻结）", schema)
	}
	got, err := decodeCompanions(golden)
	if err != nil {
		t.Fatal(err)
	}
	wantQueues := fixtureCompanionQueues()
	if got.Revision != 41 || !reflect.DeepEqual(got.Queues, wantQueues) {
		t.Fatalf("v2 迁移 decode=%+v，想要 revision=41 无损载荷 %+v", got, wantQueues)
	}
	// 规范重编码只写 v3：go_to 步骤 13B 布局在 v3 中原样保留。
	encoded, err := encodeCompanions(CompanionSave{
		Revision: got.Revision,
		Records:  got.Records,
		Queues:   got.Queues,
	})
	if err != nil {
		t.Fatal(err)
	}
	if schema := binary.LittleEndian.Uint32(encoded[8:12]); schema != 3 {
		t.Fatalf("重编码 schema=%d，想要 3", schema)
	}
	migrated, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Revision != 41 || !reflect.DeepEqual(migrated.Records, got.Records) ||
		!reflect.DeepEqual(migrated.Queues, wantQueues) {
		t.Fatalf("v2→v3 重写后载荷漂移：%+v", migrated)
	}

	root := t.TempDir()
	store := openCompanionDisk(t, root)
	path := filepath.Join(root, "companions.ai")
	if err := os.WriteFile(path, golden, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 41 || !reflect.DeepEqual(loaded.Queues, wantQueues) {
		t.Fatalf("v2 磁盘迁移 loaded=%+v，想要无损载荷", loaded)
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
	if schema := binary.LittleEndian.Uint32(after[8:12]); schema != 3 {
		t.Fatalf("首次保存 schema=%d，想要写 v3", schema)
	}
	reloaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != 42 || !reflect.DeepEqual(reloaded.Queues, wantQueues) {
		t.Fatalf("迁移后再加载=%+v，想要载荷保持", reloaded)
	}
}

// TestCompanionCodecV2RoundTripAndGolden 锁定 v2 时代载荷（全部 go_to 步骤）
// 经当前编码器的 round-trip：v2 golden 文件是冻结的只读迁移输入（字节零
// 改动），编码端只写 v3；同一 go_to 载荷在 v3 中按相同 13B 布局到达。
func TestCompanionCodecV2RoundTripAndGolden(t *testing.T) {
	input := fixtureCompanionBodies()
	queues := fixtureCompanionQueues()
	bodiesSnapshot := append([]companion.Body(nil), input...)
	queuesSnapshot := cloneStoredQueuesForTest(queues)
	encoded, err := encodeCompanions(CompanionSave{Revision: 41, Records: input, Queues: queues})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, bodiesSnapshot) || !reflect.DeepEqual(queues, queuesSnapshot) {
		t.Fatalf("编码修改调用者载荷：records=%+v queues=%+v", input, queues)
	}
	if schema := binary.LittleEndian.Uint32(encoded[8:12]); schema != currentCompanionSchema {
		t.Fatalf("schema=%d，想要 %d", schema, currentCompanionSchema)
	}

	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := []companion.Body{input[1], input[0]}
	if got.Revision != 41 || !reflect.DeepEqual(got.Records, wantRecords) {
		t.Fatalf("decode revision=%d records=%+v，想要 41/%+v", got.Revision, got.Records, wantRecords)
	}
	if !reflect.DeepEqual(got.Queues, queuesSnapshot) {
		t.Fatalf("decode queues=%+v，想要 %+v", got.Queues, queuesSnapshot)
	}
	current := got.Queues[0].Current
	if current.Command != "先去那棵橡树再看一眼" || current.StepIndex != 1 ||
		current.State != companion.TaskRunning || current.StartTick != 1200 ||
		current.DeadlineTicks != 3600 || len(current.PlanSteps) != 3 {
		t.Fatalf("任务区字段=%+v，想要精确保留", current)
	}
	for index, command := range got.Queues[0].Pending {
		if command != queuesSnapshot[0].Pending[index] {
			t.Fatalf("FIFO[%d]=%q，想要 %q", index, command, queuesSnapshot[0].Pending[index])
		}
	}

	// v2 golden 是冻结的历史字节：编码端不再产出 v2，文件只作为迁移输入。
	path := filepath.Join("testdata", "companions-v2.bin")
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if schema := binary.LittleEndian.Uint32(want[8:12]); schema != 2 {
		t.Fatalf("v2 golden schema=%d，想要 2（字节冻结零改动）", schema)
	}
	v2Decoded, err := decodeCompanions(want)
	if err != nil {
		t.Fatal(err)
	}
	if v2Decoded.Revision != 41 || !reflect.DeepEqual(v2Decoded.Queues, queuesSnapshot) {
		t.Fatalf("v2 golden 迁移 decode=%+v，想要与 round-trip 同一载荷", v2Decoded)
	}
	clear(encoded)
	if !reflect.DeepEqual(got.Queues, queuesSnapshot) {
		t.Fatalf("修改输入 bytes 后 decode 结果=%+v，想要保持 %+v", got.Queues, queuesSnapshot)
	}
}

// TestCompanionRestoreV1ReadOnlyMigrationAndFirstSaveWritesV3 锁定 v1→v3 迁移：
// v1 golden（仅身体）无损读入，首次保存直接写当前 schema v3。
func TestCompanionRestoreV1ReadOnlyMigrationAndFirstSaveWritesV3(t *testing.T) {
	v1, err := os.ReadFile(filepath.Join("testdata", "companions-v1.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if schema := binary.LittleEndian.Uint32(v1[8:12]); schema != 1 {
		t.Fatalf("v1 golden schema=%d，想要 1", schema)
	}
	got, err := decodeCompanions(v1)
	if err != nil {
		t.Fatal(err)
	}
	wantRecords := []companion.Body{fixtureCompanionBodies()[1], fixtureCompanionBodies()[0]}
	if got.Revision != 19 || !reflect.DeepEqual(got.Records, wantRecords) {
		t.Fatalf("v1 迁移 decode=%+v，想要 revision=19 records=%+v", got, wantRecords)
	}
	if got.Queues != nil {
		t.Fatalf("v1 迁移必须产出空任务域：%+v", got.Queues)
	}

	root := t.TempDir()
	store := openCompanionDisk(t, root)
	path := filepath.Join(root, "companions.ai")
	if err := os.WriteFile(path, v1, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 19 || loaded.Queues != nil ||
		!reflect.DeepEqual(loaded.Records, wantRecords) {
		t.Fatalf("v1 磁盘迁移 loaded=%+v，想要身体恢复且任务域为空", loaded)
	}
	if err := store.SaveCompanions(context.Background(), CompanionSave{
		Revision: loaded.Revision + 1,
		Records:  loaded.Records,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if schema := binary.LittleEndian.Uint32(after[8:12]); schema != currentCompanionSchema {
		t.Fatalf("首次保存 schema=%d，想要写 v3", schema)
	}
	reloaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Revision != 20 || reloaded.Queues != nil ||
		!reflect.DeepEqual(reloaded.Records, wantRecords) {
		t.Fatalf("迁移后再加载=%+v，想要身体保持且任务域为空", reloaded)
	}
}

func TestCompanionRestoreTasksAndFIFOAcrossRestart(t *testing.T) {
	root := t.TempDir()
	store := openCompanionDisk(t, root)
	queues := []StoredCompanionQueue{
		{
			ID:         fixtureCompanionID(1),
			HasCurrent: true,
			Current: StoredCompanionTask{
				Command: "去北边橡树",
				PlanSteps: []companion.PlanStep{
					{Kind: companion.PlanStepGoTo, X: 4, Y: 64, Z: -2},
					{Kind: companion.PlanStepGoTo, X: 9, Y: 64, Z: -7},
				},
				StepIndex:     1,
				State:         companion.TaskRunning,
				StartTick:     77,
				DeadlineTicks: 1277,
			},
			Pending: []string{"第二条指令", "第三条指令"},
		},
		{
			ID:         fixtureCompanionID(2),
			HasCurrent: true,
			Current: StoredCompanionTask{
				Command: "规划中的指令",
				State:   companion.TaskPlanning,
			},
		},
	}
	if err := store.SaveCompanions(context.Background(), CompanionSave{
		Revision: 6,
		Records:  fixtureCompanionBodies(),
		Queues:   queues,
	}); err != nil {
		t.Fatal(err)
	}
	// 落盘后修改调用方切片不得影响已保存内容。
	queues[0].Current.PlanSteps[0].X = 999
	queues[0].Pending[0] = "被篡改"
	queues[1].Current.Command = "被篡改"

	loaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []StoredCompanionQueue{
		{
			ID:         fixtureCompanionID(1),
			HasCurrent: true,
			Current: StoredCompanionTask{
				Command: "去北边橡树",
				PlanSteps: []companion.PlanStep{
					{Kind: companion.PlanStepGoTo, X: 4, Y: 64, Z: -2},
					{Kind: companion.PlanStepGoTo, X: 9, Y: 64, Z: -7},
				},
				StepIndex:     1,
				State:         companion.TaskRunning,
				StartTick:     77,
				DeadlineTicks: 1277,
			},
			Pending: []string{"第二条指令", "第三条指令"},
		},
		{
			ID:         fixtureCompanionID(2),
			HasCurrent: true,
			Current: StoredCompanionTask{
				Command: "规划中的指令",
				State:   companion.TaskPlanning,
			},
		},
	}
	if loaded.Revision != 6 || !reflect.DeepEqual(loaded.Queues, want) {
		t.Fatalf("跨重启恢复 queues=%+v，想要 %+v", loaded.Queues, want)
	}
	if loaded.Queues[0].Pending[0] != "第二条指令" ||
		loaded.Queues[0].Pending[1] != "第三条指令" {
		t.Fatalf("FIFO 顺序=%v", loaded.Queues[0].Pending)
	}
	// 解码结果与底层字节完全独立。
	loaded.Queues[0].Pending[0] = "再次篡改"
	again, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Queues, want) {
		t.Fatalf("二次加载 queues=%+v，想要保持 %+v", again.Queues, want)
	}
}

func TestCompanionRestoreFIFOOnlyQueueRoundTrip(t *testing.T) {
	// FIFO-only 载荷（无当前任务、仅待执行指令）是服务端的真实形态：
	// 任务终态清槽后 FIFO 仍有排队指令的瞬间即产出它。编码与解码必须
	// 对这种记录对称——HasCurrent 为假时 Current 是零值，不得参与任务
	// 枚举校验。
	queue := StoredCompanionQueue{
		ID:      fixtureCompanionID(1),
		Pending: []string{"仅排队甲", "仅排队乙"},
	}
	encoded, err := encodeCompanions(CompanionSave{
		Revision: 3,
		Records:  fixtureCompanionBodies(),
		Queues:   []StoredCompanionQueue{queue},
	})
	if err != nil {
		t.Fatalf("FIFO-only encode error=%v，想要成功", err)
	}
	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != 3 || !reflect.DeepEqual(got.Queues, []StoredCompanionQueue{queue}) {
		t.Fatalf("FIFO-only codec round-trip=%+v，想要 %+v", got.Queues, queue)
	}

	root := t.TempDir()
	store := openCompanionDisk(t, root)
	if err := store.SaveCompanions(context.Background(), CompanionSave{
		Revision: 3,
		Records:  fixtureCompanionBodies(),
		Queues:   []StoredCompanionQueue{queue},
	}); err != nil {
		t.Fatalf("FIFO-only 真实存档保存 error=%v，想要成功", err)
	}
	loaded, err := store.LoadCompanions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 3 || !reflect.DeepEqual(loaded.Queues, []StoredCompanionQueue{queue}) {
		t.Fatalf("FIFO-only 磁盘 round-trip=%+v，想要 %+v", loaded.Queues, queue)
	}
}

func TestCompanionRestoreRejectsCorruptTaskPayloads(t *testing.T) {
	valid, err := encodeCompanions(CompanionSave{
		Revision: 7,
		Records:  fixtureCompanionBodies(),
		Queues:   fixtureCompanionQueues(),
	})
	if err != nil {
		t.Fatal(err)
	}

	mutations := []struct {
		name   string
		base   func() []StoredCompanionQueue
		mutate func([]StoredCompanionQueue) []StoredCompanionQueue
	}{
		{"zero state", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = 0
			return queues
		}},
		{"state above enum", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskState(9)
			return queues
		}},
		{"running without steps", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps = nil
			return queues
		}},
		{"running step index out of range", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.StepIndex = 3
			return queues
		}},
		{"queued keeps plan", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskQueued
			return queues
		}},
		{"non-running keeps ticks", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskPlanning
			queues[0].Current.PlanSteps = nil
			queues[0].Current.StepIndex = 0
			return queues
		}},
		{"illegal step kind", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[1].Kind = companion.PlanStepKind(5)
			return queues
		}},
		{"step Y out of world", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[2].Y = math.MaxInt32
			return queues
		}},
		{"fail reason on running", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.FailReason = companion.TaskFailInvalidPlan
			return queues
		}},
		{"illegal fail reason", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskFailed
			queues[0].Current.PlanSteps = nil
			queues[0].Current.StepIndex = 0
			queues[0].Current.StartTick = 0
			queues[0].Current.DeadlineTicks = 0
			queues[0].Current.FailReason = companion.TaskFailReason(6)
			return queues
		}},
		{"command exceeds bytes", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.Command = strings.Repeat("走", 342)
			return queues
		}},
		{"command empty", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.Command = ""
			return queues
		}},
		{"command control rune", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.Command = "去\n那边"
			return queues
		}},
		{"fifo over depth", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Pending = append(queues[0].Pending, "第十七条")
			return queues
		}},
		{"fifo entry exceeds bytes", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Pending[0] = strings.Repeat("走", 342)
			return queues
		}},
		{"fifo entry empty", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Pending[0] = ""
			return queues
		}},
		{"current task without has current", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].HasCurrent = false
			return queues
		}},
		{"queue without record", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].ID = fixtureCompanionID(9)
			return queues
		}},
		{"duplicate queue ID", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			return append(queues, queues[0])
		}},
		{"empty queue", fixtureCompanionQueues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			return []StoredCompanionQueue{{ID: queues[0].ID}}
		}},
		// v3 变长布局的结构拒绝：follow 的零 deadline、位置、目标有效性与
		// 各 kind 未用字段的零值约束都在编码边界强制。
		{"v3 follow keeps deadline", fixtureCompanionV3Queues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.DeadlineTicks = 3600
			return queues
		}},
		{"v3 follow not last", fixtureCompanionV3Queues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps = []companion.PlanStep{
				{Kind: companion.PlanStepFollow, PlayerID: fixtureFollowPlayerID()},
				{Kind: companion.PlanStepGoTo, X: 1, Y: 64, Z: 1},
			}
			return queues
		}},
		{"v3 follow invalid player ID", fixtureCompanionV3Queues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[3].PlayerID = core.PlayerID{}
			return queues
		}},
		{"v3 go_to keeps player payload", fixtureCompanionV3Queues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[0].PlayerID = fixtureFollowPlayerID()
			return queues
		}},
		{"v3 place keeps player payload", fixtureCompanionV3Queues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[2].PlayerID = fixtureFollowPlayerID()
			return queues
		}},
		{"v3 follow keeps coordinates", fixtureCompanionV3Queues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[3].X = 3
			return queues
		}},
		{"v3 go_to keeps block payload", fixtureCompanionV3Queues, func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[0].Block = core.OakPlanksID
			return queues
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			queues := tc.mutate(cloneStoredQueuesForTest(tc.base()))
			_, err := encodeCompanions(CompanionSave{
				Revision: 7,
				Records:  fixtureCompanionBodies(),
				Queues:   queues,
			})
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("encode error=%v，想要 ErrCorrupt", err)
			}
		})
	}

	// v3 变长布局的解码侧字节补丁：偏移按第一性原理推导——首记录任务区
	// 起点为 32+221+1，指令前缀 2 + 指令字节 + 步骤数 2 后进入步骤区；
	// go_to/mine 各 13、place 15、follow 17。follow 的玩家 ID 在其 kind 字节
	// 之后，deadline 在步骤区之后的 4+1+1+8 偏移处。
	v3Valid, err := encodeCompanions(CompanionSave{
		Revision: 7,
		Records:  fixtureCompanionBodies(),
		Queues:   fixtureCompanionV3Queues(),
	})
	if err != nil {
		t.Fatal(err)
	}
	commandBytes := len("去橡树旁挖一格垫一块再跟着我")
	v3StepsBase := companionHeaderLength + companionRecordLength + 1 + 2 + commandBytes + 2
	v3FollowKindOffset := v3StepsBase + 13 + 13 + 15
	v3DeadlineOffset := v3FollowKindOffset + 17 + 4 + 1 + 1 + 8

	byteTests := []struct {
		name    string
		payload func() []byte
		want    error
	}{
		{"CRC", func() []byte {
			payload := bytes.Clone(valid)
			payload[len(payload)-1] ^= 0xff
			return payload
		}, ErrCorrupt},
		{"truncation", func() []byte { return bytes.Clone(valid[:len(valid)-1]) }, ErrCorrupt},
		{"trailing byte", func() []byte { return append(bytes.Clone(valid), 0) }, ErrCorrupt},
		{"future schema", func() []byte {
			payload := bytes.Clone(valid)
			binary.LittleEndian.PutUint32(payload[8:], currentCompanionSchema+1)
			return payload
		}, ErrFutureVersion},
		{"future envelope", func() []byte {
			payload := bytes.Clone(valid)
			binary.LittleEndian.PutUint32(payload[4:], companionEnvelopeVersion+1)
			return payload
		}, ErrFutureVersion},
		{"reserved flags", func() []byte {
			payload := bytes.Clone(valid)
			payload[companionHeaderLength+companionRecordLength] |= 0x04
			repairCompanionCRC(payload)
			return payload
		}, ErrCorrupt},
		// v3 非法 kind：解码在步骤处立即拒绝，绝不按猜测步长继续（错位会
		// 把后续字段整体读错）。
		{"v3 illegal step kind", func() []byte {
			payload := bytes.Clone(v3Valid)
			payload[v3FollowKindOffset] = 0x09
			repairCompanionCRC(payload)
			return payload
		}, ErrCorrupt},
		// v3 follow 目标玩家 ID 非法（全零）。
		{"v3 follow player ID invalid", func() []byte {
			payload := bytes.Clone(v3Valid)
			clear(payload[v3FollowKindOffset+1 : v3FollowKindOffset+17])
			repairCompanionCRC(payload)
			return payload
		}, ErrCorrupt},
		// v3 follow 任务携带非零 deadline：解码侧与编码侧同一道门。
		{"v3 follow deadline set", func() []byte {
			payload := bytes.Clone(v3Valid)
			binary.LittleEndian.PutUint64(payload[v3DeadlineOffset:], 3600)
			repairCompanionCRC(payload)
			return payload
		}, ErrCorrupt},
	}
	for _, tc := range byteTests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeCompanions(tc.payload()); !errors.Is(err, tc.want) {
				t.Fatalf("decode error=%v，想要 %v", err, tc.want)
			}
		})
	}

	// v2 文件中的非 go_to 步骤字节是 v2 时代不可能出现的载荷：只读迁移
	// 必须按损坏拒绝，绝不猜步长。
	v2Golden, err := os.ReadFile(filepath.Join("testdata", "companions-v2.bin"))
	if err != nil {
		t.Fatal(err)
	}
	v2StepKind := companionHeaderLength + companionRecordLength + 1 + 2 + len("先去那棵橡树再看一眼") + 2
	v2IllegalKind := bytes.Clone(v2Golden)
	v2IllegalKind[v2StepKind] = byte(companion.PlanStepFollow)
	repairCompanionCRC(v2IllegalKind)
	if _, err := decodeCompanions(v2IllegalKind); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("v2 非 go_to 步骤 decode error=%v，想要 ErrCorrupt", err)
	}

	// 超 430,080 bytes 的文件必须在任何解析与分配之前被拒绝。
	oversized := bytes.Repeat([]byte{0x5a}, maxCompanionFileLength+1)
	if _, err := decodeCompanions(oversized); !errors.Is(err, ErrCorrupt) ||
		!strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("oversized error=%v，想要分配前长度门禁", err)
	}
}

func TestCompanionRestorePlanAndFIFOBounds(t *testing.T) {
	steps := make([]companion.PlanStep, MaxCompanionPlanSteps)
	for index := range steps {
		steps[index] = companion.PlanStep{
			Kind: companion.PlanStepGoTo,
			X:    int32(index),
			Y:    64,
			Z:    -int32(index),
		}
	}
	queue := StoredCompanionQueue{
		ID:         fixtureCompanionID(2),
		HasCurrent: true,
		Current: StoredCompanionTask{
			Command:       "长计划",
			PlanSteps:     steps,
			StepIndex:     MaxCompanionPlanSteps - 1,
			State:         companion.TaskRunning,
			StartTick:     1,
			DeadlineTicks: 1201,
		},
		Pending: make([]string, MaxCompanionFIFOEntries),
	}
	for index := range queue.Pending {
		queue.Pending[index] = "排队"
	}
	encoded, err := encodeCompanions(CompanionSave{
		Revision: 2,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{queue},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Queues) != 1 || len(got.Queues[0].Current.PlanSteps) != MaxCompanionPlanSteps ||
		got.Queues[0].Current.StepIndex != MaxCompanionPlanSteps-1 ||
		len(got.Queues[0].Pending) != MaxCompanionFIFOEntries {
		t.Fatalf("上界载荷 decode=%+v", got.Queues)
	}

	// 第 5,001 步必须在编码边界拒绝。
	over := cloneStoredQueuesForTest([]StoredCompanionQueue{queue})
	over[0].Current.PlanSteps = append(
		over[0].Current.PlanSteps,
		companion.PlanStep{Kind: companion.PlanStepGoTo, X: 1, Y: 64, Z: 1},
	)
	if _, err := encodeCompanions(CompanionSave{
		Revision: 3,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   over,
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("5,001 步 encode error=%v，想要 ErrCorrupt", err)
	}

	// 第 17 条 FIFO 同样在编码边界拒绝。
	overFIFO := cloneStoredQueuesForTest([]StoredCompanionQueue{queue})
	overFIFO[0].Current.PlanSteps = overFIFO[0].Current.PlanSteps[:1]
	overFIFO[0].Current.StepIndex = 0
	overFIFO[0].Pending = append(overFIFO[0].Pending, "第十七条")
	if _, err := encodeCompanions(CompanionSave{
		Revision: 4,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   overFIFO,
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("17 条 FIFO encode error=%v，想要 ErrCorrupt", err)
	}
}

// TestCompanionRestoreV3MaxVariableStepBudget 锁定变长布局下的步骤预算：按
// 最大 17-byte 步长（4,999 个 13-byte 坐标步骤 + 收尾 follow）核算的 5,000
// 步任务区必须完整落盘并 round-trip；第 5,001 步仍在编码边界拒绝。
func TestCompanionRestoreV3MaxVariableStepBudget(t *testing.T) {
	steps := make([]companion.PlanStep, MaxCompanionPlanSteps-1)
	for index := range steps {
		steps[index] = companion.PlanStep{Kind: companion.PlanStepMine, X: int32(index), Y: 64, Z: 0}
	}
	steps = append(steps, companion.PlanStep{Kind: companion.PlanStepFollow, PlayerID: fixtureFollowPlayerID()})
	queue := StoredCompanionQueue{
		ID:         fixtureCompanionID(2),
		HasCurrent: true,
		Current: StoredCompanionTask{
			Command:   "最长变长计划",
			PlanSteps: steps,
			StepIndex: MaxCompanionPlanSteps - 1,
			State:     companion.TaskRunning,
			StartTick: 9,
		},
	}
	encoded, err := encodeCompanions(CompanionSave{
		Revision: 5,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   []StoredCompanionQueue{queue},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > maxCompanionFileLength {
		t.Fatalf("最大变长预算文件长度=%d，超过上界 %d", len(encoded), maxCompanionFileLength)
	}
	got, err := decodeCompanions(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded := got.Queues[0].Current
	if len(decoded.PlanSteps) != MaxCompanionPlanSteps ||
		decoded.PlanSteps[MaxCompanionPlanSteps-1].PlayerID != fixtureFollowPlayerID() ||
		decoded.DeadlineTicks != 0 {
		t.Fatalf("变长上界载荷 decode=%+v", decoded)
	}

	over := cloneStoredQueuesForTest([]StoredCompanionQueue{queue})
	over[0].Current.PlanSteps = append(
		over[0].Current.PlanSteps[:MaxCompanionPlanSteps-1],
		companion.PlanStep{Kind: companion.PlanStepGoTo, X: 1, Y: 64, Z: 1},
		companion.PlanStep{Kind: companion.PlanStepFollow, PlayerID: fixtureFollowPlayerID()},
	)
	if _, err := encodeCompanions(CompanionSave{
		Revision: 6,
		Records:  fixtureCompanionBodies()[:1],
		Queues:   over,
	}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("5,001 步 encode error=%v，想要 ErrCorrupt", err)
	}
}
