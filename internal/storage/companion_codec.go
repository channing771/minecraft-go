package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

const (
	companionEnvelopeVersion uint32 = 1
	// companionSchemaV1 是 M5A 的只读迁移 schema：记录只有 221-byte 身体。
	companionSchemaV1 uint32 = 1
	// companionSchemaV2 是 M5B 的只读迁移 schema：记录 = 身体 + flags +
	// 可选任务区与 FIFO 区，任务区步骤固定 13-byte 且只写过 go_to。
	// 迁移读入后按当前 schema 重写；v2 字节永不再生。
	companionSchemaV2 uint32 = 2
	// companionSchemaV3 是 M5C 的只读迁移 schema：记录 = 身体 + 可选任务
	// 区与 FIFO 区，任务区步骤按 kind 变长。v4 起记录尾部还可携带可选摘
	// 要区，v3 文件的摘要位（flags bit2）仍是保留位。迁移读入后按 v4 重
	// 写（摘要为空）；v3 字节永不再生。
	companionSchemaV3 uint32 = 3
	// companionSchemaV4 是 M5D 引入摘要区的最低 schema：v4 及以上版本的
	// 记录 flags 才允许 summary 位。独立于 currentCompanionSchema 存在，
	// 未来 v5 成为 current 时 v4 迁移文件的摘要位仍按合法解析，而不是被
	// 误判为保留位损坏。
	companionSchemaV4 uint32 = 4
	// currentCompanionSchema 是当前写出的 schema：记录 = 身体 + 可选任务
	// 区与 FIFO 区（仅 active 记录携带）+ 可选摘要区（仅 active 且有摘要
	// 时写入），任务区步骤按 kind 变长（见 companionPlanStepWireLength）。
	// 编码端只写当前版本。
	currentCompanionSchema uint32 = companionSchemaV4
	companionHeaderLength         = 32
	companionRecordLength         = 221
	// maxCompanionFileLength 是物理文件字节上界（spec：438,280）。推导：
	// v3 上界 430,080（单条 active 记录 ≤ 221 身体 + 1 flags + 任务区
	// 86,050 + FIFO 区 16,418 共 102,690；4 条 active ≈ 410,760；60 条
	// inactive 记录各 222 共 13,320；+ envelope 32 ≈ 424,112，取整上界
	// 430,080）之上，v4 为 4 条 active 记录各追加一个摘要区（u16 前缀
	// 2 + 2,048 摘要文本 = 2,050）：430,080 + 4×2,050 = 438,280。解码在
	// 任何解析与分配之前按本常量拒绝超长。
	maxCompanionFileLength = 438280
	// companionTaskCommandPrefixLength 是任务区指令的 u16 长度前缀。
	companionTaskCommandPrefixLength = 2
	// companionSummaryPrefixLength 是摘要区的 u16 长度前缀：摘要区只在
	// 有摘要时写入（空摘要不写区），前缀值因此恒为 1..2,048。
	companionSummaryPrefixLength = 2
	// companionPlanStepLength 是 go_to/mine 步骤的编码长度（kind 1 +
	// 坐标 3×int32）。v2 时代全部步骤固定为本长度；v3 起 place 在其上追加
	// block uint16（15 bytes），follow 以 16-byte 目标玩家 ID 取代坐标
	//（kind 1 + 16 = 17 bytes）。
	companionPlanStepLength = 13
)

// v2 起记录尾部的 flags 位：bit0 携带任务区、bit1 携带 FIFO 区；其余位在
// 对应 schema 中保留且必须为零——保留位非零一律按损坏拒绝，为未来 schema
// 演进留出空间。bit2（摘要区）只在 v4 及之后合法：v3/v2 文件的 bit2 仍是
// 保留位（其布局没有摘要字节可读）。
const (
	companionFlagHasTask    uint8 = 1 << 0
	companionFlagHasFIFO    uint8 = 1 << 1
	companionFlagHasSummary uint8 = 1 << 2
)

