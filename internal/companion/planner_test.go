package companion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
)

// testPlayerUUID 与 testCompanionUUID 是合法 UUIDv4 文本，仅测试使用。
const (
	testPlayerUUID    = "0f2a3b4c-5d6e-4f7a-8b9c-0d1e2f3a4b5c"
	testCompanionUUID = "1a2b3c4d-5e6f-4a7b-9c8d-0e1f2a3b4c5d"
	// bodyLeakMarker 是嵌进恶意响应正文的唯一标记，用于断言错误文本不回显正文。
	bodyLeakMarker = "LEAK-ME-NOT-0123456789"
)

// testSnapshot 返回一份字段全部合法的观察快照，供各测试在其上做变异。
// 快照的权威构造（server 侧 tick 边界）属后续任务，这里只覆盖类型不变量。
func testSnapshot() PlanSnapshot {
	issuer, err := core.ParsePlayerID(testPlayerUUID)
	if err != nil {
		panic(err)
	}
	companionID, err := ParseID(testCompanionUUID)
	if err != nil {
		panic(err)
	}
	return PlanSnapshot{
		Command: "去那棵橡树旁边",
		Issuer: PlanPlayer{
			ID:         issuer,
			Position:   [3]float32{8.5, 65, -1.5},
			Yaw:        0.25,
			Pitch:      -0.1,
			LookHit:    core.BlockPos{X: 9, Y: 64, Z: -1},
			HasLookHit: true,
		},
		Companion: PlanCompanion{
			ID:         companionID,
			Position:   [3]float32{6.5, 65, 0.5},
			Yaw:        3,
			Pitch:      0,
			Inventory:  core.Inventory{},
			TaskStatus: "空闲",
		},
		ExposedBlocks: []PlanBlock{
			{Pos: core.BlockPos{X: 8, Y: 63, Z: -2}, Block: core.GrassID},
			{Pos: core.BlockPos{X: 9, Y: 63, Z: -2}, Block: core.StoneID},
			{Pos: core.BlockPos{X: 9, Y: 64, Z: -1}, Block: core.OakLogID},
		},
		Heights:        []PlanHeight{{X: 8, Z: -2, Height: 63}, {X: 9, Z: -1, Height: 64}},
		ChunkRevisions: []ChunkRevision{{Chunk: core.ChunkPos{X: 0, Z: -1}, Revision: 7}},
		WorldTimeTicks: 6000,
	}
}

// wantPlanError 断言错误属于期望的哨兵类别且不同时命中另一类别。
func wantPlanError(t *testing.T, err error, want, other error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，got nil", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("错误类别错误: %v，want %v", err, want)
	}
	if errors.Is(err, other) {
		t.Fatalf("错误同时命中另一类别: %v", err)
	}
}

// chatCompletionsBody 构造一份 OpenAI 形态的响应正文，content 是模型文本。
// envelope 层携带 role 等额外字段，模拟真实 OpenAI-compatible 服务的宽容 envelope。
func chatCompletionsBody(t *testing.T, content string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": content},
		}},
	})
	if err != nil {
		t.Fatalf("构造响应正文失败: %v", err)
	}
	return string(encoded)
}

// planText 按给定的 summary 与 steps 原文拼出一份计划 JSON。
func planText(summary string, steps string) string {
	return `{"summary":` + quoteJSON(summary) + `,"steps":[` + steps + `]}`
}

// quoteJSON 把字符串编码为 JSON 字符串字面量。
func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// newTestPlanner 起一个由 handler 提供响应的假模型并构造指向它的 PlannerClient。
// apiKey 是已解析的密钥值；client 为 nil 时使用默认受控客户端。
func newTestPlanner(t *testing.T, apiKey string, client *http.Client, handler http.HandlerFunc) (*PlannerClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	planner, err := NewPlannerClient(ModelSettings{
		Endpoint:  server.URL,
		Model:     "test-model",
		APIKeyEnv: "MORNLEA_TEST_KEY",
	}, apiKey, client)
	if err != nil {
		t.Fatalf("NewPlannerClient 失败: %v", err)
	}
	return planner, server
}

