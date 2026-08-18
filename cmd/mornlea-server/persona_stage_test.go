// M5D 阶段总验收的 persona 文件来源端到端测试（D8）：真实 config.Load 路径
// （t.TempDir 的 config.json + 配置目录 personas/<canonical 名称>.txt）→
// run() 的服务端装配 → 真实 server.NewHost（真实 50ms 权威 tick）→ 真实 TCP
// 登录 → @指令 → 假模型收到的台词请求体携带文件人设。假模型以同一 endpoint
// 同时服务 Planner 与 Dialogue（两者共用 ai 组的模型设置），全程 httptest，
// 绝不访问真实模型服务、绝不打开前台窗口。
//
// 该断言只能落在 cmd 侧：archcheck 的 TestOnlyCommandsImportConfig 禁止任何
// internal 包（含测试导入）依赖 internal/config，internal/server 的阶段总
// 验收测试因此无法触达真实配置装载；四 kind 计划、摘要落盘/重启复用与
// Memory/TCP parity 由 internal/server 的
// TestM5StageAcceptancePersonaDialogueEndToEnd 承担，两者互补构成 D8 全链。
package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

// personaStageCompanionID 是阶段验收伙伴的 UUIDv4 身份（与 server 侧测试的
// chatTestCompanionID 同一字节形状，仅尾字节区分）。
func personaStageCompanionID() companion.ID {
	return companion.ID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, 0x5d}
}

// personaStageIssuerID 是发令玩家的 UUIDv4 身份（与 server 集成测试的
// integrationPlayerID 同一形状，仅尾字节区分）。
func personaStageIssuerID() core.PlayerID {
	return core.PlayerID{0x9d, 0x16, 0xa0, 0x86, 0x33, 0x8b, 0x4e, 0x82,
		0x8a, 0x51, 0x7a, 0x72, 0x42, 0x13, 0x6e, 0x21}
}

// personaStageDialogueRecord 是假模型观察到的一次台词请求输入事实：节点
// 类别、人设与最近对话摘要（对齐 internal/server 侧 fakeDialogueModel 的
// 记录形态，断言依据是 companion 包的稳定 wire 枚举文本）。
type personaStageDialogueRecord struct {
	NodeKind string
	Persona  string
	Summary  string
}

// fakePersonaStageModel 是同时服务 Planner 与 Dialogue 的 httptest 假模型：
// 两类请求共用 /chat/completions，以用户正文的形态区分——台词请求的用户正文
// 是 dialogueUserPayload（含 "node" 键），规划请求是 PlanSnapshot（含
// "issuer"/"companion"，无 "node" 键）。规划响应返回「走向伙伴当前站立格」
// 的单步 go_to 计划（目标即当前位置，任务立即完成——阶段验收此处只关心
// persona 输入链路，计划复杂度由 server 侧测试承担）；台词响应对齐
// internal/server 侧假模型的固定台词与终态摘要。
type fakePersonaStageModel struct {
	mu      sync.Mutex
	records []personaStageDialogueRecord
	server  *httptest.Server
}

func newFakePersonaStageModel(t *testing.T) *fakePersonaStageModel {
	t.Helper()
	model := &fakePersonaStageModel{}
	model.server = httptest.NewServer(http.HandlerFunc(model.handle))
	t.Cleanup(model.server.Close)
	return model
}

func (model *fakePersonaStageModel) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	// 用户消息嵌套在外层请求 JSON 里（messages[1]），先解出 content 再按形态
	// 分流——判定依据是 companion 包线形态的稳定键，不是自由文本猜测。
	userContent := ""
	var outer struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &outer); err == nil && len(outer.Messages) == 2 {
		userContent = outer.Messages[1].Content
	}
	var probe map[string]json.RawMessage
	_ = json.Unmarshal([]byte(userContent), &probe)
	content := ""
	if _, isDialogue := probe["node"]; isDialogue {
		var payload struct {
			Persona string `json:"persona"`
			Summary string `json:"summary"`
			Node    struct {
				Kind string `json:"kind"`
			} `json:"node"`
		}
		if err := json.Unmarshal([]byte(userContent), &payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		model.mu.Lock()
		model.records = append(model.records, personaStageDialogueRecord{
			NodeKind: payload.Node.Kind, Persona: payload.Persona, Summary: payload.Summary,
		})
		model.mu.Unlock()
		content = `{"line":"我出发了"}`
		if payload.Node.Kind == "terminal" {
			content = `{"line":"完成了","summary":"最近完成了任务"}`
		}
	} else {
		// 规划请求：按快照里伙伴的当前位置返回原地单步计划。
		var snapshot struct {
			Companion struct {
				Position [3]float32 `json:"position"`
			} `json:"companion"`
		}
		if err := json.Unmarshal([]byte(userContent), &snapshot); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		position := snapshot.Companion.Position
		plan, _ := json.Marshal(map[string]any{
			"summary": "原地待命",
			"steps":   []map[string]any{{"kind": "go_to", "x": int32(position[0]), "y": int32(position[1]), "z": int32(position[2])}},
		})
		content = string(plan)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]string{"role": "assistant", "content": content}},
		},
	})
}

