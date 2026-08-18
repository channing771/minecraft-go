package companion

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestValidateDefinitions(t *testing.T) {
	ids := []string{
		"00112233-4455-4677-8899-aabbccddeeff",
		"10112233-4455-4677-8899-aabbccddeeff",
		"20112233-4455-4677-8899-aabbccddeeff",
		"30112233-4455-4677-8899-aabbccddeeff",
		"40112233-4455-4677-8899-aabbccddeeff",
	}
	definitions := make([]Definition, 4)
	for index := range definitions {
		definitions[index] = Definition{ID: mustParseID(t, ids[index]), Name: "伙伴" + string(rune('A'+index))}
	}

	tests := []struct {
		name        string
		definitions []Definition
		wantError   bool
	}{
		{name: "四个有效定义", definitions: definitions},
		{name: "五个定义", definitions: append(append([]Definition(nil), definitions...), Definition{ID: mustParseID(t, ids[4]), Name: "伙伴E"}), wantError: true},
		{name: "重复ID", definitions: []Definition{definitions[0], {ID: definitions[0].ID, Name: "另一个"}}, wantError: true},
		{name: "重复名称", definitions: []Definition{definitions[0], {ID: definitions[1].ID, Name: definitions[0].Name}}, wantError: true},
		{name: "大小写敏感名称", definitions: []Definition{{ID: definitions[0].ID, Name: "A"}, {ID: definitions[1].ID, Name: "a"}}},
		{name: "名称含普通空格", definitions: []Definition{{ID: definitions[0].ID, Name: "阿 木"}}, wantError: true},
		{name: "名称含Unicode空白", definitions: []Definition{{ID: definitions[0].ID, Name: "阿\u3000木"}}, wantError: true},
		{name: "名称非canonical", definitions: []Definition{{ID: definitions[0].ID, Name: " 阿木"}}, wantError: true},
		{name: "零ID", definitions: []Definition{{Name: "阿木"}}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateDefinitions(test.definitions)
			if (err != nil) != test.wantError {
				t.Fatalf("ValidateDefinitions() error = %v，wantError %v", err, test.wantError)
			}
		})
	}
}

func TestValidateDefinitionsNameBoundaries(t *testing.T) {
	id := mustParseID(t, "00112233-4455-4677-8899-aabbccddeeff")
	tests := []struct {
		name      string
		value     string
		wantError bool
	}{
		{name: "空名称", value: "", wantError: true},
		{name: "非法UTF8", value: string([]byte{0xff}), wantError: true},
		{name: "Unicode control", value: "阿\n木", wantError: true},
		{name: "一个rune", value: "阿"},
		{name: "三十二个rune", value: strings.Repeat("阿", 32)},
		{name: "三十三个rune", value: strings.Repeat("阿", 33), wantError: true},
		{name: "三十二个四字节rune共128bytes", value: strings.Repeat("😀", 32)},
	}
	// 合法 UTF-8 单 rune 最多占 4 bytes；32-rune 上限内不存在独立的 129-byte 合法样本。
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateName(test.value); (err != nil) != test.wantError {
				t.Errorf("ValidateName(%q) error = %v，wantError %v", test.value, err, test.wantError)
			}
			definitions := []Definition{{ID: id, Name: test.value}}
			if err := ValidateDefinitions(definitions); (err != nil) != test.wantError {
				t.Errorf("ValidateDefinitions(%q) error = %v，wantError %v", test.value, err, test.wantError)
			}
		})
	}
}

func TestCompanionIDJSONAndTextRoundTrip(t *testing.T) {
	const canonical = "00112233-4455-4677-8899-aabbccddeeff"
	want := mustParseID(t, canonical)
	if !want.Valid() || want.String() != canonical {
		t.Fatalf("解析后 ID = %q valid=%v", want.String(), want.Valid())
	}

	text, err := want.MarshalText()
	if err != nil || string(text) != canonical {
		t.Fatalf("MarshalText = %q, %v", text, err)
	}
	var fromText ID
	if err := fromText.UnmarshalText(text); err != nil || fromText != want {
		t.Fatalf("UnmarshalText = %v, %v", fromText, err)
	}

	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("Marshal JSON: %v", err)
	}
	if string(encoded) != `"`+canonical+`"` {
		t.Fatalf("JSON = %s", encoded)
	}
	var decoded ID
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != want {
		t.Fatalf("Unmarshal JSON = %v, %v", decoded, err)
	}

	for _, invalid := range []string{
		"00112233-4455-4677-8899-AABBCCDDEEFF",
		"00112233-4455-3677-8899-aabbccddeeff",
		"00000000-0000-0000-0000-000000000000",
	} {
		if err := decoded.UnmarshalText([]byte(invalid)); err == nil {
			t.Fatalf("UnmarshalText 接受非法 ID %q", invalid)
		}
	}
}

