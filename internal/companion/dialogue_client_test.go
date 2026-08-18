// DialogueClient 的 HTTP 半部测试：与 planner_test.go 同一纪律——httptest 假
// 模型、不开前台窗口。覆盖：请求正文只含四类有界数据 + 固定提示（与 Planner
// 提示隔离、不含密钥）、非终态/终态成功路径、5xx/超时/超 64 KiB/畸形正文错误
// 路径、错误不含密钥与正文原文、越界请求在发起前被拒、默认客户端 30 秒超时。
package companion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// dialogueLeakMarker 是嵌进恶意响应正文的唯一标记，用于断言错误文本不回显正文。
const dialogueLeakMarker = "DIALOGUE-LEAK-0123456789"

// newTestDialogueClient 起一个由 handler 提供响应的假模型并构造指向它的
// DialogueClient。apiKey 是已解析的密钥值；client 为 nil 时使用默认受控客户端。
func newTestDialogueClient(
	t *testing.T,
	apiKey string,
	client *http.Client,
	handler http.HandlerFunc,
) (*DialogueClient, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	dialogue, err := NewDialogueClient(ModelSettings{
		Endpoint:  server.URL,
		Model:     "test-model",
		APIKeyEnv: "MORNLEA_TEST_KEY",
	}, apiKey, client)
	if err != nil {
		t.Fatalf("NewDialogueClient 失败: %v", err)
	}
	return dialogue, server
}

// wantDialogueUnavailable 断言错误属于传输层类别且不同时命中输出非法类别。
func wantDialogueUnavailable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，got nil", ErrDialogueUnavailable)
	}
	if !errors.Is(err, ErrDialogueUnavailable) {
		t.Fatalf("错误类别错误: %v，want %v", err, ErrDialogueUnavailable)
	}
	if errors.Is(err, ErrDialogueInvalidResponse) {
		t.Fatalf("错误同时命中另一类别: %v", err)
	}
}

// testDialogueRequest 返回一份字段全部合法的台词请求（开始节点）。
func testDialogueRequest(t *testing.T) DialogueRequest {
	t.Helper()
	request, err := NewDialogueRequest(
		"沉稳寡言的老向导，说话简短。",
		"上次帮玩家修好了木桥。",
		DialogueNode{Kind: DialogueNodeStart},
		testDialogueEnv(),
	)
	if err != nil {
		t.Fatalf("构造测试请求失败: %v", err)
	}
	return request
}

// decodeDialogueRequestBody 把请求正文严格解码为（消息, 模型名）形态，供各
// 测试断言请求形状；DisallowUnknownFields 锁定请求体不携带 model/messages
// 之外的额外旋钮，解码失败直接终结测试。
func decodeDialogueRequestBody(t *testing.T, body []byte) (messages []chatMessage, model string) {
	t.Helper()
	var decoded struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}
	strict := json.NewDecoder(strings.NewReader(string(body)))
	strict.DisallowUnknownFields()
	if err := strict.Decode(&decoded); err != nil {
		t.Fatalf("请求正文不是预期的窄 schema: %v\n%s", err, body)
	}
	return decoded.Messages, decoded.Model
}

