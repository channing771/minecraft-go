package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

// fuzzTaskRecordOffsets 是携带任务载荷的单记录文件的关键偏移：固定指令
// "go"（2 bytes）与单步计划，使状态/步骤数/FIFO 计数字节可被稳定补丁。
// 布局变化时本表必须同步更新（与 v1 offset 测试同一纪律）。v3 变长种子
// 的偏移按四 kind 布局另行推导（go_to 13 + mine 13 + place 15 + follow 17）。
const (
	fuzzTaskStepsCountOffset = 32 + companionRecordLength + 1 + 2 + 2
	fuzzTaskStateOffset      = fuzzTaskStepsCountOffset + 2 + 13 + 4
	fuzzTaskFIFOCountOffset  = fuzzTaskStateOffset + 1 + 1 + 8 + 8
	// fuzzV3StepsBase 是 v3 四 kind 种子的步骤区起点（指令 "go" 同上）。
	fuzzV3StepsBase = 32 + companionRecordLength + 1 + 2 + 2 + 2
	// fuzzV3PlaceKindOffset 是 place 步骤 kind 字节（go_to 与 mine 各 13
	// bytes 之后）。
	fuzzV3PlaceKindOffset = fuzzV3StepsBase + 13 + 13
	// fuzzV3FollowKindOffset 是 follow 步骤 kind 字节（place 15 bytes 之后），
	// 其后 16 bytes 是目标玩家 ID。
	fuzzV3FollowKindOffset = fuzzV3PlaceKindOffset + 15
	// fuzzV3DeadlineOffset 是 v3 种子任务区的 deadline 字段（步骤区之后
	// 步骤索引 4 + 状态 1 + 失败原因 1 + 开始 tick 8）。
	fuzzV3DeadlineOffset = fuzzV3FollowKindOffset + 17 + 4 + 1 + 1 + 8
)