// countingHandler 返回一个记录请求数的处理函数，响应由 respond 提供。
func countingHandler(count *int32, respond func(w http.ResponseWriter)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(count, 1)
		respond(w)
	}
}

// TestPlanSnapshotValidateBounds 覆盖快照全部字段的边界：指令字节上限、环境方块
// 数量与顺序、高度样本数量与取值、revision 条数与顺序、任务状态摘要长度、身份
// 有效性与浮点有限性。
func TestPlanSnapshotValidateBounds(t *testing.T) {
	if err := testSnapshot().Validate(); err != nil {
		t.Fatalf("基准快照被拒绝: %v", err)
	}

	commandOver := strings.Repeat("走", 342) // 1026 bytes > 1024
	blocksOver := make([]PlanBlock, MaxPlanExposedBlocks+1)
	for index := range blocksOver {
		blocksOver[index] = PlanBlock{Pos: core.BlockPos{X: int32(index), Y: 0, Z: 0}, Block: core.StoneID}
	}
	// 高度样本按 33×33 上界多放一条。
	heightsOver := make([]PlanHeight, MaxPlanHeightSamples+1)
	for index := range heightsOver {
		heightsOver[index] = PlanHeight{X: int32(index % 33), Z: int32(index / 33), Height: 63}
	}
	revisionsOver := make([]ChunkRevision, MaxPlanChunkRevisions+1)
	for index := range revisionsOver {
		revisionsOver[index] = ChunkRevision{Chunk: core.ChunkPos{X: int32(index - 5), Z: 0}, Revision: 1}
	}
	statusOver := strings.Repeat("忙", MaxPlanTaskStatusBytes/3+1)

	for name, mutate := range map[string]func(*PlanSnapshot){
		"指令为空":       func(s *PlanSnapshot) { s.Command = "" },
		"指令超长":       func(s *PlanSnapshot) { s.Command = commandOver },
		"指令非 UTF-8":  func(s *PlanSnapshot) { s.Command = "\xff\xfe" },
		"指令含控制字符":    func(s *PlanSnapshot) { s.Command = "走\x00" },
		"发令玩家 ID 无效": func(s *PlanSnapshot) { s.Issuer.ID = core.PlayerID{} },
		"伙伴 ID 无效":   func(s *PlanSnapshot) { s.Companion.ID = ID{} },
		"玩家位置 NaN":   func(s *PlanSnapshot) { s.Issuer.Position = [3]float32{float32(math.NaN()), 1, 1} },
		"伙伴位置 Inf":   func(s *PlanSnapshot) { s.Companion.Position = [3]float32{1, float32(math.Inf(1)), 1} },
		"玩家朝向 NaN":   func(s *PlanSnapshot) { s.Issuer.Yaw = float32(math.NaN()) },
		"环境方块超 256":  func(s *PlanSnapshot) { s.ExposedBlocks = blocksOver },
		"环境方块乱序": func(s *PlanSnapshot) {
			s.ExposedBlocks = []PlanBlock{s.ExposedBlocks[1], s.ExposedBlocks[0], s.ExposedBlocks[2]}
		},
		"环境方块坐标重复":     func(s *PlanSnapshot) { s.ExposedBlocks[1] = s.ExposedBlocks[0] },
		"环境方块为空气":      func(s *PlanSnapshot) { s.ExposedBlocks[0].Block = core.AirID },
		"环境方块未注册":      func(s *PlanSnapshot) { s.ExposedBlocks[0].Block = core.BlockID(9999) },
		"环境方块 Y 越界":    func(s *PlanSnapshot) { s.ExposedBlocks[0].Pos.Y = core.MaxY },
		"高度样本超上界":      func(s *PlanSnapshot) { s.Heights = heightsOver },
		"高度样本乱序":       func(s *PlanSnapshot) { s.Heights = []PlanHeight{{X: 9, Z: -1}, {X: 8, Z: -2}} },
		"高度样本 Y 越界":    func(s *PlanSnapshot) { s.Heights[0].Height = core.MaxY },
		"高度样本空列越界":     func(s *PlanSnapshot) { s.Heights[0].Height = core.MinY - 2 },
		"revision 超上界": func(s *PlanSnapshot) { s.ChunkRevisions = revisionsOver },
		"revision 乱序": func(s *PlanSnapshot) {
			s.ChunkRevisions = []ChunkRevision{{Chunk: core.ChunkPos{X: 1, Z: 0}, Revision: 2}, {Chunk: core.ChunkPos{X: 0, Z: 0}, Revision: 1}}
		},
		"revision 坐标重复": func(s *PlanSnapshot) {
			s.ChunkRevisions = append(s.ChunkRevisions, ChunkRevision{Chunk: core.ChunkPos{X: 0, Z: -1}, Revision: 9})
		},
		"任务状态摘要超长":      func(s *PlanSnapshot) { s.Companion.TaskStatus = statusOver },
		"任务状态摘要非 UTF-8": func(s *PlanSnapshot) { s.Companion.TaskStatus = "\xff" },
		"背包非法": func(s *PlanSnapshot) {
			s.Companion.Inventory.Backpack[0] = core.ItemStack{Item: core.ItemStone, Count: 0}
		},
	} {
		snapshot := testSnapshot()
		snapshot.ExposedBlocks = append([]PlanBlock(nil), snapshot.ExposedBlocks...)
		snapshot.Heights = append([]PlanHeight(nil), snapshot.Heights...)
		snapshot.ChunkRevisions = append([]ChunkRevision(nil), snapshot.ChunkRevisions...)
		mutate(&snapshot)
		if err := snapshot.Validate(); err == nil {
			t.Errorf("%s 被接受", name)
		}
	}
}