var (
	companionEnvelopeMagic = [4]byte{'M', 'C', 'A', 'I'}
	companionCRCTable      = crc32.MakeTable(crc32.Castagnoli)
)

func encodeCompanions(save CompanionSave) ([]byte, error) {
	if save.Revision == 0 {
		return nil, fmt.Errorf("%w: zero companion revision", ErrCorrupt)
	}
	if len(save.Records) > companion.MaxStored {
		return nil, fmt.Errorf("%w: companion count %d exceeds limit", ErrCorrupt, len(save.Records))
	}
	records := slices.Clone(save.Records)
	slices.SortFunc(records, func(a, b companion.Body) int {
		return bytes.Compare(a.ID[:], b.ID[:])
	})
	for index, body := range records {
		if err := validateCompanionBody(body); err != nil {
			return nil, fmt.Errorf("companion record %d: %w", index, err)
		}
		if index > 0 && records[index-1].ID == body.ID {
			return nil, fmt.Errorf("%w: duplicate companion ID", ErrCorrupt)
		}
	}
	if err := validateStoredCompanionQueues(save.Queues, records, currentCompanionSchema); err != nil {
		return nil, err
	}
	queuesByID := make(map[companion.ID]StoredCompanionQueue, len(save.Queues))
	for _, queue := range save.Queues {
		queuesByID[queue.ID] = queue
	}

	payloadLength := 0
	for _, body := range records {
		payloadLength += companionRecordLength + 1
		if queue, exists := queuesByID[body.ID]; exists {
			if queue.HasCurrent {
				payloadLength += companionTaskEncodedLength(queue.Current)
			}
			if len(queue.Pending) != 0 {
				payloadLength += companionFIFOEncodedLength(queue.Pending)
			}
			if queue.Summary != "" {
				payloadLength += companionSummaryPrefixLength + len(queue.Summary)
			}
		}
	}
	encoded := make([]byte, 0, companionHeaderLength+payloadLength)
	encoded = append(encoded, companionEnvelopeMagic[:]...)
	encoded = appendU32(encoded, companionEnvelopeVersion)
	encoded = appendU32(encoded, currentCompanionSchema)
	encoded = appendU64(encoded, save.Revision)
	encoded = appendU32(encoded, uint32(len(records)))
	encoded = appendU32(encoded, uint32(payloadLength))
	encoded = appendU32(encoded, 0)
	for _, body := range records {
		encoded = appendCompanionBody(encoded, body)
		var flags uint8
		queue, exists := queuesByID[body.ID]
		if exists && queue.HasCurrent {
			flags |= companionFlagHasTask
		}
		if exists && len(queue.Pending) != 0 {
			flags |= companionFlagHasFIFO
		}
		if exists && queue.Summary != "" {
			flags |= companionFlagHasSummary
		}
		encoded = append(encoded, flags)
		if flags&companionFlagHasTask != 0 {
			encoded = appendCompanionTask(encoded, queue.Current)
		}
		if flags&companionFlagHasFIFO != 0 {
			encoded = appendCompanionFIFO(encoded, queue.Pending)
		}
		// 摘要区追加在记录尾部：active 且有摘要才写；无摘要的 active 记录
		// 与 inactive 记录都不写——记录字节位形与 v3 完全对齐，便于审读
		// 与 v3 golden 对照。
		if flags&companionFlagHasSummary != 0 {
			encoded = appendCompanionSummary(encoded, queue.Summary)
		}
	}
	// 长度门禁的编码侧镜像：产出必须能被解码端接受，超上界（输入违反
	// active 记录假设时的防御路径）立即拒绝而不是写出不可读文件。
	if len(encoded) > companionHeaderLength+payloadLength || len(encoded) > maxCompanionFileLength {
		return nil, fmt.Errorf(
			"%w: companion file length %d exceeds limit", ErrCorrupt, len(encoded),
		)
	}
	binary.LittleEndian.PutUint32(encoded[28:], companionChecksum(encoded))
	return encoded, nil
}

