package companion

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestValidatePersona 覆盖人设文本校验矩阵：空串合法（等价空人设）、
// 4,096 字节边界（含多字节文本按字节计）接受、4,097 字节拒绝、含 NUL 拒绝、
// 非法 UTF-8 拒绝。所有拒绝分支的错误信息都不得回显文本内容。
func TestValidatePersona(t *testing.T) {
	// 1,365 个三字节"山"共 4,095 字节，再补一个单字节字符恰好落在
	// MaxPersonaBytes 边界上，锁定"按字节而非按 rune 计数"的语义。
	multibyteBoundary := strings.Repeat("山", MaxPersonaBytes/3) + "A"
	cases := []struct {
		name    string
		persona string
		wantErr bool
	}{
		{name: "空串合法即空人设", persona: "", wantErr: false},
		{name: "4096字节ASCII接受", persona: strings.Repeat("A", MaxPersonaBytes), wantErr: false},
		{name: "4096字节多字节接受", persona: multibyteBoundary, wantErr: false},
		{name: "4097字节拒绝", persona: strings.Repeat("A", MaxPersonaBytes+1), wantErr: true},
		{name: "含NUL拒绝", persona: "山\x00民", wantErr: true},
		{name: "非法UTF8拒绝", persona: "山\xff\xfe民", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidatePersona(testCase.persona)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("ValidatePersona(%d 字节) 错误 = %v, wantErr = %v",
					len(testCase.persona), err, testCase.wantErr)
			}
			if err == nil {
				return
			}
			// persona 不外泄：错误信息只描述越界原因，绝不包含文本内容。
			if strings.Contains(err.Error(), testCase.persona) {
				t.Fatalf("错误信息回显了人设文本: %q", err.Error())
			}
		})
	}
}

// TestDefinitionPersonaJSONTag 锁定 Definition.Persona 的 wire 键名与 omitempty
// 语义：空人设写出时省略该键，非空人设按 "persona" 键往返，config.Save 的
// 调试面板往返因此不会丢失或篡改人设。
func TestDefinitionPersonaJSONTag(t *testing.T) {
	id, err := ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(Definition{ID: id, Persona: ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "persona") {
		t.Fatalf("空人设不应写出 persona 键: %s", encoded)
	}
	encoded, err = json.Marshal(Definition{ID: id, Persona: "温和的向导"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"persona":"温和的向导"`) {
		t.Fatalf("非空人设未按 persona 键写出: %s", encoded)
	}
}