// TestPlanSnapshotExposedBlocksBoundedSorted 验证环境摘要的排序与截断：超过
// 256 个方块时保留按坐标确定性排序的前 256 个，同一集合以不同输入顺序进入
// 得到相同结果（确定性），且源切片不被改动。
func TestPlanSnapshotExposedBlocksBoundedSorted(t *testing.T) {
	const total = MaxPlanExposedBlocks + 44
	blocks := make([]PlanBlock, 0, total)
	random := rand.New(rand.NewSource(7))
	for index := 0; index < total; index++ {
		blocks = append(blocks, PlanBlock{
			Pos: core.BlockPos{
				X: int32(random.Intn(33)) - 16,
				Y: core.MinY + int32(random.Intn(17)),
				Z: int32(random.Intn(33)) - 16,
			},
			Block: core.BlockID(1 + random.Intn(8)),
		})
	}
	source := append([]PlanBlock(nil), blocks...)

	bounded := BoundExposedBlocks(blocks)
	if len(bounded) != MaxPlanExposedBlocks {
		t.Fatalf("保留数量 = %d，want %d", len(bounded), MaxPlanExposedBlocks)
	}
	for index := 1; index < len(bounded); index++ {
		previous, current := bounded[index-1].Pos, bounded[index].Pos
		if previous.X > current.X ||
			(previous.X == current.X && previous.Y > current.Y) ||
			(previous.X == current.X && previous.Y == current.Y && previous.Z >= current.Z) {
			t.Fatalf("方块 %d 未按 (X,Y,Z) 严格升序: %+v 后跟 %+v", index-1, previous, current)
		}
	}

	shuffled := append([]PlanBlock(nil), blocks...)
	random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	again := BoundExposedBlocks(shuffled)
	if len(again) != len(bounded) {
		t.Fatalf("两次保留数量不一致: %d vs %d", len(again), len(bounded))
	}
	for index := range bounded {
		if bounded[index] != again[index] {
			t.Fatalf("截断结果不确定：位置 %d 得到 %+v 与 %+v", index, bounded[index], again[index])
		}
	}

	// 源切片不被原地改动（调用方仍持有原始观察数据）。
	for index := range blocks {
		if blocks[index] != source[index] {
			t.Fatalf("输入切片被改动：位置 %d", index)
		}
	}
	snapshot := testSnapshot()
	snapshot.ExposedBlocks = bounded
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("截断后的快照应通过校验: %v", err)
	}
}