// companionTaskEncodedLength 计算任务区的编码长度，供 payload 长度预算。
// 步骤按 kind 变长（companionPlanStepWireLength），其余字段定长。
func companionTaskEncodedLength(task StoredCompanionTask) int {
	length := companionTaskCommandPrefixLength + len(task.Command) + 2 + 4 + 1 + 1 + 8 + 8
	for _, step := range task.PlanSteps {
		length += companionPlanStepWireLength(step.Kind)
	}
	return length
}

// companionPlanStepWireLength 返回 v3 编码中按 kind 的步骤字节长度：
// go_to/mine 13（kind + 3×int32 坐标）、place 15（+block uint16）、
// follow 17（kind + 16-byte 目标玩家 ID，坐标不落盘）。未知 kind 无法
// 通过载荷校验到达本函数；防御性返回最大步长保证长度预算永不低估，编码
// 端的最终长度门禁与解码端的 kind 校验兜底。
func companionPlanStepWireLength(kind companion.PlanStepKind) int {
	switch kind {
	case companion.PlanStepGoTo, companion.PlanStepMine:
		return companionPlanStepLength
	case companion.PlanStepPlace:
		return companionPlanStepLength + 2
	default:
		return 1 + len(core.PlayerID{})
	}
}

// companionFIFOEncodedLength 计算 FIFO 区的编码长度（u16 计数 + 每条
// u16 前缀与字节）。
func companionFIFOEncodedLength(pending []string) int {
	length := 2
	for _, command := range pending {
		length += companionTaskCommandPrefixLength + len(command)
	}
	return length
}

// companionSchemaReadable 判断存档 schema 是否在解码入口白名单内。成员
// 显式列出 v1/v2/v3/v4 的字面常量，绝不退化成 currentCompanionSchema
// 引用：未来 v5 成为 current 时，schema=4 的合法迁移文件仍必须在入口被
// 放行，才能到达 decodeCompanionQueueSections 的 `schema >=
// companionSchemaV4` 摘要位前瞻检查——若用 current 引用，v4 文件会在入口
// 被误判 ErrCorrupt，永远到不了前瞻检查，companionSchemaV4 独立常量的
// 设计意图随之落空。新增 schema 版本时在此显式追加成员，并同步白名单
// 锁测试（TestCompanionDecodeSchemaWhitelistListsLiteralV4）。
func companionSchemaReadable(schema uint32) bool {
	return schema == companionSchemaV1 || schema == companionSchemaV2 ||
		schema == companionSchemaV3 || schema == companionSchemaV4
}