// TestDialogueClientRoundTripAndIsolation 验证成功路径的请求形状：POST
// /chat/completions、恰好两条消息、系统提示是与 Planner 完全隔离的固定模板、
// 用户消息只含 persona/summary/node/env 四类有界数据、密钥只出现在
// Authorization 头、同一请求两次发送字节级一致（确定性）。
func TestDialogueClientRoundTripAndIsolation(t *testing.T) {
	const apiKey = "test-secret-key"
	var bodies []string
	var headers []http.Header
	dialogue, _ := newTestDialogueClient(t, apiKey, nil, func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取请求正文失败: %v", err)
		}
		bodies = append(bodies, string(body))
		headers = append(headers, r.Header.Clone())
		if r.Method != http.MethodPost {
			t.Errorf("方法 = %s，want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("路径 = %s", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		_, _ = io.WriteString(w, chatCompletionsBody(t, `{"line":"我出发了"}`))
	})

	request := testDialogueRequest(t)
	if _, _, err := dialogue.Do(context.Background(), request, false); err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	if _, _, err := dialogue.Do(context.Background(), request, false); err != nil {
		t.Fatalf("第二次 Do 失败: %v", err)
	}
	if len(bodies) != 2 || len(headers) != 2 {
		t.Fatalf("请求数 = %d，want 2", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("同一请求两次发送的正文不一致:\n%s\n%s", bodies[0], bodies[1])
	}
	if got := headers[0].Get("Authorization"); got != "Bearer "+apiKey {
		t.Fatalf("Authorization = %q", got)
	}

	messages, model := decodeDialogueRequestBody(t, []byte(bodies[0]))
	if model != "test-model" {
		t.Fatalf("请求模型名 = %q", model)
	}
	if len(messages) != 2 {
		t.Fatalf("消息数 = %d，want 2（system + user）", len(messages))
	}
	if messages[0].Role != "system" || messages[0].Content != dialogueSystemPrompt {
		t.Fatalf("系统提示被改动或角色错误: role=%s", messages[0].Role)
	}
	// 与 Planner 提示完全隔离：不携带规划格式、注册表词表或规划措辞。
	if strings.Contains(messages[0].Content, plannerSystemPromptHead) ||
		strings.Contains(messages[0].Content, "行动规划器") ||
		strings.Contains(messages[0].Content, `"kind":"go_to"`) {
		t.Fatalf("Dialogue 系统提示泄漏 Planner 提示内容: %s", messages[0].Content)
	}
	if messages[1].Role != "user" {
		t.Fatalf("第二条消息角色 = %s，want user", messages[1].Role)
	}

	// 密钥绝不进入请求正文。
	if strings.Contains(bodies[0], apiKey) {
		t.Fatalf("请求正文泄漏密钥: %s", bodies[0])
	}

	// 用户消息顶层恰好四类有界数据：persona、summary、node、env。
	user := decodeUserObject(t, messages[1].Content)
	assertDialogueUserFields(t, user)
	assertStringField(t, user, "persona", "沉稳寡言的老向导，说话简短。")
	assertStringField(t, user, "summary", "上次帮玩家修好了木桥。")
	node, ok := user["node"].(map[string]any)
	if !ok {
		t.Fatalf("node 不是 object: %v", user["node"])
	}
	if got, _ := node["kind"].(string); got != "start" {
		t.Fatalf("node.kind = %v，want start", node["kind"])
	}
	if len(node) != 1 {
		t.Fatalf("开始节点携带专属外字段: %v", node)
	}
	env, ok := user["env"].(map[string]any)
	if !ok {
		t.Fatalf("env 不是 object: %v", user["env"])
	}
	if len(env) != 2 {
		t.Fatalf("env 字段集 = %v，want exposed_blocks/heights", env)
	}
}

// decodeUserObject 把用户消息解码为通用 object；顶层不是 object 即失败。
func decodeUserObject(t *testing.T, content string) map[string]any {
	t.Helper()
	var user map[string]any
	if err := json.NewDecoder(strings.NewReader(content)).Decode(&user); err != nil {
		t.Fatalf("用户消息不是合法 JSON object: %v\n%s", err, content)
	}
	return user
}

// assertDialogueUserFields 锁定用户消息顶层键集合恰好为四类有界数据，未来
// 无声追加任何新键（key、聊天文本、存档路径……）都会在此失败。
func assertDialogueUserFields(t *testing.T, user map[string]any) {
	t.Helper()
	want := map[string]bool{"persona": true, "summary": true, "node": true, "env": true}
	if len(user) != len(want) {
		t.Fatalf("用户消息顶层键集 = %v，want %v", user, want)
	}
	for key := range user {
		if !want[key] {
			t.Fatalf("用户消息出现四类数据之外的键 %q", key)
		}
	}
}

// assertStringField 断言顶层字符串字段值。
func assertStringField(t *testing.T, user map[string]any, key, want string) {
	t.Helper()
	got, ok := user[key].(string)
	if !ok || got != want {
		t.Fatalf("字段 %s = %v，want %q", key, user[key], want)
	}
}

// TestDialogueClientNonTerminalAndTerminalSuccess 验证两条成功路径：非终态
// 响应只解出 line（summary 为空串）；终态响应解出 line 与 summary。假模型按
// 请求节点类别文本区分响应形态（与 server 侧假台词模型同一约定）。
func TestDialogueClientNonTerminalAndTerminalSuccess(t *testing.T) {
	dialogue, _ := newTestDialogueClient(t, "", nil, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// 用户消息是嵌套 JSON 字符串（外层引号被转义），先解出 content 再判
		// 断节点类别，避免匹配转义形态。
		userContent := ""
		if messages, _ := decodeDialogueRequestBodySoft(body); len(messages) == 2 {
			userContent = messages[1].Content
		}
		content := `{"line":"修好了","summary":"帮玩家修好了木桥，顺手清了碎料"}`
		if !strings.Contains(userContent, `"kind":"terminal"`) {
			content = `{"line":"修好了"}`
		}
		_, _ = io.WriteString(w, chatCompletionsBody(t, content))
	})

	line, summary, err := dialogue.Do(context.Background(), testDialogueRequest(t), false)
	if err != nil {
		t.Fatalf("非终态 Do 失败: %v", err)
	}
	if line != "修好了" {
		t.Fatalf("非终态 line = %q", line)
	}
	if summary != "" {
		t.Fatalf("非终态 summary = %q，want 空串", summary)
	}

	terminalRequest, err := NewDialogueRequest("", "",
		DialogueNode{Kind: DialogueNodeTerminal, State: TaskCompleted},
		DialogueEnvDigest{})
	if err != nil {
		t.Fatalf("构造终态请求失败: %v", err)
	}
	line, summary, err = dialogue.Do(context.Background(), terminalRequest, true)
	if err != nil {
		t.Fatalf("终态 Do 失败: %v", err)
	}
	if line != "修好了" || summary != "帮玩家修好了木桥，顺手清了碎料" {
		t.Fatalf("终态解码结果 line=%q summary=%q", line, summary)
	}
}

