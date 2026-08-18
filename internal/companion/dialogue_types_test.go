package companion

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// dialogueBody 把若干 `"key":value` 片段拼成一份 JSON object 正文，供解码矩阵
// 使用；片段由调用方保证语法正确，测试关注点在解码契约而不是 JSON 语法。
func dialogueBody(fields ...string) []byte {
	return []byte("{" + strings.Join(fields, ",") + "}")
}

// quotedJSON 返回 s 的 JSON 字符串字面量形式，保证特殊字符被正确转义。
func quotedJSON(s string) string {
	return `"` + s + `"`
}

// wantDialogueError 断言响应解码返回错误且属于 ErrDialogueInvalidResponse
// 哨兵类别（响应解码侧；拥有值矩阵里另有正面断言，这里只关心拒绝事实）。
// F-3 拆分后请求构造侧由 wantDialogueRequestError 承载，两个哨兵互斥——
// 解码失败同时命中请求哨兵即说明辖域回归。
func wantDialogueError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回 ErrDialogueInvalidResponse 类错误，got nil")
	}
	if !errors.Is(err, ErrDialogueInvalidResponse) {
		t.Fatalf("错误类别错误: %v，want ErrDialogueInvalidResponse", err)
	}
	if errors.Is(err, ErrDialogueInvalidRequest) {
		t.Fatalf("响应解码错误同时命中请求构造哨兵: %v", err)
	}
}

// wantDialogueRequestError 断言请求构造返回错误且属于 ErrDialogueInvalidRequest
// 哨兵类别（请求构造侧），且不命中响应解码哨兵——两个哨兵分辖进入模型
// 之前与模型输出之后的两端，互斥性在此钉住。
func wantDialogueRequestError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回 ErrDialogueInvalidRequest 类错误，got nil")
	}
	if !errors.Is(err, ErrDialogueInvalidRequest) {
		t.Fatalf("错误类别错误: %v，want ErrDialogueInvalidRequest", err)
	}
	if errors.Is(err, ErrDialogueInvalidResponse) {
		t.Fatalf("请求构造错误同时命中响应解码哨兵: %v", err)
	}
}

func TestDecodeDialogueResponseNonTerminalValid(t *testing.T) {
	line, summary, err := DecodeDialogueResponse(dialogueBody(`"line":"我出发了"`), false)
	if err != nil {
		t.Fatalf("合法非终态响应被拒绝: %v", err)
	}
	if line != "我出发了" {
		t.Fatalf("line 解码错误: %q", line)
	}
	if summary != "" {
		t.Fatalf("非终态响应 summary 必须为空串: %q", summary)
	}
}

func TestDecodeDialogueResponseTerminalValid(t *testing.T) {
	line, summary, err := DecodeDialogueResponse(
		dialogueBody(`"line":"修好了"`, `"summary":"帮玩家修好了木桥"`), true)
	if err != nil {
		t.Fatalf("合法终态响应被拒绝: %v", err)
	}
	if line != "修好了" {
		t.Fatalf("line 解码错误: %q", line)
	}
	if summary != "帮玩家修好了木桥" {
		t.Fatalf("summary 解码错误: %q", summary)
	}
}

func TestDecodeDialogueResponseRejectsMalformedBodies(t *testing.T) {
	cases := map[string][]byte{
		"空正文":         {},
		"非法 JSON":     []byte(`{`),
		"顶层是数组":       []byte(`["我出发了"]`),
		"顶层是字符串":      []byte(`"我出发了"`),
		"line 非字符串":   dialogueBody(`"line":123`),
		"line 为 null": dialogueBody(`"line":null`),
		"空 object":    dialogueBody(),
		"缺 line 字段":   dialogueBody(`"summary":"只有摘要"`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			wantDialogueError(t, mustDecodeErr(t, body, true))
		})
	}
}

// mustDecodeErr 是解码矩阵的便捷封装：返回 err 并在解码意外成功时直接失败。
func mustDecodeErr(t *testing.T, body []byte, terminal bool) error {
	t.Helper()
	_, _, err := DecodeDialogueResponse(body, terminal)
	if err == nil {
		t.Fatalf("期望拒绝正文 %q（terminal=%v），解码却成功", body, terminal)
	}
	return err
}