func decodeCompanions(data []byte) (StoredCompanions, error) {
	// 分配前门禁：任何超过物理上界的输入在解析前拒绝。
	if len(data) > maxCompanionFileLength {
		return StoredCompanions{}, fmt.Errorf("%w: companion file length %d exceeds limit", ErrCorrupt, len(data))
	}
	header := byteDecoder{data: data}
	if err := header.magic(companionEnvelopeMagic); err != nil {
		return StoredCompanions{}, corrupt("companion envelope magic", err)
	}
	version, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion envelope version", err)
	}
	if version != companionEnvelopeVersion {
		if version > companionEnvelopeVersion {
			return StoredCompanions{}, fmt.Errorf("%w: companion envelope version %d", ErrFutureVersion, version)
		}
		return StoredCompanions{}, fmt.Errorf("%w: unsupported companion envelope version %d", ErrCorrupt, version)
	}
	schema, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion schema", err)
	}
	if !companionSchemaReadable(schema) {
		// 拒绝路径的错误分类仍以 current 为界：比当前更新的版本是明确的
		// 前瞻信号（ErrFutureVersion）；白名单成员资格本身只看字面版本
		// （companionSchemaReadable），与 current 的取值解耦。
		if schema > currentCompanionSchema {
			return StoredCompanions{}, fmt.Errorf("%w: companion schema %d", ErrFutureVersion, schema)
		}
		return StoredCompanions{}, fmt.Errorf("%w: unsupported companion schema %d", ErrCorrupt, schema)
	}
	revision, err := header.u64()
	if err != nil {
		return StoredCompanions{}, corrupt("companion revision", err)
	}
	if revision == 0 {
		return StoredCompanions{}, fmt.Errorf("%w: zero companion revision", ErrCorrupt)
	}
	count, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion count", err)
	}
	if count > companion.MaxStored {
		return StoredCompanions{}, fmt.Errorf("%w: companion count %d exceeds limit", ErrCorrupt, count)
	}
	payloadLength, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion payload length", err)
	}
	// v1 是固定步长布局；v2 的记录长度随任务区/FIFO 变化，只约束总量。
	if schema == companionSchemaV1 && payloadLength != count*companionRecordLength {
		return StoredCompanions{}, fmt.Errorf("%w: companion payload length does not match count", ErrCorrupt)
	}
	wantCRC, err := header.u32()
	if err != nil {
		return StoredCompanions{}, corrupt("companion CRC32C", err)
	}
	if uint64(header.remaining()) != uint64(payloadLength) {
		return StoredCompanions{}, fmt.Errorf("%w: companion payload length does not match file", ErrCorrupt)
	}
	if companionChecksum(data) != wantCRC {
		return StoredCompanions{}, fmt.Errorf("%w: companion CRC32C", ErrCorrupt)
	}

	records := make([]companion.Body, int(count))
	var queues []StoredCompanionQueue
	for index := range records {
		body, err := decodeCompanionBody(&header)
		if err != nil {
			return StoredCompanions{}, fmt.Errorf("companion record %d: %w", index, err)
		}
		if index > 0 && bytes.Compare(records[index-1].ID[:], body.ID[:]) >= 0 {
			return StoredCompanions{}, fmt.Errorf("%w: companion IDs are not strictly sorted", ErrCorrupt)
		}
		records[index] = body
		if schema == companionSchemaV1 {
			continue
		}
		queue, err := decodeCompanionQueueSections(&header, schema)
		if err != nil {
			return StoredCompanions{}, fmt.Errorf("companion record %d: %w", index, err)
		}
		if queue.HasCurrent || len(queue.Pending) != 0 || queue.Summary != "" {
			queue.ID = body.ID
			queues = append(queues, queue)
		}
	}
	// 全部记录消费完毕后 payload 必须恰好读空：头部声明的 payload 长度已
	// 与文件长度核对，这里的检查封死「变长区长度被改小后残留字节」的错位
	// 形态——任何区段（指令、步骤、FIFO 条目、摘要）的前缀被缩短都会留下
	// 未消费字节，静默接受它们等于接受非规范文件（重编码字节必然不同）。
	if header.remaining() != 0 {
		return StoredCompanions{}, fmt.Errorf(
			"%w: companion payload has %d trailing bytes", ErrCorrupt, header.remaining(),
		)
	}
	return StoredCompanions{Revision: revision, Records: records, Queues: queues}, nil
}

