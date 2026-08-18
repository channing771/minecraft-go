package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/network"
)

// TestPlannerRequestExcludesPersonaAndSummary 是「表达平面绝不进入规划输入」
// 的 server 侧反向锁（终审 Should-Fix-1）：伙伴携带非空人设（ResolvedPersona，
// 值等价于任一来源——内联或 personas/ 外部文件，文件来源到达该字段的链路由
// cmd/mornlea-server 的 persona 全链测试锁定）与非空最近对话摘要进入 Planning
// 时，Planner 请求正文 MUST 同时不含二者文本。这补上了既有 planner_test 包级
// 纯函数测试覆盖不到的 server 接线链路，并以「值」而非「字段名」为断言口径
// ——未来任何把 slot.summary 或 ResolvedPersona 塞进快照字符串字段的改动
// 都会被捕获。
func TestPlannerRequestExcludesPersonaAndSummary(t *testing.T) {
	const secretPersona = "内联人设绝密文本乐观但怕黑"
	const secretSummary = "绝密最近摘要伙伴曾挖过煤矿"
	id := chatTestCompanionID(1)
	definitions := []companion.Definition{{
		ID: id, Name: "阿木",
		Persona: secretPersona, ResolvedPersona: secretPersona,
	}}
	host, client, _ := companionManagerHostReady(t, definitions, nil)

	// 捕获规划请求正文的假模型：记录原始 body 后返回合法 envelope（计划
	// 本身无关紧要——非法计划令任务失败即可，正文已在失败前被捕获）。
	var mu sync.Mutex
	var bodies []string
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": `{"summary":"s","steps":[]}`}},
			},
		})
	}))
	t.Cleanup(model.Close)
	plannerClient, err := companion.NewPlannerClient(companion.ModelSettings{
		Endpoint: model.URL + "/v1",
		Model:    "test-model",
	}, "", nil)
	if err != nil {
		t.Fatalf("构造测试 planner: %v", err)
	}
	host.world.companionManager.planner = plannerClient

	// 摘要属于伙伴而非任务：直接写入 manager 状态模拟「已有对话历史」。
	host.world.stepMu.Lock()
	host.world.companionManager.slots[id].summary = secretSummary
	host.world.stepMu.Unlock()

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 随便走走"})
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		result := host.world.StepForTest()
		receiveCompanionChatTick(t, client, result.Tick)
		mu.Lock()
		count := len(bodies)
		mu.Unlock()
		if count >= 1 {
			break
		}
	}
	mu.Lock()
	captured := append([]string(nil), bodies...)
	mu.Unlock()
	if len(captured) == 0 {
		t.Fatal("规划请求未在窗口内到达假模型")
	}
	for index, raw := range captured {
		if strings.Contains(raw, secretPersona) {
			t.Fatalf("规划请求 %d 泄漏人设文本：%s", index, raw)
		}
		if strings.Contains(raw, secretSummary) {
			t.Fatalf("规划请求 %d 泄漏最近摘要文本：%s", index, raw)
		}
	}
}
