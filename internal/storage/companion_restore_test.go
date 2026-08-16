// companions.ai schema v2 的任务区/FIFO 持久化与恢复测试：v2 round-trip 与
// golden、v1 只读迁移且首次保存写 v2、任务与 FIFO 跨重启精确恢复、损坏矩阵
// （CRC/future/截断/超 350,208 bytes/非法任务状态/非法 count）与 5,000 步骤、
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

func TestCompanionCodecV2RoundTripAndGolden(t *testing.T) {
	if maxCompanionFileLength != 350208 {
		t.Fatalf("max companion file length=%d，想要 350208", maxCompanionFileLength)
	}
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

	path := filepath.Join("testdata", "companions-v2.bin")
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
		t.Fatal("companions v2 fixture drift；需要显式 -update-storage-fixtures 重生成并评审字节")
	}
	clear(encoded)
	if !reflect.DeepEqual(got.Queues, queuesSnapshot) {
		t.Fatalf("修改输入 bytes 后 decode 结果=%+v，想要保持 %+v", got.Queues, queuesSnapshot)
	}
}

func TestCompanionRestoreV1ReadOnlyMigrationAndFirstSaveWritesV2(t *testing.T) {
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
		t.Fatalf("首次保存 schema=%d，想要写 v2", schema)
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
		mutate func([]StoredCompanionQueue) []StoredCompanionQueue
	}{
		{"zero state", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = 0
			return queues
		}},
		{"state above enum", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskState(7)
			return queues
		}},
		{"running without steps", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps = nil
			return queues
		}},
		{"running step index out of range", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.StepIndex = 3
			return queues
		}},
		{"queued keeps plan", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskQueued
			return queues
		}},
		{"non-running keeps ticks", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskPlanning
			queues[0].Current.PlanSteps = nil
			queues[0].Current.StepIndex = 0
			return queues
		}},
		{"illegal step kind", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[1].Kind = companion.PlanStepKind(2)
			return queues
		}},
		{"step Y out of world", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.PlanSteps[2].Y = math.MaxInt32
			return queues
		}},
		{"fail reason on running", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.FailReason = companion.TaskFailInvalidPlan
			return queues
		}},
		{"illegal fail reason", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.State = companion.TaskFailed
			queues[0].Current.PlanSteps = nil
			queues[0].Current.StepIndex = 0
			queues[0].Current.StartTick = 0
			queues[0].Current.DeadlineTicks = 0
			queues[0].Current.FailReason = companion.TaskFailReason(5)
			return queues
		}},
		{"command exceeds bytes", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.Command = strings.Repeat("走", 342)
			return queues
		}},
		{"command empty", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.Command = ""
			return queues
		}},
		{"command control rune", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Current.Command = "去\n那边"
			return queues
		}},
		{"fifo over depth", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Pending = append(queues[0].Pending, "第十七条")
			return queues
		}},
		{"fifo entry exceeds bytes", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Pending[0] = strings.Repeat("走", 342)
			return queues
		}},
		{"fifo entry empty", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].Pending[0] = ""
			return queues
		}},
		{"queue without record", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			queues[0].ID = fixtureCompanionID(9)
			return queues
		}},
		{"duplicate queue ID", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			return append(queues, queues[0])
		}},
		{"empty queue", func(queues []StoredCompanionQueue) []StoredCompanionQueue {
			return []StoredCompanionQueue{{ID: queues[0].ID}}
		}},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			queues := tc.mutate(cloneStoredQueuesForTest(fixtureCompanionQueues()))
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
	}
	for _, tc := range byteTests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeCompanions(tc.payload()); !errors.Is(err, tc.want) {
				t.Fatalf("decode error=%v，想要 %v", err, tc.want)
			}
		})
	}

	// 超 350,208 bytes 的文件必须在任何解析与分配之前被拒绝。
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