// decodeCompanionQueueSections 解码 v2 起记录尾部的 flags 与可选任务区、
// FIFO 区、摘要区。flags 为零时返回空载荷；保留位非零按损坏拒绝——摘要
// 位（bit2）只在 v4 合法，v3/v2 文件置位即损坏。schema 决定任务区步骤的
// 解码布局（v2 固定 13B / v3 起按 kind 变长）。
func decodeCompanionQueueSections(decoder *byteDecoder, schema uint32) (StoredCompanionQueue, error) {
	flags, err := decoder.u8()
	if err != nil {
		return StoredCompanionQueue{}, corrupt("companion record flags", err)
	}
	allowed := companionFlagHasTask | companionFlagHasFIFO
	if schema >= companionSchemaV4 {
		allowed |= companionFlagHasSummary
	}
	if flags&^allowed != 0 {
		return StoredCompanionQueue{}, fmt.Errorf("%w: companion record flags %#x reserved", ErrCorrupt, flags)
	}
	var queue StoredCompanionQueue
	if flags&companionFlagHasTask != 0 {
		queue.Current, err = decodeCompanionTask(decoder, schema)
		if err != nil {
			return StoredCompanionQueue{}, err
		}
		queue.HasCurrent = true
	}
	if flags&companionFlagHasFIFO != 0 {
		pending, err := decodeCompanionFIFO(decoder)
		if err != nil {
			return StoredCompanionQueue{}, err
		}
		queue.Pending = pending
	}
	if flags&companionFlagHasSummary != 0 {
		if queue.Summary, err = decodeCompanionSummary(decoder); err != nil {
			return StoredCompanionQueue{}, err
		}
	}
	return queue, nil
}

// appendCompanionSummary 追加 v4 记录尾部的摘要区：u16 长度前缀 + 文本
// 字节。调用前必须已通过 validateStoredCompanionSummary（长度、UTF-8 与
// 无 NUL），且摘要非空——空摘要不写区（flags 不置位），保证磁盘上不存在
// 零长摘要区这一非规范位形。
func appendCompanionSummary(dst []byte, summary string) []byte {
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(summary)))
	return append(dst, summary...)
}

// decodeCompanionSummary 解码 v4 摘要区：u16 长度前缀 + 文本字节。长度
// 超过 MaxCompanionSummaryBytes、零长（编码端永不产出，非规范位形）、含
// NUL 或非法 UTF-8 一律按损坏拒绝——摘要是模型产出的自由文本，持久层不
// 猜测、不清洗、不截断。
func decodeCompanionSummary(decoder *byteDecoder) (string, error) {
	length, err := decoder.u16()
	if err != nil {
		return "", corrupt("companion summary length", err)
	}
	if length > MaxCompanionSummaryBytes {
		return "", fmt.Errorf(
			"%w: companion summary length %d exceeds limit", ErrCorrupt, length,
		)
	}
	if length == 0 {
		return "", fmt.Errorf("%w: companion summary section without text", ErrCorrupt)
	}
	text, err := decoder.take(int(length))
	if err != nil {
		return "", corrupt("companion summary", err)
	}
	if !utf8.Valid(text) {
		return "", fmt.Errorf("%w: companion summary is not valid UTF-8", ErrCorrupt)
	}
	if bytes.IndexByte(text, 0) >= 0 {
		return "", fmt.Errorf("%w: companion summary contains NUL", ErrCorrupt)
	}
	return string(text), nil
}

// appendCompanionTask 追加任务区：指令（u16 长度前缀 + 字节）、步骤数
// u16、每步按 kind 变长（见 appendCompanionPlanStep）、步骤索引 u32、
// 状态 u8、失败原因 u8、开始 tick 与 deadline 各 u64。
func appendCompanionTask(dst []byte, task StoredCompanionTask) []byte {
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(task.Command)))
	dst = append(dst, task.Command...)
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(task.PlanSteps)))
	for _, step := range task.PlanSteps {
		dst = appendCompanionPlanStep(dst, step)
	}
	dst = appendU32(dst, uint32(task.StepIndex))
	dst = append(dst, byte(task.State), byte(task.FailReason))
	dst = appendU64(dst, task.StartTick)
	dst = appendU64(dst, task.DeadlineTicks)
	return dst
}