// TestPlannerRoundTrip 验证成功路径：请求打到 /chat/completions、携带模型名与
// Bearer 密钥头、响应被解码为拥有值的 Plan。
func TestPlannerRoundTrip(t *testing.T) {
	const apiKey = "test-secret-key"
	var gotRequest *http.Request
	var gotBody []byte
	planner, _ := newTestPlanner(t, apiKey, nil, func(w http.ResponseWriter, r *http.Request) {
		gotRequest = r
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求正文失败: %v", err)
		}
		fmt.Fprint(w, chatCompletionsBody(t, planText("前往橡树", `{"kind":"go_to","x":10,"y":64,"z":-5},{"kind":"go_to","x":12,"y":65,"z":-7}`)))
	})

	plan, err := planner.Plan(context.Background(), testSnapshot())
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	if plan.Summary != "前往橡树" {
		t.Fatalf("Summary = %q", plan.Summary)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("步骤数 = %d，want 2", len(plan.Steps))
	}
	first := plan.Steps[0]
	if first.Kind != PlanStepGoTo || first.X != 10 || first.Y != 64 || first.Z != -5 {
		t.Fatalf("第一步 = %+v", first)
	}
	if plan.Steps[1] != (PlanStep{Kind: PlanStepGoTo, X: 12, Y: 65, Z: -7}) {
		t.Fatalf("第二步 = %+v", plan.Steps[1])
	}

	if gotRequest == nil {
		t.Fatal("服务端未收到请求")
	}
	if gotRequest.Method != http.MethodPost {
		t.Fatalf("方法 = %s", gotRequest.Method)
	}
	if !strings.HasSuffix(gotRequest.URL.Path, "/chat/completions") {
		t.Fatalf("路径 = %s", gotRequest.URL.Path)
	}
	if got := gotRequest.Header.Get("Authorization"); got != "Bearer "+apiKey {
		t.Fatalf("Authorization = %q", got)
	}
	if got := gotRequest.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !strings.Contains(string(gotBody), `"model":"test-model"`) {
		t.Fatalf("请求缺少模型名: %s", gotBody)
	}
}

// TestPlannerPromptIsolation 验证规划输入的隔离性：请求正文只含固定系统提示与
// 快照 JSON，不含密钥、不含 persona 字样与其他玩家聊天文本；密钥只出现在
// Authorization 头，密钥为空时连头都不出现。
func TestPlannerPromptIsolation(t *testing.T) {
	const apiKey = "test-secret-key"
	var bodies []string
	var headers []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求正文失败: %v", err)
		}
		bodies = append(bodies, string(body))
		headers = append(headers, r.Header.Clone())
		fmt.Fprint(w, chatCompletionsBody(t, planText("原地等待", `{"kind":"go_to","x":6,"y":65,"z":0}`)))
	}))
	t.Cleanup(server.Close)

	withKey, err := NewPlannerClient(ModelSettings{
		Endpoint:  server.URL,
		Model:     "test-model",
		APIKeyEnv: "MORNLEA_TEST_KEY",
	}, apiKey, nil)
	if err != nil {
		t.Fatalf("NewPlannerClient 失败: %v", err)
	}
	withoutKey, err := NewPlannerClient(ModelSettings{
		Endpoint: server.URL,
		Model:    "test-model",
	}, "", nil)
	if err != nil {
		t.Fatalf("NewPlannerClient 失败: %v", err)
	}

	snapshot := testSnapshot()
	if _, err := withKey.Plan(context.Background(), snapshot); err != nil {
		t.Fatalf("带密钥 Plan 失败: %v", err)
	}
	if _, err := withoutKey.Plan(context.Background(), snapshot); err != nil {
		t.Fatalf("无密钥 Plan 失败: %v", err)
	}
	if len(bodies) != 2 || len(headers) != 2 {
		t.Fatalf("请求数 = %d，want 2", len(bodies))
	}

	// 密钥只出现在 Authorization 头，绝不出现在请求正文。
	if got := headers[0].Get("Authorization"); got != "Bearer "+apiKey {
		t.Fatalf("带密钥请求缺少 Authorization 头: %q", got)
	}
	if strings.Contains(bodies[0], apiKey) {
		t.Fatalf("请求正文泄漏密钥: %s", bodies[0])
	}
	// 空密钥客户端连 Authorization 头都不出现。
	if headers[1].Get("Authorization") != "" {
		t.Fatalf("空密钥仍发送 Authorization 头: %v", headers[1])
	}

	// 两次正文都必须只是「系统提示 + 快照 JSON」的确定结构。
	expectedUser, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("序列化快照失败: %v", err)
	}
	for index, body := range bodies {
		var decoded struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		strict := json.NewDecoder(strings.NewReader(body))
		strict.DisallowUnknownFields()
		if err := strict.Decode(&decoded); err != nil {
			t.Fatalf("请求体 %d 不是预期的窄 schema: %v", index, err)
		}
		if decoded.Model != "test-model" {
			t.Fatalf("请求体 %d 模型名 = %q", index, decoded.Model)
		}
		if len(decoded.Messages) != 2 {
			t.Fatalf("请求体 %d 消息数 = %d，want 2", index, len(decoded.Messages))
		}
		if decoded.Messages[0].Role != "system" || decoded.Messages[0].Content != plannerSystemPrompt {
			t.Fatalf("请求体 %d 系统提示被改动", index)
		}
		if decoded.Messages[1].Role != "user" || decoded.Messages[1].Content != string(expectedUser) {
			t.Fatalf("请求体 %d 用户消息不是快照的确定性 JSON 序列化", index)
		}
		if strings.Contains(body, "persona") || strings.Contains(body, "人设") {
			t.Fatalf("请求体 %d 含 persona 字样", index)
		}
		if strings.Contains(body, "别的玩家在聊天里说的话") {
			t.Fatalf("请求体 %d 含其他玩家聊天文本", index)
		}
	}
}

