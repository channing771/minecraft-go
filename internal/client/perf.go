package client

import (
	"errors"
	"math"
	"slices"
	"time"
)

var ErrRSSUnsupported = errors.New("当前平台不支持进程 RSS 采样")

// FrameSample 是固定场景的一帧性能样本。
type FrameSample struct {
	FrameMS           float64
	CandidateSections int
	CandidateBytes    int
	CandidateFaces    int
	PendingUploads    int
}

// PerfSampler 使用预分配的环形缓冲记录帧，不在热路径分配。
type PerfSampler struct {
	samples []FrameSample
	next    int
	count   int
	dropped int
}

func NewPerfSampler(capacity int) *PerfSampler {
	if capacity < 1 {
		capacity = 1
	}
	return &PerfSampler{samples: make([]FrameSample, capacity)}
}

func (s *PerfSampler) Add(sample FrameSample) {
	s.samples[s.next] = sample
	s.next = (s.next + 1) % len(s.samples)
	if s.count < len(s.samples) {
		s.count++
	} else {
		s.dropped++
	}
}

func (s *PerfSampler) Reset() {
	s.next = 0
	s.count = 0
	s.dropped = 0
}

// PhaseSummary 是一个固定阶段的可比较摘要。
type PhaseSummary struct {
	Frames                   int     `json:"frames"`
	FPS                      float64 `json:"fps"`
	P50MS                    float64 `json:"p50_ms"`
	P95MS                    float64 `json:"p95_ms"`
	P99MS                    float64 `json:"p99_ms"`
	MaxMS                    float64 `json:"max_ms"`
	PeakRSSBytes             uint64  `json:"peak_rss_bytes"`
	MeanCandidateSections    float64 `json:"mean_candidate_sections"`
	MeanCandidateBytes       float64 `json:"mean_candidate_bytes"`
	MeanCandidateFaces       float64 `json:"mean_candidate_faces"`
	MaxPendingUploads        int     `json:"max_pending_uploads"`
	DroppedRingBufferSamples int     `json:"dropped_ring_buffer_samples,omitempty"`
}

// PersistenceSummary 汇总 benchmark 期间完成的存档批次耗时。
type PersistenceSummary struct {
	Snapshots int64   `json:"snapshots"`
	P50MS     float64 `json:"p50_ms"`
	P95MS     float64 `json:"p95_ms"`
	P99MS     float64 `json:"p99_ms"`
	MaxMS     float64 `json:"max_ms"`
}

// ProtocolSummary 汇总固定协议探针的尾延迟与编码字节数。
type ProtocolSummary struct {
	EncodeP99MS float64 `json:"encode_p99_ms"`
	DecodeP99MS float64 `json:"decode_p99_ms"`
	Bytes       uint64  `json:"bytes"`
}

// LatencySummary 是固定容量延迟探针的稳定 JSON 摘要。
type LatencySummary struct {
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	P99MS   float64 `json:"p99_ms"`
	MaxMS   float64 `json:"max_ms"`
}

// MultiplayerSummary 汇总固定八玩家场景的客户端、服务端和 GPU 提交指标。
type MultiplayerSummary struct {
	RemoteStateEncode   LatencySummary `json:"remote_state_encode"`
	RemoteStateDecode   LatencySummary `json:"remote_state_decode"`
	InterestDiff        LatencySummary `json:"interest_diff"`
	RosterApply         LatencySummary `json:"roster_apply"`
	Interpolation       LatencySummary `json:"interpolation"`
	AvatarSubmit        LatencySummary `json:"avatar_submit"`
	NameTagSubmit       LatencySummary `json:"name_tag_submit"`
	RemoteGPUComplete   LatencySummary `json:"remote_gpu_complete"`
	ServerOutboundBytes uint64         `json:"server_outbound_bytes"`
	OutboxHighWater     int            `json:"outbox_high_water"`
	PlayerJobsHighWater int            `json:"player_jobs_high_water"`
	PlayerDoneHighWater int            `json:"player_done_high_water"`
	PeakRSSBytes        uint64         `json:"peak_rss_bytes"`
}

// LatencyRecorder 在 Add 热路径中只覆写预分配的环形缓冲；Summary 才复制并排序。
type LatencyRecorder struct {
	samples []time.Duration
	next    int
	count   int
}