// appendCompanionPlanStep 按 v3 变长布局追加单个计划步骤：go_to/mine 写
// kind + 3×int32 坐标；place 在坐标之后追加 block uint16；follow 只写
// kind + 16-byte 目标玩家 ID（坐标不落盘，恢复后保持零值）。调用前必须
// 已通过 validateStoredCompanionTask 的步骤校验（kind 合法且未用字段为
// 零），未用字段零值约束保证变长编码不丢数据、round-trip 精确。
func appendCompanionPlanStep(dst []byte, step companion.PlanStep) []byte {
	dst = append(dst, byte(step.Kind))
	switch step.Kind {
	case companion.PlanStepFollow:
		dst = append(dst, step.PlayerID[:]...)
	default:
		dst = appendU32(dst, uint32(step.X))
		dst = appendU32(dst, uint32(step.Y))
		dst = appendU32(dst, uint32(step.Z))
		if step.Kind == companion.PlanStepPlace {
			dst = binary.LittleEndian.AppendUint16(dst, uint16(step.Block))
		}
	}
	return dst
}

func decodeCompanionTask(decoder *byteDecoder, schema uint32) (StoredCompanionTask, error) {
	var task StoredCompanionTask
	commandLength, err := decoder.u16()
	if err != nil {
		return StoredCompanionTask{}, corrupt("companion task command length", err)
	}
	command, err := decoder.take(int(commandLength))
	if err != nil {
		return StoredCompanionTask{}, corrupt("companion task command", err)
	}
	task.Command = string(command)
	stepCount, err := decoder.u16()
	if err != nil {
		return StoredCompanionTask{}, corrupt("companion task plan length", err)
	}
	if stepCount > MaxCompanionPlanSteps {
		return StoredCompanionTask{}, fmt.Errorf(
			"%w: companion task plan steps %d exceeds limit", ErrCorrupt, stepCount,
		)
	}
	if stepCount != 0 {
		task.PlanSteps = make([]companion.PlanStep, int(stepCount))
		for index := range task.PlanSteps {
			var step companion.PlanStep
			if schema == companionSchemaV2 {
				step, err = decodeCompanionPlanStepFixed(decoder)
			} else {
				step, err = decodeCompanionPlanStepV3(decoder)
			}
			if err != nil {
				return StoredCompanionTask{}, fmt.Errorf("companion task plan step %d: %w", index, err)
			}
			task.PlanSteps[index] = step
		}
	}
	stepIndex, err := decoder.u32()
	if err != nil {
		return StoredCompanionTask{}, corrupt("companion task step index", err)
	}
	task.StepIndex = int(stepIndex)
	state, err := decoder.u8()
	if err != nil {
		return StoredCompanionTask{}, corrupt("companion task state", err)
	}
	task.State = companion.TaskState(state)
	failReason, err := decoder.u8()
	if err != nil {
		return StoredCompanionTask{}, corrupt("companion task fail reason", err)
	}
	task.FailReason = companion.TaskFailReason(failReason)
	if task.StartTick, err = decoder.u64(); err != nil {
		return StoredCompanionTask{}, corrupt("companion task start tick", err)
	}
	if task.DeadlineTicks, err = decoder.u64(); err != nil {
		return StoredCompanionTask{}, corrupt("companion task deadline", err)
	}
	if err := validateStoredCompanionTask(task, schema); err != nil {
		return StoredCompanionTask{}, err
	}
	return task, nil
}

// decodeCompanionPlanStepFixed 解码 v2 的固定 13-byte 步骤（kind + 3×int32
// 坐标）。v2 编码端只写过 go_to，非 go_to 的 kind 字节由载荷校验按损坏
// 拒绝——这里不做 kind 分派，保持既有路径逐字节不变。
func decodeCompanionPlanStepFixed(decoder *byteDecoder) (companion.PlanStep, error) {
	kind, err := decoder.u8()
	if err != nil {
		return companion.PlanStep{}, corrupt("companion plan step kind", err)
	}
	x, err := decoder.u32()
	if err != nil {
		return companion.PlanStep{}, corrupt("companion plan step X", err)
	}
	y, err := decoder.u32()
	if err != nil {
		return companion.PlanStep{}, corrupt("companion plan step Y", err)
	}
	z, err := decoder.u32()
	if err != nil {
		return companion.PlanStep{}, corrupt("companion plan step Z", err)
	}
	return companion.PlanStep{
		Kind: companion.PlanStepKind(kind),
		X:    int32(x),
		Y:    int32(y),
		Z:    int32(z),
	}, nil
}