// TestPlannerDefaultClientBounded 断言默认 HTTP 客户端带 30 秒固定超时且
// PlannerRequestTimeout 常量保持 30 秒，默认客户端安装受控 transport
// （响应头上限、禁用保活）。
func TestPlannerDefaultClientBounded(t *testing.T) {
	if PlannerRequestTimeout != 30*time.Second {
		t.Fatalf("PlannerRequestTimeout = %v，want 30s", PlannerRequestTimeout)
	}
	planner, _ := newTestPlanner(t, "", nil, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chatCompletionsBody(t, planText("等待", `{"kind":"go_to","x":6,"y":65,"z":0}`)))
	})
	if got := planner.httpClient.Timeout; got != PlannerRequestTimeout {
		t.Fatalf("默认客户端超时 = %v，want %v", got, PlannerRequestTimeout)
	}
	if planner.httpClient.Transport == nil {
		t.Fatal("默认客户端未安装受控 transport（响应头上限/禁用保活）")
	}
}

// TestPlannerTimeoutFailsWithoutRetry 用注入短超时客户端模拟 30 秒超时路径：
// 请求在超时后失败，类别是 PlannerUnavailable，且服务端只收到一次请求。
func TestPlannerTimeoutFailsWithoutRetry(t *testing.T) {
	var requests int32
	planner, _ := newTestPlanner(t, "", &http.Client{Timeout: 150 * time.Millisecond},
		countingHandler(&requests, func(w http.ResponseWriter) {
			time.Sleep(800 * time.Millisecond)
			fmt.Fprint(w, chatCompletionsBody(t, planText("太慢", `{"kind":"go_to","x":6,"y":65,"z":0}`)))
		}))

	started := time.Now()
	_, err := planner.Plan(context.Background(), testSnapshot())
	if elapsed := time.Since(started); elapsed > 600*time.Millisecond {
		t.Fatalf("超时返回过慢: %v", elapsed)
	}
	wantPlanError(t, err, ErrPlannerUnavailable, ErrPlannerInvalidPlan)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("超时后请求数 = %d，want 1（不得自动重试）", got)
	}
}