func NewLatencyRecorder(capacity int) *LatencyRecorder {
	if capacity < 1 {
		capacity = 1
	}
	return &LatencyRecorder{samples: make([]time.Duration, capacity)}
}

func (recorder *LatencyRecorder) Add(value time.Duration) {
	recorder.samples[recorder.next] = value
	recorder.next = (recorder.next + 1) % len(recorder.samples)
	if recorder.count < len(recorder.samples) {
		recorder.count++
	}
}

func (recorder *LatencyRecorder) Reset() {
	recorder.next = 0
	recorder.count = 0
}

func (recorder *LatencyRecorder) Summary() LatencySummary {
	if recorder.count == 0 {
		return LatencySummary{}
	}
	values := make([]time.Duration, recorder.count)
	copy(values, recorder.samples[:recorder.count])
	slices.Sort(values)
	milliseconds := func(value time.Duration) float64 {
		return float64(value.Nanoseconds()) / float64(time.Millisecond)
	}
	return LatencySummary{
		Samples: len(values),
		P50MS:   milliseconds(values[percentileIndex(len(values), 0.50)]),
		P95MS:   milliseconds(values[percentileIndex(len(values), 0.95)]),
		P99MS:   milliseconds(values[percentileIndex(len(values), 0.99)]),
		MaxMS:   milliseconds(values[len(values)-1]),
	}
}

func percentileIndex(length int, percentile float64) int {
	index := int(math.Ceil(percentile*float64(length))) - 1
	return max(0, min(index, length-1))
}

func (s *PerfSampler) Summary(peakRSS uint64) PhaseSummary {
	if s.count == 0 {
		return PhaseSummary{PeakRSSBytes: peakRSS}
	}
	ordered := make([]FrameSample, s.count)
	start := 0
	if s.count == len(s.samples) {
		start = s.next
	}
	for i := range s.count {
		ordered[i] = s.samples[(start+i)%len(s.samples)]
	}

	durations := make([]float64, len(ordered))
	var totalMS, sections, bytes, faces float64
	maxPending := 0
	for i, sample := range ordered {
		durations[i] = sample.FrameMS
		totalMS += sample.FrameMS
		sections += float64(sample.CandidateSections)
		bytes += float64(sample.CandidateBytes)
		faces += float64(sample.CandidateFaces)
		maxPending = max(maxPending, sample.PendingUploads)
	}
	slices.Sort(durations)
	n := float64(len(ordered))
	return PhaseSummary{
		Frames:                   len(ordered),
		FPS:                      n / (totalMS / 1000),
		P50MS:                    percentile(durations, 0.50),
		P95MS:                    percentile(durations, 0.95),
		P99MS:                    percentile(durations, 0.99),
		MaxMS:                    durations[len(durations)-1],
		PeakRSSBytes:             peakRSS,
		MeanCandidateSections:    sections / n,
		MeanCandidateBytes:       bytes / n,
		MeanCandidateFaces:       faces / n,
		MaxPendingUploads:        maxPending,
		DroppedRingBufferSamples: s.dropped,
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	index = max(0, min(index, len(sorted)-1))
	return sorted[index]
}

// PerfReport 是 cmd/mcgo 与 cmd/perfcheck 共用的稳定 JSON 格式。
type PerfReport struct {
	ScenarioVersion   int                     `json:"scenario_version"`
	Transport         string                  `json:"transport,omitempty"`
	Hardware          string                  `json:"hardware"`
	OS                string                  `json:"os"`
	GoVersion         string                  `json:"go_version"`
	GitCommit         string                  `json:"git_commit"`
	Framebuffer       string                  `json:"framebuffer"`
	LoadSeconds       float64                 `json:"load_seconds"`
	SnapshotSeconds   float64                 `json:"snapshot_seconds"`
	Phases            map[string]PhaseSummary `json:"phases"`
	Ticks             PhaseSummary            `json:"ticks"`
	Persistence       PersistenceSummary      `json:"persistence"`
	Protocol          ProtocolSummary         `json:"protocol,omitempty"`
	PlayerPersistence PersistenceSummary      `json:"player_persistence,omitempty"`
	Multiplayer       MultiplayerSummary      `json:"multiplayer"`
}