// decodeCompanionPlanStepV3 解码 v3 的变长步骤：先读 kind，再按 kind 读取
// 专属载荷；非法 kind 在步骤处立即拒绝，绝不猜测步长——变长布局下按猜测
// 长度继续会把后续字段整体读错。未使用字段保持零值，与 PlanStep 的
// 「字段按 kind 使用」契约一致。
func decodeCompanionPlanStepV3(decoder *byteDecoder) (companion.PlanStep, error) {
	kind, err := decoder.u8()
	if err != nil {
		return companion.PlanStep{}, corrupt("companion plan step kind", err)
	}
	step := companion.PlanStep{Kind: companion.PlanStepKind(kind)}
	switch step.Kind {
	case companion.PlanStepGoTo, companion.PlanStepMine, companion.PlanStepPlace:
		if step.X, err = decodeStepCoord(decoder, "X"); err != nil {
			return companion.PlanStep{}, err
		}
		if step.Y, err = decodeStepCoord(decoder, "Y"); err != nil {
			return companion.PlanStep{}, err
		}
		if step.Z, err = decodeStepCoord(decoder, "Z"); err != nil {
			return companion.PlanStep{}, err
		}
		if step.Kind == companion.PlanStepPlace {
			block, err := decoder.u16()
			if err != nil {
				return companion.PlanStep{}, corrupt("companion plan step block", err)
			}
			step.Block = core.BlockID(block)
		}
	case companion.PlanStepFollow:
		playerID, err := decoder.take(len(core.PlayerID{}))
		if err != nil {
			return companion.PlanStep{}, corrupt("companion plan step player ID", err)
		}
		copy(step.PlayerID[:], playerID)
	default:
		return companion.PlanStep{}, fmt.Errorf(
			"%w: companion plan step kind %d is not delivered", ErrCorrupt, kind,
		)
	}
	return step, nil
}

// decodeStepCoord 读取步骤的一个 int32 坐标分量。
func decodeStepCoord(decoder *byteDecoder, field string) (int32, error) {
	value, err := decoder.u32()
	if err != nil {
		return 0, corrupt("companion plan step "+field, err)
	}
	return int32(value), nil
}

// appendCompanionFIFO 追加 FIFO 区：u16 计数 + 每条指令（u16 前缀 + 字节）。
func appendCompanionFIFO(dst []byte, pending []string) []byte {
	dst = binary.LittleEndian.AppendUint16(dst, uint16(len(pending)))
	for _, command := range pending {
		dst = binary.LittleEndian.AppendUint16(dst, uint16(len(command)))
		dst = append(dst, command...)
	}
	return dst
}

func decodeCompanionFIFO(decoder *byteDecoder) ([]string, error) {
	count, err := decoder.u16()
	if err != nil {
		return nil, corrupt("companion FIFO count", err)
	}
	if count > MaxCompanionFIFOEntries {
		return nil, fmt.Errorf("%w: companion FIFO depth %d exceeds limit", ErrCorrupt, count)
	}
	if count == 0 {
		return nil, fmt.Errorf("%w: companion FIFO section without entries", ErrCorrupt)
	}
	pending := make([]string, int(count))
	for index := range pending {
		length, err := decoder.u16()
		if err != nil {
			return nil, corrupt("companion FIFO entry length", err)
		}
		entry, err := decoder.take(int(length))
		if err != nil {
			return nil, corrupt("companion FIFO entry", err)
		}
		pending[index] = string(entry)
		if err := companion.TaskCommand(pending[index]).Validate(); err != nil {
			return nil, fmt.Errorf("companion FIFO entry %d: %w: %v", index, ErrCorrupt, err)
		}
	}
	return pending, nil
}