// TestPlannerContextCancelReturnsCleanly 验证 context 取消路径：先确认服务端
// 收到请求再取消，Plan 干净返回 PlannerUnavailable，不悬挂、不 panic、不重发。
func TestPlannerContextCancelReturnsCleanly(t *testing.T) {
	handlerDone := make(chan struct{})
	received := make(chan struct{})
	var once sync.Once
	var requests int32
	planner, _ := newTestPlanner(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
		once.Do(func() { close(received) })
		<-handlerDone
		fmt.Fprint(w, chatCompletionsBody(t, planText("取消", `{"kind":"go_to","x":6,"y":65,"z":0}`)))
	}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := planner.Plan(ctx, testSnapshot())
		result <- err
	}()
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("服务端未收到请求")
	}
	cancel()
	select {
	case err := <-result:
		wantPlanError(t, err, ErrPlannerUnavailable, ErrPlannerInvalidPlan)
	case <-time.After(2 * time.Second):
		t.Fatal("context 取消后 Plan 未返回")
	}
	close(handlerDone)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("取消后请求数 = %d，want 1", got)
	}
}

// TestPlannerHTTPStatusFailsNoBodyLeak 验证非 2xx 响应令任务按 PlannerUnavailable
// 失败、不重试，且错误文本不含响应正文原文与密钥。
func TestPlannerHTTPStatusFailsNoBodyLeak(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound, http.StatusBadGateway} {
		var requests int32
		planner, _ := newTestPlanner(t, "test-secret-key", nil, countingHandler(&requests, func(w http.ResponseWriter) {
			w.WriteHeader(status)
			fmt.Fprintf(w, "upstream exploded %s", bodyLeakMarker)
		}))
		_, err := planner.Plan(context.Background(), testSnapshot())
		wantPlanError(t, err, ErrPlannerUnavailable, ErrPlannerInvalidPlan)
		if got := atomic.LoadInt32(&requests); got != 1 {
			t.Fatalf("状态 %d 请求数 = %d，want 1", status, got)
		}
		if strings.Contains(err.Error(), bodyLeakMarker) {
			t.Fatalf("状态 %d 错误泄漏响应正文: %v", status, err)
		}
		if strings.Contains(err.Error(), "test-secret-key") {
			t.Fatalf("状态 %d 错误泄漏密钥: %v", status, err)
		}
	}
}

