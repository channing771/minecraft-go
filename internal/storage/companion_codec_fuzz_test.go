package storage

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
)

// fuzzTaskRecordOffsets 是携带任务载荷的单记录 v2 文件的关键偏移：固定
// 指令 "go"（2 bytes）与单步计划，使状态/步骤数/FIFO 计数字节可被稳定
// 补丁。布局变化时本表必须同步更新（与 v1 offset 测试同一纪律）。
const (
	fuzzTaskStepsCountOffset = 32 + companionRecordLength + 1 + 2 + 2
	fuzzTaskStateOffset      = fuzzTaskStepsCountOffset + 2 + 13 + 4
	fuzzTaskFIFOCountOffset  = fuzzTaskStateOffset + 1 + 1 + 8 + 8
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
		// v1 是只读迁移：解码成功即可；规范重编码必然写 v2，字节不可比对。
		if binary.LittleEndian.Uint32(payload[8:12]) == companionSchemaV1 {
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