func appendCompanionBody(dst []byte, body companion.Body) []byte {
	dst = append(dst, body.ID[:]...)
	dst = appendU32(dst, uint32(body.Dimension))
	for _, value := range body.Position {
		dst = appendF32(dst, value)
	}
	dst = appendF32(dst, body.Yaw)
	dst = appendF32(dst, body.Pitch)
	dst = append(dst, body.Inventory.Hotbar.Selected)
	for _, stack := range body.Inventory.Hotbar.Slots {
		dst = appendPlayerStack(dst, stack)
	}
	for _, stack := range body.Inventory.Backpack {
		dst = appendPlayerStack(dst, stack)
	}
	return dst
}

func decodeCompanionBody(decoder *byteDecoder) (companion.Body, error) {
	idBytes, err := decoder.take(len(companion.ID{}))
	if err != nil {
		return companion.Body{}, corrupt("companion ID", err)
	}
	var body companion.Body
	copy(body.ID[:], idBytes)
	dimension, err := decoder.u32()
	if err != nil {
		return companion.Body{}, corrupt("companion dimension", err)
	}
	body.Dimension = core.DimensionID(int32(dimension))
	for index := range body.Position {
		body.Position[index], err = decodeF32(decoder)
		if err != nil {
			return companion.Body{}, corrupt("companion position", err)
		}
	}
	if body.Yaw, err = decodeF32(decoder); err != nil {
		return companion.Body{}, corrupt("companion yaw", err)
	}
	if body.Pitch, err = decodeF32(decoder); err != nil {
		return companion.Body{}, corrupt("companion pitch", err)
	}
	if body.Inventory.Hotbar.Selected, err = decoder.u8(); err != nil {
		return companion.Body{}, corrupt("companion selected slot", err)
	}
	for index := range body.Inventory.Hotbar.Slots {
		body.Inventory.Hotbar.Slots[index], err = decodePlayerStack(decoder)
		if err != nil {
			return companion.Body{}, corrupt("companion hotbar slot", err)
		}
	}
	for index := range body.Inventory.Backpack {
		body.Inventory.Backpack[index], err = decodePlayerStack(decoder)
		if err != nil {
			return companion.Body{}, corrupt("companion backpack slot", err)
		}
	}
	if err := validateCompanionBody(body); err != nil {
		return companion.Body{}, err
	}
	return body, nil
}

func validateCompanionBody(body companion.Body) error {
	if !body.ID.Valid() {
		return fmt.Errorf("%w: invalid companion ID", ErrCorrupt)
	}
	if body.Dimension != core.Overworld {
		return fmt.Errorf("%w: unsupported companion dimension %d", ErrCorrupt, body.Dimension)
	}
	for _, value := range body.Position {
		if !finitePlayerFloat(value) {
			return fmt.Errorf("%w: non-finite companion position", ErrCorrupt)
		}
	}
	if !finitePlayerFloat(body.Yaw) {
		return fmt.Errorf("%w: non-finite companion yaw", ErrCorrupt)
	}
	if !finitePlayerFloat(body.Pitch) || body.Pitch < -math.Pi/2 || body.Pitch > math.Pi/2 {
		return fmt.Errorf("%w: invalid companion pitch", ErrCorrupt)
	}
	if !body.Inventory.Valid() {
		return fmt.Errorf("%w: invalid companion inventory", ErrCorrupt)
	}
	return nil
}

func companionChecksum(data []byte) uint32 {
	hasher := crc32.New(companionCRCTable)
	_, _ = hasher.Write(data[8:28])
	_, _ = hasher.Write(data[companionHeaderLength:])
	return hasher.Sum32()
}