// TestPlannerOversizedBodyRejected 验证 64 KiB 逐字节边界：正好 64 KiB 的正文
// 允许进入解码并成功，64 KiB+1 直接按上限拒绝归入 PlannerUnavailable（spec：
// 超限属于传输层失败类别）；超限错误不含正文原文。
func TestPlannerOversizedBodyRejected(t *testing.T) {
	buildBody := func(total int) string {
		body := chatCompletionsBody(t, planText("等待", `{"kind":"go_to","x":6,"y":65,"z":0}`))
		if len(body) >= total {
			t.Fatalf("测试构造失败：基准正文 %d 已超过目标 %d", len(body), total)
		}
		// 用 envelope 允许的未知字段把正文填充到精确长度：envelope 层对未知
		// 字段宽容（OpenAI 兼容服务会附带 id/usage 等字段），计划层才严格。
		pad := strings.Repeat("a", total-len(body)-len(`,"padding":""}`)+len(`}`))
		return body[:len(body)-1] + `,"padding":"` + pad + `"}`
	}

	var requests int32
	var response atomic.Value
	response.Store(buildBody(64 << 10))
	planner, _ := newTestPlanner(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
		fmt.Fprint(w, response.Load().(string))
	}))

	// 边界下界：正好 64 KiB 不触发超限，请求成功。
	if _, err := planner.Plan(context.Background(), testSnapshot()); err != nil {
		t.Fatalf("64 KiB 正文应放行解码: %v", err)
	}

	// 边界上界：64 KiB+1 按上限拒绝，错误不含正文。
	markerBody := strings.Replace(buildBody((64<<10)+1), "padding\":\"a", "padding\":\""+bodyLeakMarker, 1)
	response.Store(markerBody)
	_, err := planner.Plan(context.Background(), testSnapshot())
	wantPlanError(t, err, ErrPlannerUnavailable, ErrPlannerInvalidPlan)
	if strings.Contains(err.Error(), bodyLeakMarker) {
		t.Fatalf("超限错误泄漏响应正文: %v", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("a", 64)) {
		t.Fatalf("超限错误泄漏填充正文: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("请求数 = %d，want 2", got)
	}
}

// TestPlannerDecodeStrict 验证严格解码矩阵：未知字段、尾随数据、空步骤、未交付
// 步骤类型、非法数值与坐标越界全部按 InvalidPlan 失败；合法边界（Y=MinY 与
// Y=MaxY-1、int32 负极值）通过。
func TestPlannerDecodeStrict(t *testing.T) {
	const validSteps = `{"kind":"go_to","x":10,"y":64,"z":-5}`
	cases := []struct {
		name    string
		content string
		valid   bool
	}{
		{name: "合法单步", content: planText("前进", validSteps), valid: true},
		{name: "Y 等于 MinY", content: planText("贴地", `{"kind":"go_to","x":0,"y":-64,"z":0}`), valid: true},
		{name: "Y 等于 MaxY-1", content: planText("登顶", `{"kind":"go_to","x":0,"y":319,"z":0}`), valid: true},
		{name: "负坐标", content: planText("向西", `{"kind":"go_to","x":-2147483648,"y":0,"z":-1}`), valid: true},
		{name: "未知顶层字段", content: `{"summary":"前进","steps":[` + validSteps + `],"reason":"因为"}`},
		{name: "未知步骤字段", content: `{"summary":"前进","steps":[{"kind":"go_to","x":1,"y":2,"z":3,"speed":4}]}`},
		{name: "content 尾随数据", content: planText("前进", validSteps) + ` {"summary":"再来","steps":[]}`},
		{name: "空 steps", content: `{"summary":"前进","steps":[]}`},
		{name: "steps 缺席", content: `{"summary":"前进"}`},
		{name: "summary 缺席", content: `{"steps":[` + validSteps + `]}`},
		{name: "summary 为空", content: `{"summary":"","steps":[` + validSteps + `]}`},
		{name: "summary 纯空白", content: `{"summary":"   ","steps":[` + validSteps + `]}`},
		{name: "summary 超长", content: planText(strings.Repeat("长", MaxPlanSummaryBytes/3+1), validSteps)},
		{name: "kind follow", content: planText("跟随", `{"kind":"follow","x":1,"y":2,"z":3}`)},
		{name: "kind mine", content: planText("挖掘", `{"kind":"mine","x":1,"y":2,"z":3}`)},
		{name: "kind place", content: planText("放置", `{"kind":"place","x":1,"y":2,"z":3}`)},
		{name: "kind 大小写敏感", content: planText("前进", `{"kind":"GO_TO","x":1,"y":2,"z":3}`)},
		{name: "kind 缺席", content: `{"summary":"前进","steps":[{"x":1,"y":2,"z":3}]}`},
		{name: "Y 等于 MaxY", content: planText("越界", `{"kind":"go_to","x":0,"y":320,"z":0}`)},
		{name: "Y 低于 MinY", content: planText("越界", `{"kind":"go_to","x":0,"y":-65,"z":0}`)},
		{name: "X 超出 int32", content: planText("越界", `{"kind":"go_to","x":2147483648,"y":0,"z":0}`)},
		{name: "坐标非整数", content: planText("越界", `{"kind":"go_to","x":1.5,"y":2,"z":3}`)},
		{name: "坐标是字符串", content: planText("越界", `{"kind":"go_to","x":"1","y":2,"z":3}`)},
		{name: "坐标是 null", content: planText("越界", `{"kind":"go_to","x":null,"y":2,"z":3}`)},
		{name: "content 是数组", content: `[{"summary":"前进"}]`},
		{name: "content 是字符串", content: `"一路向西"`},
		{name: "content 非 JSON", content: `请前往橡树`},
	}

	for _, testCase := range cases {
		var requests int32
		planner, _ := newTestPlanner(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
			fmt.Fprint(w, chatCompletionsBody(t, testCase.content))
		}))
		plan, err := planner.Plan(context.Background(), testSnapshot())
		if testCase.valid {
			if err != nil {
				t.Fatalf("%s: 期望成功，got %v", testCase.name, err)
			}
			if len(plan.Steps) != 1 || plan.Steps[0].Kind != PlanStepGoTo {
				t.Fatalf("%s: 解码结果异常: %+v", testCase.name, plan)
			}
			continue
		}
		wantPlanError(t, err, ErrPlannerInvalidPlan, ErrPlannerUnavailable)
		if strings.Contains(err.Error(), bodyLeakMarker) {
			t.Fatalf("%s: 错误泄漏正文标记", testCase.name)
		}
		if got := atomic.LoadInt32(&requests); got != 1 {
			t.Fatalf("%s: 请求数 = %d，want 1（不重试）", testCase.name, got)
		}
	}
}