// TestDialogueClientTerminalRequestCarriesNodeIdentity 验证终态请求的用户
// 消息携带节点身份（类别与终态枚举文本），失败终态还携带稳定原因枚举。
func TestDialogueClientTerminalRequestCarriesNodeIdentity(t *testing.T) {
	var userContent string
	dialogue, _ := newTestDialogueClient(t, "", nil, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		messages, _ := decodeDialogueRequestBodySoft(body)
		if len(messages) == 2 {
			userContent = messages[1].Content
		}
		_, _ = io.WriteString(w, chatCompletionsBody(t, `{"line":"失败收尾","summary":"任务失败"}`))
	})
	failedRequest, err := NewDialogueRequest("", "",
		DialogueNode{Kind: DialogueNodeTerminal, State: TaskFailed, Reason: TaskFailPathUnreachable},
		DialogueEnvDigest{})
	if err != nil {
		t.Fatalf("构造失败终态请求失败: %v", err)
	}
	if _, _, err := dialogue.Do(context.Background(), failedRequest, true); err != nil {
		t.Fatalf("终态 Do 失败: %v", err)
	}
	user := decodeUserObject(t, userContent)
	assertDialogueUserFields(t, user)
	node, _ := user["node"].(map[string]any)
	if got, _ := node["kind"].(string); got != "terminal" {
		t.Fatalf("node.kind = %v，want terminal", node["kind"])
	}
	if got, _ := node["state"].(string); got != "failed" {
		t.Fatalf("node.state = %v，want failed", node["state"])
	}
	if got, _ := node["reason"].(string); got != "path_unreachable" {
		t.Fatalf("node.reason = %v，want path_unreachable", node["reason"])
	}
}

// decodeDialogueRequestBodySoft 是 handler 内捕获消息用的非终结版本：解码
// 失败返回空结果，由调用方后续断言兜底。
func decodeDialogueRequestBodySoft(body []byte) ([]chatMessage, string) {
	var decoded struct {
		Model    string        `json:"model"`
		Messages []chatMessage `json:"messages"`
	}
	if err := json.NewDecoder(strings.NewReader(string(body))).Decode(&decoded); err != nil {
		return nil, ""
	}
	return decoded.Messages, decoded.Model
}

// TestDialogueClientHTTPStatusFailsNoLeak 验证非 2xx 响应归入传输层失败、不
// 重试，且错误文本不含响应正文原文与密钥。
func TestDialogueClientHTTPStatusFailsNoLeak(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusNotFound, http.StatusBadGateway} {
		var requests int32
		dialogue, _ := newTestDialogueClient(t, "test-secret-key", nil, countingHandler(&requests, func(w http.ResponseWriter) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, "upstream exploded "+dialogueLeakMarker)
		}))
		_, _, err := dialogue.Do(context.Background(), testDialogueRequest(t), false)
		wantDialogueUnavailable(t, err)
		if got := atomic.LoadInt32(&requests); got != 1 {
			t.Fatalf("状态 %d 请求数 = %d，want 1（不得自动重试）", status, got)
		}
		if strings.Contains(err.Error(), dialogueLeakMarker) {
			t.Fatalf("状态 %d 错误泄漏响应正文: %v", status, err)
		}
		if strings.Contains(err.Error(), "test-secret-key") {
			t.Fatalf("状态 %d 错误泄漏密钥: %v", status, err)
		}
	}
}