// snapshotRecords 返回按到达顺序记录的台词请求输入事实快照。
func (model *fakePersonaStageModel) snapshotRecords() []personaStageDialogueRecord {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]personaStageDialogueRecord(nil), model.records...)
}

// waitForPersonaStageRecords 轮询等待台词请求数达到 want（真实 50ms tick 与
// 伙伴出生扫描都是异步的，请求抵达时刻不可预知）。窗口是早退式上界：正常
// 负载秒级返回；取 90 秒是因为 `go test ./... -race` 全仓并行时机器高负载
// 会显著拖慢整条异步链，窗口过窄会在门禁全量跑时假失败。
func waitForPersonaStageRecords(t *testing.T, model *fakePersonaStageModel, want int) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if records := model.snapshotRecords(); len(records) >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("台词请求数未达到 %d：%+v", want, model.snapshotRecords())
}

// TestMornleaServerPersonaFileReachesDialogueRequests 验证 persona 外部文件
// 来源的完整输入链：配置文件不写内联 persona，生效人设只能来自配置目录
// personas/阿木.txt；经真实 config.Load、run() 装配、真实 Host 与真实 TCP
// 登录后，伙伴任务的每一次台词请求体都携带该文件人设，且终态请求在全新
// 存档上以空摘要为近期记忆输入（摘要生命周期由 server 侧测试承担）。
func TestMornleaServerPersonaFileReachesDialogueRequests(t *testing.T) {
	const companionName = "阿木"
	const filePersona = "沉稳寡言的老向导，说话简短，喜欢用矿物打比方。"
	dir := t.TempDir()

	// 配置文件：ai 组携带假模型 endpoint 与伙伴定义，刻意不写内联 persona。
	model := newFakePersonaStageModel(t)
	cfg := config.Defaults()
	cfg.AI = &config.AI{
		ModelSettings: companion.ModelSettings{
			Endpoint:           model.server.URL + "/v1",
			Model:              "test-model",
			TaskTimeoutMinutes: 10,
		},
		Companions: []companion.Definition{{ID: personaStageCompanionID(), Name: companionName}},
	}
	configPath := filepath.Join(dir, "config.json")
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("保存配置文件: %v", err)
	}
	personasDir := filepath.Join(dir, "personas")
	if err := os.MkdirAll(personasDir, 0o700); err != nil {
		t.Fatalf("创建 personas 目录: %v", err)
	}
	personaPath := filepath.Join(personasDir, companionName+".txt")
	if err := os.WriteFile(personaPath, []byte(filePersona), 0o600); err != nil {
		t.Fatalf("写外部人设文件: %v", err)
	}

	// 真实 run()：磁盘世界在独立临时目录，监听器预先创建以便测试知道地址
	//（--listen 参数本身被注入的 listenTCP 覆盖，只需通过参数校验）。
	// newHost 包一层真实 NewHost，只放宽排水敏感参数（outbox 容量与心跳
	// 节奏，与 internal/server 集成测试同款 4096/1h）：默认 512 深度在
	// 全仓 -race 并行负载下会让测试客户端排水不及、outbox 满而断会话，
	// 台词广播随之不可达——被测的 persona 链路与这些参数正交。
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("ListenTCP: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	done := make(chan error, 1)
	go func() {
		done <- run(runCtx, []string{
			"--config", configPath,
			"--world", filepath.Join(t.TempDir(), "world"),
			"--listen", "127.0.0.1:1",
		}, dependencies{
			listenTCP: func(string) (network.Listener, error) { return listener, nil },
			newHost: func(ctx context.Context, cfg server.Config, gen server.Generator, store storage.WorldStore) (mornleaServerHost, error) {
				cfg.OutboxCapacity = 4096
				cfg.HeartbeatInterval = time.Hour
				cfg.HeartbeatTimeout = time.Hour
				return server.NewHost(ctx, cfg, gen, store)
			},
		})
	}()

	// 真实 TCP 登录与指令发送。
	dialCtx, cancelDial := context.WithTimeout(runCtx, 10*time.Second)
	clientStream, err := network.DialTCP(dialCtx, listener.Addr())
	cancelDial()
	if err != nil {
		t.Fatalf("DialTCP: %v", err)
	}
	identity := network.Identity{PlayerID: personaStageIssuerID(), DisplayName: "发令者"}
	loginCtx, cancelLogin := context.WithTimeout(runCtx, 10*time.Second)
	endpoint, err := network.LoginClient(loginCtx, clientStream, identity)
	cancelLogin()
	if err != nil {
		t.Fatalf("LoginClient: %v", err)
	}
	t.Cleanup(func() { _ = endpoint.Close() })

	sendCtx, cancelSend := context.WithTimeout(runCtx, 10*time.Second)
	err = endpoint.Send(sendCtx, network.ChatCommand{Text: "@" + companionName + " 原地待命"})
	cancelSend()
	if err != nil {
		t.Fatalf("发送指令: %v", err)
	}

	// 收集 ChatEvent 直到任务进入 Running（出生扫描与规划都是异步的；
	// Recv 循环同时负责应答服务端心跳。开始事件后 start 台词节点即可派发，
	// 无需等待终态——理由见下方请求体断言注释）。
	deadline := time.Now().Add(120 * time.Second)
	var events []network.ChatEvent
	terminal := false
	for !terminal && time.Now().Before(deadline) {
		recvCtx, cancelRecv := context.WithTimeout(runCtx, 5*time.Second)
		message, recvErr := endpoint.Recv(recvCtx)
		cancelRecv()
		if recvErr != nil {
			t.Fatalf("Recv: %v", recvErr)
		}
		if event, ok := message.(network.ChatEvent); ok {
			events = append(events, event)
			terminal = event.Kind == network.ChatEventTaskStarted ||
				event.Kind == network.ChatEventTaskFailed
		}
	}
	if !terminal {
		t.Fatalf("任务未在窗口内开始：%+v", events)
	}
	for _, event := range events {
		if event.Kind == network.ChatEventTaskFailed {
			t.Fatalf("原地任务失败：%+v", event)
		}
	}

	// 台词请求体断言：本测试的独有覆盖是「真实 config.Load 文件人设 → run()
	// 装配 → 真实服务端 → 台词请求体」，因此只等待 start 节点即可闭环（开始
	// 节点同样暴露 Summary 输入——全新存档为空）。终止节点与摘要生命周期由
	// internal/server 的阶段总验收与 SummaryLifecycle 测试以确定性步进锁定；
	// 这里不强等 terminal：真实 50ms tick 下全任务完成对全仓 -race 并行负载
	// 过敏（终态前的模型往返 + 客户端 Recv 饥饿可致 outbox 满连坐）。
	waitForPersonaStageRecords(t, model, 1)
	records := model.snapshotRecords()
	if len(records) == 0 {
		t.Fatalf("台词请求数=%d，想要至少 start", len(records))
	}
	for index, record := range records {
		if record.Persona != filePersona {
			t.Fatalf("台词请求 %d 人设=%q，想要外部文件人设 %q", index, record.Persona, filePersona)
		}
	}
	if records[0].NodeKind != "start" || records[0].Summary != "" {
		t.Fatalf("首个台词请求=%+v，想要 start 节点且空摘要（全新存档）", records[0])
	}

	// 不在客户端侧断言 CompanionSpeech：假 planner 返回原地单步计划，任务
	// Running 生命周期只有一两个 tick（约 100ms），全仓 -race 并行负载下的
	// 台词 HTTP 往返会超过该窗口，start 结果按过时语义被正确丢弃——生产行
	// 为无误，是本断言对时序过敏。台词广播到真实 TCP 客户端由 server 侧
	// TestCompanionDialogueSpeechMemoryTCPParity（真 TCP 传输）确定性锁定。

	// 关服：ctx 取消走 run() 内的正常 Shutdown 路径，返回值必须干净。
	cancelRun()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run 退出错误: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("run 未在关服窗口内退出")
	}
}