// TestPlannerEnvelopeStrict 覆盖响应 envelope 层的失败语义：非法 JSON、尾随
// 数据、choices 缺席/为空/多于一个、content 为空全部按 InvalidPlan 失败。
func TestPlannerEnvelopeStrict(t *testing.T) {
	cases := map[string]string{
		"envelope 非 JSON": `not json at all`,
		"envelope 尾随数据":   chatCompletionsBody(t, planText("前进", `{"kind":"go_to","x":1,"y":2,"z":3}`)) + ` {}`,
		"choices 缺席":      `{}`,
		"choices 为空":      `{"choices":[]}`,
		"choices 多于一个":    `{"choices":[{"message":{"content":"{}"}},{"message":{"content":"{}"}}]}`,
		"message 缺席":      `{"choices":[{}]}`,
		"content 为空字符串":   chatCompletionsBody(t, ""),
		"content 缺席":      `{"choices":[{"message":{"role":"assistant"}}]}`,
		"顶层是数组":           `[]`,
	}
	for name, body := range cases {
		var requests int32
		planner, _ := newTestPlanner(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
			fmt.Fprint(w, body)
		}))
		_, err := planner.Plan(context.Background(), testSnapshot())
		wantPlanError(t, err, ErrPlannerInvalidPlan, ErrPlannerUnavailable)
		if got := atomic.LoadInt32(&requests); got != 1 {
			t.Fatalf("%s: 请求数 = %d，want 1", name, got)
		}
	}
}

// TestPlannerUnreachableEndpoint 验证连接失败归入 PlannerUnavailable。
func TestPlannerUnreachableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close() // 立即关闭，端口不可达
	planner, err := NewPlannerClient(ModelSettings{Endpoint: server.URL, Model: "test-model"}, "", nil)
	if err != nil {
		t.Fatalf("NewPlannerClient 失败: %v", err)
	}
	_, err = planner.Plan(context.Background(), testSnapshot())
	wantPlanError(t, err, ErrPlannerUnavailable, ErrPlannerInvalidPlan)
}

// TestPlannerRejectsInvalidSettings 验证构造器拒绝非法模型设置。
func TestPlannerRejectsInvalidSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)
	if _, err := NewPlannerClient(ModelSettings{Endpoint: "", Model: "m"}, "", nil); err == nil {
		t.Fatal("空 endpoint 被接受")
	}
	if _, err := NewPlannerClient(ModelSettings{Endpoint: server.URL, Model: ""}, "", nil); err == nil {
		t.Fatal("空 model 被接受")
	}
	if _, err := NewPlannerClient(ModelSettings{Endpoint: server.URL, Model: "m"}, "", nil); err != nil {
		t.Fatalf("合法设置被拒绝: %v", err)
	}
}

// TestPlannerRejectsInvalidSnapshot 验证非法快照在发起请求前被拒绝，且服务端
// 未收到任何请求。
func TestPlannerRejectsInvalidSnapshot(t *testing.T) {
	var requests int32
	planner, _ := newTestPlanner(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
		fmt.Fprint(w, chatCompletionsBody(t, planText("前进", `{"kind":"go_to","x":1,"y":2,"z":3}`)))
	}))
	snapshot := testSnapshot()
	snapshot.Command = ""
	if _, err := planner.Plan(context.Background(), snapshot); err == nil {
		t.Fatal("非法快照被接受")
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("非法快照仍发出请求: %d", got)
	}
}