// TestDialogueClientTimeoutFailsWithoutRetry 用注入短超时客户端模拟 30 秒
// 超时路径：请求在超时后失败，类别是传输层失败，且服务端只收到一次请求。
func TestDialogueClientTimeoutFailsWithoutRetry(t *testing.T) {
	var requests int32
	dialogue, _ := newTestDialogueClient(t, "", &http.Client{Timeout: 150 * time.Millisecond},
		countingHandler(&requests, func(w http.ResponseWriter) {
			time.Sleep(800 * time.Millisecond)
			_, _ = io.WriteString(w, chatCompletionsBody(t, `{"line":"太慢"}`))
		}))

	started := time.Now()
	_, _, err := dialogue.Do(context.Background(), testDialogueRequest(t), false)
	if elapsed := time.Since(started); elapsed > 600*time.Millisecond {
		t.Fatalf("超时返回过慢: %v", elapsed)
	}
	wantDialogueUnavailable(t, err)
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("超时后请求数 = %d，want 1（不得自动重试）", got)
	}
}

// TestDialogueClientOversizedBodyRejected 验证 64 KiB 逐字节边界：正好 64 KiB
// 的正文放行解码并成功，64 KiB+1 按传输层上限拒绝；超限错误不含正文原文。
func TestDialogueClientOversizedBodyRejected(t *testing.T) {
	buildBody := func(total int, marker bool) string {
		body := chatCompletionsBody(t, `{"line":"等待"}`)
		if len(body) >= total {
			t.Fatalf("测试构造失败：基准正文 %d 已超过目标 %d", len(body), total)
		}
		// envelope 层对未知字段宽容（OpenAI 兼容服务会附带 id/usage 等字段），
		// 用 padding 字段把正文填充到精确长度。
		pad := dialogueLeakMarker + strings.Repeat("a", total-len(body)-len(`,"padding":""}`)+len(`}`)-len(dialogueLeakMarker))
		if !marker {
			pad = strings.Repeat("a", total-len(body)-len(`,"padding":""}`)+len(`}`))
		}
		return body[:len(body)-1] + `,"padding":"` + pad + `"}`
	}

	var requests int32
	var response atomic.Value
	response.Store(buildBody(64<<10, false))
	dialogue, _ := newTestDialogueClient(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, response.Load().(string))
	}))

	// 边界下界：正好 64 KiB 不触发超限，请求成功。
	if _, _, err := dialogue.Do(context.Background(), testDialogueRequest(t), false); err != nil {
		t.Fatalf("64 KiB 正文应放行解码: %v", err)
	}

	// 边界上界：64 KiB+1 按上限拒绝，错误不含正文。
	response.Store(buildBody((64<<10)+1, true))
	_, _, err := dialogue.Do(context.Background(), testDialogueRequest(t), false)
	wantDialogueUnavailable(t, err)
	if strings.Contains(err.Error(), dialogueLeakMarker) {
		t.Fatalf("超限错误泄漏响应正文: %v", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("a", 64)) {
		t.Fatalf("超限错误泄漏填充正文: %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("请求数 = %d，want 2", got)
	}
}

// TestDialogueClientMalformedResponses 覆盖模型输出非法矩阵：envelope 层失败
// （非 JSON、尾随数据、choices 数量、content 为空）与 content 层失败（未知
// 字段、缺 line、终态缺 summary、非终态带 summary、台词超长）全部按输出
// 非法类别失败，且不重试、不泄漏正文标记。
func TestDialogueClientMalformedResponses(t *testing.T) {
	overlongLine := `{"line":"` + strings.Repeat("长", MaxDialogueLineBytes/3+1) + `"}` // 3*(N)+2 引号 > 256。
	cases := map[string]struct {
		body     string
		terminal bool
	}{
		"envelope 非 JSON":     {body: `not json at all`},
		"envelope 尾随数据":       {body: chatCompletionsBody(t, `{"line":"好"}`) + ` {}`},
		"choices 缺席":          {body: `{}`},
		"choices 为空":          {body: `{"choices":[]}`},
		"choices 多于一个":        {body: `{"choices":[{"message":{"content":"{}"}},{"message":{"content":"{}"}}]}`},
		"content 为空字符串":       {body: chatCompletionsBody(t, "")},
		"content 非 JSON":      {body: chatCompletionsBody(t, `一句台词`)},
		"content 未知字段":        {body: chatCompletionsBody(t, `{"line":"好","extra":1}`)},
		"content 缺 line":      {body: chatCompletionsBody(t, `{}`)},
		"content 尾随数据":        {body: chatCompletionsBody(t, `{"line":"好"} {}`)},
		"终态缺 summary":         {body: chatCompletionsBody(t, `{"line":"好"}`), terminal: true},
		"非终态带 summary":        {body: chatCompletionsBody(t, `{"line":"好","summary":"记忆"}`)},
		"台词超过 256 bytes":      {body: chatCompletionsBody(t, overlongLine)},
		"台词含 Unicode control": {body: chatCompletionsBody(t, `{"line":"好\x01"}`)},
	}
	for name, testCase := range cases {
		var requests int32
		dialogue, _ := newTestDialogueClient(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
			_, _ = io.WriteString(w, testCase.body)
		}))
		_, _, err := dialogue.Do(context.Background(), testDialogueRequest(t), testCase.terminal)
		wantDialogueError(t, err)
		if errors.Is(err, ErrDialogueUnavailable) {
			t.Fatalf("%s: 错误同时命中传输层类别: %v", name, err)
		}
		if strings.Contains(err.Error(), dialogueLeakMarker) {
			t.Fatalf("%s: 错误泄漏正文标记", name)
		}
		if got := atomic.LoadInt32(&requests); got != 1 {
			t.Fatalf("%s: 请求数 = %d，want 1（不重试）", name, got)
		}
	}
}