func TestDecodeDialogueResponseRejectsUnknownField(t *testing.T) {
	wantDialogueError(t, mustDecodeErr(t,
		dialogueBody(`"line":"你好"`, `"mood":"happy"`), false))
}

func TestDecodeDialogueResponseRejectsTrailingData(t *testing.T) {
	cases := map[string][]byte{
		"合法第二个 object": []byte(`{"line":"你好"} {"line":"再见"}`),
		"非 JSON 垃圾后缀":  []byte(`{"line":"你好"}xyz`),
		"顶层多个值":        []byte(`{"line":"你好"} 42`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			wantDialogueError(t, mustDecodeErr(t, body, false))
		})
	}
}

func TestDecodeDialogueResponseRejectsOversizeBody(t *testing.T) {
	// 构造恰好 64 KiB + 1 字节的正文：长度检查必须在任何 JSON 解析之前拒绝，
	// 防御上游限长失效时解码器为超大缓冲做完整解析。
	body := []byte(`{"line":"` + strings.Repeat("a", MaxDialogueResponseBytes-10) + `"}`)
	if len(body) != MaxDialogueResponseBytes+1 {
		t.Fatalf("测试正文长度 %d 不是 %d+1", len(body), MaxDialogueResponseBytes)
	}
	wantDialogueError(t, mustDecodeErr(t, body, false))
}

func TestDecodeDialogueResponseLineByteBounds(t *testing.T) {
	// 0 字节 line（空串）非法：台词必须是非空一行。
	if _, _, err := DecodeDialogueResponse(dialogueBody(`"line":""`), false); err == nil {
		t.Fatalf("空 line 被接受")
	}
	// 恰好 256 字节 ASCII 合法。
	if _, _, err := DecodeDialogueResponse(
		dialogueBody(`"line":`+quotedJSON(strings.Repeat("a", MaxDialogueLineBytes))), false); err != nil {
		t.Fatalf("256 字节 line 被拒绝: %v", err)
	}
	// 257 字节非法：绝不截断或清洗后接受。
	if _, _, err := DecodeDialogueResponse(
		dialogueBody(`"line":`+quotedJSON(strings.Repeat("a", MaxDialogueLineBytes+1))), false); err == nil {
		t.Fatalf("257 字节 line 被接受")
	}
}

func TestDecodeDialogueResponseLineForbiddenContent(t *testing.T) {
	// 用例值是 JSON 字符串字面量原文（含 \u0000 等转义序列）：控制字符必须
	// 以合法转义进入正文，才能穿过 JSON 语法层真实到达 validateDialogueLine
	// 的校验分支——嵌原始控制字节只会在语法层被拒，覆盖不到校验逻辑。
	cases := map[string]string{
		"含 NUL":      `你好\u0000世界`,
		"含换行":        `你好\n世界`,
		"含制表符":       `你好\t世界`,
		"含 DEL 控制字符": `你好\u007f世界`,
		"含 C1 区控制字符": `你好\u0085世界`,
		"首部空白":       ` 你好`,
		"尾部空白":       `你好 `,
		"首部换行转义":     `\n你好`,
		"全角首部空白也算空白": `\u3000你好`,
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			wantDialogueError(t, mustDecodeErr(t,
				dialogueBody(`"line":`+quotedJSON(line)), false))
		})
	}
}