func TestCompanionBodyHasNoFutureFields(t *testing.T) {
	typeOfBody := reflect.TypeOf(Body{})
	want := []string{"ID", "Dimension", "Position", "Yaw", "Pitch", "Inventory"}
	if typeOfBody.NumField() != len(want) {
		t.Fatalf("Body 字段数 = %d，want %d", typeOfBody.NumField(), len(want))
	}
	for index, name := range want {
		if got := typeOfBody.Field(index).Name; got != name {
			t.Fatalf("Body 字段 %d = %q，want %q", index, got, name)
		}
	}
}

// TestDefinitionPersonaTagsAreFrozen 用反射锁定 Definition 两个人设字段的
// json tag 分工（F-2）：Persona 是磁盘镜像字段（persona,omitempty），保存
// 内联原文且无内联人设时不落空键；ResolvedPersona 是进程内派生的生效值
// （json:"-"），永不进入任何 JSON 序列化。M5D D2 已在行为级锁定「内联人设
// 原样落盘、生效值不落盘」，本测试把分工钉在结构 tag 层——未来任何人给
// ResolvedPersona 换上可序列化 tag 或去掉 Persona 的 omitempty，都会先在
// 这里变红，而不是等到存档往返静默篡改用户磁盘数据时才暴露。
func TestDefinitionPersonaTagsAreFrozen(t *testing.T) {
	wantTags := map[string]string{
		"Persona":         "persona,omitempty",
		"ResolvedPersona": "-",
	}
	typeOfDefinition := reflect.TypeOf(Definition{})
	for name, want := range wantTags {
		field, ok := typeOfDefinition.FieldByName(name)
		if !ok {
			t.Fatalf("Definition 缺少字段 %s", name)
		}
		if got := field.Tag.Get("json"); got != want {
			t.Fatalf("Definition.%s 的 json tag = %q，want %q", name, got, want)
		}
	}
}

// TestDefinitionMarshalsResolvedPersonaNowhere 值级锁定 ResolvedPersona 永不
// 进入 JSON 输出（F-2）：构造 ResolvedPersona 非空而 Persona 为空的
// Definition，Marshal 后输出既无 resolvedPersona 键、也无生效人设值的任何
// 字节——生效人设是 config.Load 解析出的进程内事实，落盘会破坏 Persona 的
// 磁盘镜像语义（外部 personas/ 文件内容会被静默吸收为内联，见 identity.go
// 的字段注释）。反射 tag 锁（上一测试）钉结构分工，本测试钉序列化结果，
// 两层互为冗余防线。
func TestDefinitionMarshalsResolvedPersonaNowhere(t *testing.T) {
	resolved := "生效人设文本：沉稳寡言的老向导，说话简短。"
	encoded, err := json.Marshal(Definition{
		ID:              mustParseID(t, "00112233-4455-4677-8899-aabbccddeeff"),
		Name:            "阿木",
		ResolvedPersona: resolved,
	})
	if err != nil {
		t.Fatalf("Marshal Definition: %v", err)
	}
	if strings.Contains(string(encoded), "resolvedPersona") {
		t.Fatalf("Marshal 输出出现 resolvedPersona 键: %s", encoded)
	}
	if strings.Contains(string(encoded), resolved) {
		t.Fatalf("Marshal 输出泄漏生效人设值: %s", encoded)
	}
}

func mustParseID(t *testing.T, text string) ID {
	t.Helper()
	id, err := ParseID(text)
	if err != nil {
		t.Fatalf("ParseID(%q): %v", text, err)
	}
	return id
}