// TestDialogueClientRejectsInvalidRequest 验证越界请求值在发起请求前被拒绝
// （复用 D3 的防御校验），服务端未收到任何请求。
func TestDialogueClientRejectsInvalidRequest(t *testing.T) {
	var requests int32
	dialogue, _ := newTestDialogueClient(t, "", nil, countingHandler(&requests, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, chatCompletionsBody(t, `{"line":"不应到达"}`))
	}))
	// 绕过构造器直造越界请求，验证 Do 入口的第二层防御。
	invalid := testDialogueRequest(t)
	invalid.Persona = strings.Repeat("a", MaxPersonaBytes+1)
	_, _, err := dialogue.Do(context.Background(), invalid, false)
	wantDialogueError(t, err)
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Fatalf("越界请求仍发出请求: %d", got)
	}
}

// TestDialogueClientDefaultClientBounded 断言默认 HTTP 客户端带 30 秒固定超时
// （DialogueRequestTimeout 与 PlannerRequestTimeout 同源且保持 30 秒）并安装
// 受控 transport（响应头上限、禁用保活）。
func TestDialogueClientDefaultClientBounded(t *testing.T) {
	if DialogueRequestTimeout != 30*time.Second {
		t.Fatalf("DialogueRequestTimeout = %v，want 30s", DialogueRequestTimeout)
	}
	if DialogueRequestTimeout != PlannerRequestTimeout {
		t.Fatalf("Dialogue 与 Planner 超时配置漂移: %v vs %v",
			DialogueRequestTimeout, PlannerRequestTimeout)
	}
	dialogue, _ := newTestDialogueClient(t, "", nil, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, chatCompletionsBody(t, `{"line":"等待"}`))
	})
	if got := dialogue.httpClient.Timeout; got != DialogueRequestTimeout {
		t.Fatalf("默认客户端超时 = %v，want %v", got, DialogueRequestTimeout)
	}
	if dialogue.httpClient.Transport == nil {
		t.Fatal("默认客户端未安装受控 transport（响应头上限/禁用保活）")
	}
}

// TestDialogueClientRejectsInvalidSettings 验证构造器拒绝非法模型设置、接受
// 合法设置（与 PlannerClient 同一设置边界）。
func TestDialogueClientRejectsInvalidSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(server.Close)
	if _, err := NewDialogueClient(ModelSettings{Endpoint: "", Model: "m"}, "", nil); err == nil {
		t.Fatal("空 endpoint 被接受")
	}
	if _, err := NewDialogueClient(ModelSettings{Endpoint: server.URL, Model: ""}, "", nil); err == nil {
		t.Fatal("空 model 被接受")
	}
	if _, err := NewDialogueClient(ModelSettings{Endpoint: server.URL, Model: "m"}, "", nil); err != nil {
		t.Fatalf("合法设置被拒绝: %v", err)
	}
}