func TestDecodeDialogueResponseSummaryRules(t *testing.T) {
	// 终态缺 summary 非法。
	wantDialogueError(t, mustDecodeErr(t, dialogueBody(`"line":"完成"`), true))
	// 非终态出现 summary 即非法（即使值为空串——字段出现本身就是非法）。
	wantDialogueError(t, mustDecodeErr(t,
		dialogueBody(`"line":"进行中"`, `"summary":""`), false))
	// null 的成员语义裁决（规格字面优先）：JSON null 视为「字段出现」——非终态
	// 携带 summary:null 必须拒绝；终态 summary:null 视为缺席（缺 summary）
	// 同样拒绝。失败模式良性：只跳过一句台词。
	wantDialogueError(t, mustDecodeErr(t,
		dialogueBody(`"line":"进行中"`, `"summary":null`), false))
	wantDialogueError(t, mustDecodeErr(t,
		dialogueBody(`"line":"完成"`, `"summary":null`), true))
	// 终态 summary 恰好 2,048 字节合法。
	if _, summary, err := DecodeDialogueResponse(
		dialogueBody(`"line":"完成"`, `"summary":`+quotedJSON(strings.Repeat("a", MaxDialogueSummaryBytes))), true); err != nil {
		t.Fatalf("2,048 字节 summary 被拒绝: %v", err)
	} else if len(summary) != MaxDialogueSummaryBytes {
		t.Fatalf("summary 长度错误: %d", len(summary))
	}
	// 终态 summary 2,049 字节非法。
	wantDialogueError(t, mustDecodeErr(t,
		dialogueBody(`"line":"完成"`, `"summary":`+quotedJSON(strings.Repeat("a", MaxDialogueSummaryBytes+1))), true))
	// 终态 summary 含 NUL 非法。
	wantDialogueError(t, mustDecodeErr(t,
		dialogueBody(`"line":"完成"`, `"summary":"收工\u0000了"`), true))
	// 终态 summary 为空串合法：字段出现即满足「必须有 summary」，空值等价于清空记忆。
	if _, summary, err := DecodeDialogueResponse(
		dialogueBody(`"line":"完成"`, `"summary":""`), true); err != nil {
		t.Fatalf("空串 summary 被拒绝: %v", err)
	} else if summary != "" {
		t.Fatalf("空串 summary 解码错误: %q", summary)
	}
}

// TestValidateDialogueTextRejectsInvalidUTF8 直测文本校验辅助函数的无效
// UTF-8 分支：encoding/json 解码字符串字段时会把无效字节与孤代理统一替换为
// U+FFFD（Go 标准库既定行为），因此无效 UTF-8 无法以原始形态穿过 JSON 解码
// 层；该分支是校验函数自身的防御层，服务未来不经 JSON 的直构路径，这里用
// 原始无效字节序列直接覆盖。
func TestValidateDialogueTextRejectsInvalidUTF8(t *testing.T) {
	invalid := string([]byte{0xC3, 0x28, 0xFF, 0xFE})
	if err := validateDialogueLine(invalid); err == nil {
		t.Fatalf("无效 UTF-8 line 未被校验函数拒绝")
	}
	if err := validateDialogueSummary(invalid); err == nil {
		t.Fatalf("无效 UTF-8 summary 未被校验函数拒绝")
	}
}

// testDialogueNode 返回一份字段合法的进展节点，供请求构造测试在其上变异。
func testDialogueNode() DialogueNode {
	return DialogueNode{Kind: DialogueNodeProgress, StepKind: PlanStepMine}
}

// testDialogueEnv 返回一份字段合法的最小环境摘要，供请求构造测试在其上变异。
func testDialogueEnv() DialogueEnvDigest {
	return DialogueEnvDigest{
		ExposedBlocks: []PlanBlock{
			{Pos: core.BlockPos{X: 8, Y: 63, Z: -2}, Block: core.GrassID},
			{Pos: core.BlockPos{X: 9, Y: 64, Z: -1}, Block: core.OakLogID},
		},
		Heights: []PlanHeight{
			{X: 8, Z: -2, Height: 63},
			{X: 9, Z: -1, Height: 64},
		},
	}
}

func TestNewDialogueRequestValid(t *testing.T) {
	persona := "沉稳寡言的老向导，说话简短。"
	summary := "上次帮玩家修好了木桥。"
	env := testDialogueEnv()
	request, err := NewDialogueRequest(persona, summary, testDialogueNode(), env)
	if err != nil {
		t.Fatalf("合法请求被拒绝: %v", err)
	}
	if request.Persona != persona || request.Summary != summary {
		t.Fatalf("请求文本字段不完整: %+v", request)
	}
	if request.Node != testDialogueNode() {
		t.Fatalf("请求节点字段不完整: %+v", request.Node)
	}
	if !reflect.DeepEqual(request.Env, env) {
		t.Fatalf("请求环境摘要字段不完整: %+v", request.Env)
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("构造结果未通过二次校验: %v", err)
	}
	// persona 与 summary 为空都是合法形态：空人设 + 无近期记忆。
	if _, err := NewDialogueRequest("", "", DialogueNode{Kind: DialogueNodeStart}, DialogueEnvDigest{}); err != nil {
		t.Fatalf("空 persona 与空 summary 请求被拒绝: %v", err)
	}
}