func FuzzDecodeCompanions(f *testing.F) {
	if fixture, err := os.ReadFile(filepath.Join("testdata", "companions-v1.bin")); err == nil {
		for length := range len(fixture) + 1 {
			f.Add(bytes.Clone(fixture[:length]))
		}
	}
	if fixture, err := os.ReadFile(filepath.Join("testdata", "companions-v2.bin")); err == nil {
		for length := range len(fixture) + 1 {
			f.Add(bytes.Clone(fixture[:length]))
		}
	}
	records := make([]companion.Body, companion.MaxStored)
	for index := range records {
		records[index].ID = fixtureCompanionID(byte(index))
	}
	maximum, err := encodeCompanions(CompanionSave{Revision: 1, Records: records})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(maximum)
	// 单记录 Running 任务 + 两条 FIFO：驱动任务区/FIFO 解码路径。
	taskBearing, err := encodeCompanions(CompanionSave{
		Revision: 5,
		Records:  fixtureCompanionBodies()[:1],
		Queues: []StoredCompanionQueue{{
			ID:         fixtureCompanionID(2),
			HasCurrent: true,
			Current: StoredCompanionTask{
				Command:       "go",
				PlanSteps:     []companion.PlanStep{{Kind: companion.PlanStepGoTo, X: 1, Y: 64, Z: 2}},
				State:         companion.TaskRunning,
				StartTick:     5,
				DeadlineTicks: 1205,
			},
			Pending: []string{"go", "go2"},
		}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(taskBearing)
	// FIFO-only 形态（无当前任务、仅排队指令）：flags 仅 bit1，驱动
	// 「HasCurrent 为假时 Current 零值」的编码/解码对称路径。
	fifoOnly, err := encodeCompanions(CompanionSave{
		Revision: 6,
		Records:  fixtureCompanionBodies()[:1],
		Queues: []StoredCompanionQueue{{
			ID:      fixtureCompanionID(2),
			Pending: []string{"仅排队甲", "仅排队乙"},
		}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(fifoOnly)
	// 非法状态枚举与超界 count 种子：CRC 已修复，解码必须深入任务区校验。
	invalidState := bytes.Clone(taskBearing)
	invalidState[fuzzTaskStateOffset] = 7
	repairCompanionCRC(invalidState)
	f.Add(invalidState)
	oversizedSteps := bytes.Clone(taskBearing)
	binary.LittleEndian.PutUint16(oversizedSteps[fuzzTaskStepsCountOffset:], MaxCompanionPlanSteps+1)
	repairCompanionCRC(oversizedSteps)
	f.Add(oversizedSteps)
	oversizedFIFO := bytes.Clone(taskBearing)
	oversizedFIFO[fuzzTaskFIFOCountOffset] = byte(MaxCompanionFIFOEntries + 1)
	repairCompanionCRC(oversizedFIFO)
	f.Add(oversizedFIFO)
	// v3 变长种子：四 kind 全覆盖的 Running 任务驱动变长解码路径；非法
	// kind 步长错位、follow 目标非法与 follow 携带 deadline 三个补丁种子
	// 深入步骤级校验。
	v3TaskBearing, err := encodeCompanions(CompanionSave{
		Revision: 7,
		Records:  fixtureCompanionBodies()[:1],
		Queues: []StoredCompanionQueue{{
			ID:         fixtureCompanionID(2),
			HasCurrent: true,
			Current: StoredCompanionTask{
				Command: "go",
				PlanSteps: []companion.PlanStep{
					{Kind: companion.PlanStepGoTo, X: 1, Y: 64, Z: 2},
					{Kind: companion.PlanStepMine, X: 1, Y: 65, Z: 2},
					{Kind: companion.PlanStepPlace, X: 1, Y: 66, Z: 2, Block: core.OakPlanksID},
					{Kind: companion.PlanStepFollow, PlayerID: fixtureFollowPlayerID()},
				},
				StepIndex: 1,
				State:     companion.TaskRunning,
				StartTick: 5,
			},
			Pending: []string{"go", "go2"},
		}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(v3TaskBearing)
	illegalKind := bytes.Clone(v3TaskBearing)
	illegalKind[fuzzV3PlaceKindOffset] = 0x09
	repairCompanionCRC(illegalKind)
	f.Add(illegalKind)
	followTargetInvalid := bytes.Clone(v3TaskBearing)
	clear(followTargetInvalid[fuzzV3FollowKindOffset+1 : fuzzV3FollowKindOffset+17])
	repairCompanionCRC(followTargetInvalid)
	f.Add(followTargetInvalid)
	followDeadlineSet := bytes.Clone(v3TaskBearing)
	binary.LittleEndian.PutUint64(followDeadlineSet[fuzzV3DeadlineOffset:], 1200)
	repairCompanionCRC(followDeadlineSet)
	f.Add(followDeadlineSet)
	oversized := make([]byte, 32)
	copy(oversized, "MCAI")
	binary.LittleEndian.PutUint32(oversized[4:], 1)
	binary.LittleEndian.PutUint32(oversized[8:], 1)
	binary.LittleEndian.PutUint64(oversized[12:], 1)
	binary.LittleEndian.PutUint32(oversized[20:], companion.MaxStored+1)
	binary.LittleEndian.PutUint32(oversized[24:], (companion.MaxStored+1)*221)
	f.Add(oversized)
	f.Fuzz(func(t *testing.T, payload []byte) {
		got, err := decodeCompanions(payload)
		if err != nil {
			return
		}
		if got.Revision == 0 || len(got.Records) > companion.MaxStored {
			t.Fatalf("successful decode escaped bounds: %+v", got)
		}
		for index, body := range got.Records {
			if !body.ID.Valid() || !body.Inventory.Valid() || index > 0 && bytes.Compare(got.Records[index-1].ID[:], body.ID[:]) >= 0 {
				t.Fatalf("successful decode returned invalid records: %+v", got.Records)
			}
		}
		seen := make(map[companion.ID]struct{}, len(got.Queues))
		for _, queue := range got.Queues {
			if _, duplicate := seen[queue.ID]; duplicate ||
				len(queue.Pending) > MaxCompanionFIFOEntries ||
				len(queue.Current.PlanSteps) > MaxCompanionPlanSteps {
				t.Fatalf("successful decode escaped task bounds: %+v", got.Queues)
			}
			seen[queue.ID] = struct{}{}
		}
		// v1/v2 是只读迁移 schema：解码成功即可；规范重编码只写 v3，
		// 字节不可比对。
		if schema := binary.LittleEndian.Uint32(payload[8:12]); schema != currentCompanionSchema {
			return
		}
		encoded, err := encodeCompanions(CompanionSave{
			Revision: got.Revision,
			Records:  got.Records,
			Queues:   got.Queues,
		})
		if err != nil || !bytes.Equal(encoded, payload) {
			t.Fatalf("successful decode is not canonical: encode error=%v", err)
		}
	})
}
