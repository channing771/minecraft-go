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

func mustParseID(t *testing.T, text string) ID {
	t.Helper()
	id, err := ParseID(text)
	if err != nil {
		t.Fatalf("ParseID(%q): %v", text, err)
	}
	return id
}