func TestNewDialogueRequestRejectsOversizeText(t *testing.T) {
	cases := map[string]struct {
		persona string
		summary string
	}{
		"persona 超上界":      {persona: strings.Repeat("好", MaxPersonaBytes/3+1) + "xx", summary: ""},
		"summary 超上界":      {persona: "", summary: strings.Repeat("a", MaxDialogueSummaryBytes+1)},
		"persona 含 NUL":    {persona: "设定\x00文本", summary: ""},
		"summary 含 NUL":    {persona: "", summary: "记忆\x00文本"},
		"summary 无效 UTF-8": {persona: "", summary: string([]byte{0xC3, 0x28})},
	}
	// persona 用三字节汉字乘到恰好越界：MaxPersonaBytes/3*3 + 2 > MaxPersonaBytes。
	if len(cases["persona 超上界"].persona) <= MaxPersonaBytes {
		t.Fatalf("persona 越界样例构造错误: %d", len(cases["persona 超上界"].persona))
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewDialogueRequest(tc.persona, tc.summary, testDialogueNode(), testDialogueEnv())
			wantDialogueRequestError(t, err)
		})
	}
	// 恰好等于上界的输入合法（边界另一侧）。
	if _, err := NewDialogueRequest(
		strings.Repeat("a", MaxPersonaBytes), strings.Repeat("a", MaxDialogueSummaryBytes),
		testDialogueNode(), testDialogueEnv()); err != nil {
		t.Fatalf("恰好上界的 persona/summary 被拒绝: %v", err)
	}
}

func TestNewDialogueRequestRejectsBadEnv(t *testing.T) {
	oversizeExposed := DialogueEnvDigest{ExposedBlocks: make([]PlanBlock, MaxPlanExposedBlocks+1)}
	oversizeHeights := DialogueEnvDigest{Heights: make([]PlanHeight, MaxPlanHeightSamples+1)}
	unorderedExposed := DialogueEnvDigest{ExposedBlocks: []PlanBlock{
		{Pos: core.BlockPos{X: 9, Y: 63, Z: -2}, Block: core.StoneID},
		{Pos: core.BlockPos{X: 8, Y: 63, Z: -2}, Block: core.GrassID},
	}}
	unorderedHeights := DialogueEnvDigest{Heights: []PlanHeight{
		{X: 9, Z: -1, Height: 64}, {X: 8, Z: -2, Height: 63},
	}}
	airExposed := DialogueEnvDigest{ExposedBlocks: []PlanBlock{
		{Pos: core.BlockPos{X: 8, Y: 63, Z: -2}, Block: core.AirID},
	}}
	cases := map[string]DialogueEnvDigest{
		"暴露方块超上限":   oversizeExposed,
		"高度样本超上限":   oversizeHeights,
		"暴露方块非严格升序": unorderedExposed,
		"高度样本非严格升序": unorderedHeights,
		"暴露方块是空气":   airExposed,
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewDialogueRequest("", "", testDialogueNode(), env)
			wantDialogueRequestError(t, err)
		})
	}
}

// TestDialogueRequestFieldSetIsBounded 用反射冻结请求结构字段集合恰好为四类
// 有界输入：persona、summary、节点与环境摘要。spec 要求请求输入 MUST NOT
// 包含 API key、其他玩家聊天文本或存档路径——结构层面不存在这些字段，且本
// 测试阻止未来无声追加。
func TestDialogueRequestFieldSetIsBounded(t *testing.T) {
	requestType := reflect.TypeOf(DialogueRequest{})
	want := []string{"Persona", "Summary", "Node", "Env"}
	if requestType.NumField() != len(want) {
		t.Fatalf("DialogueRequest 字段数 %d，want %d", requestType.NumField(), len(want))
	}
	for index, name := range want {
		if requestType.Field(index).Name != name {
			t.Fatalf("DialogueRequest 字段[%d] = %s，want %s", index, requestType.Field(index).Name, name)
		}
	}
}
