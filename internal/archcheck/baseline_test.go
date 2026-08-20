package archcheck_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// baselineVersionMapping 描述 `CLAUDE.md` 中一条基线版本断言与其代码权威来源的对应关系。
//
// 两侧都是"从文件里读"：docPattern 从 `CLAUDE.md` 正文捕获文档声称的版本号，
// codePattern 从权威来源文件的源码文本捕获真实值。任何一侧写死期望值都会让本门禁
// 在下次升版时静默失效——那恰恰是它要防的事。
type baselineVersionMapping struct {
	// name 是断言的可读名，只用于失败信息。
	name string
	// docPattern 在 `CLAUDE.md` 中定位该断言，捕获组 1 为文档声称的版本号。
	docPattern string
	// sourcePath 是权威来源文件，相对模块根。
	sourcePath string
	// codePattern 在权威来源文本中捕获常量值，捕获组 1 可以是数值，
	// 也可以是另一个常量名（间接定义，由 resolveBaselineConstant 继续解析）。
	codePattern string
	// why 说明"为什么这个文件是权威来源"，便于后来者判断映射是否仍然成立。
	why string
}

// baselineVersionMappings 是 `CLAUDE.md`「项目定位」段声明的全部契约版本号。
//
// 这份文件会被每个 agent 会话读进上下文，它说什么后续工作就按什么假设推进；
// 而"归档时更新基线"此前只是一条建议，没有任何机械检查兜底，结果基线一次滞后了
// 四个里程碑（文档写协议 v16、代码已到 v20）。本表把那条建议变成门禁。
var baselineVersionMappings = []baselineVersionMapping{
	{
		name:        "协议版本",
		docPattern:  `已经包含协议 v(\d+)`,
		sourcePath:  filepath.Join("internal", "network", "packet.go"),
		codePattern: `const\s+ProtocolVersion\s+uint32\s*=\s*(\w+)`,
		why:         "ProtocolVersion 是握手与全部 packet 编解码唯一支持的版本号，wire 兼容性以它为准。",
	},
	{
		name:        "区块 schema",
		docPattern:  `区块 schema v(\d+)`,
		sourcePath:  filepath.Join("internal", "storage", "chunk_codec.go"),
		codePattern: `currentChunkSchema\s+uint32\s*=\s*(\w+)`,
		why:         "currentChunkSchema 是区块记录写出时落盘的 schema 号，也是拒绝更高版本的上界。",
	},
	{
		name:        "玩家 schema",
		docPattern:  `玩家 schema v(\d+)`,
		sourcePath:  filepath.Join("internal", "storage", "player_codec.go"),
		codePattern: `currentPlayerSchema\s+uint32\s*=\s*(\w+)`,
		why:         "currentPlayerSchema 是玩家记录写出时落盘的 schema 号。",
	},
	{
		name:        "companions.ai schema",
		docPattern:  "独立 `companions\\.ai` schema v(\\d+)",
		sourcePath:  filepath.Join("internal", "storage", "companion_codec.go"),
		codePattern: `currentCompanionSchema\s+uint32\s*=\s*(\w+)`,
		why:         "currentCompanionSchema 是 companions.ai 写出时落盘的 schema 号；它以 companionSchemaVN 间接定义，需解析到最终数值。",
	},
	{
		name:        "世界 metadata 版本",
		docPattern:  `世界 metadata v(\d+)`,
		sourcePath:  filepath.Join("internal", "storage", "metadata.go"),
		codePattern: `currentMetadataVersion\s+uint32\s*=\s*(\w+)`,
		why:         "currentMetadataVersion 是世界 metadata 写出时的 FormatVersion，也是加载时的上界。",
	},
	{
		name:        "engine ABI",
		docPattern:  `engine ABI v(\d+)`,
		sourcePath:  filepath.Join("engine", "include", "mornlea_engine.h"),
		codePattern: `#define\s+MORNLEA_ENGINE_ABI_VERSION\s+(\d+)u`,
		why:         "头文件宏是 Go 与 Rust 共同编译进二进制的 engine C ABI 版本，混装检测以它为准。",
	},
	{
		name:        "client ABI",
		docPattern:  `client ABI v(\d+)`,
		sourcePath:  filepath.Join("engine", "include", "mornlea_client.h"),
		codePattern: `#define\s+MORNLEA_CLIENT_ABI_VERSION\s+(\d+)u`,
		why:         "头文件宏是 mornlea_client cdylib 的 C ABI 版本，混装检测以它为准。",
	},
	{
		name:        "benchmark scenario",
		docPattern:  `benchmark scenario 为 v(\d+)`,
		sourcePath:  filepath.Join("cmd", "mornlea", "benchmark.go"),
		codePattern: `scenarioVersion\s*=\s*(\w+)`,
		why:         "scenarioVersion 是 benchmark 报告写出的场景版本，场景迁移链以它为终点。",
	},
}

// TestBaselineVersionsMatchCode 把 `CLAUDE.md`「项目定位」段的契约版本号与代码里的
// 权威常量逐条比对，防止长期基线文档在归档后悄悄滞后。
//
// 两条实现约束：
//   - 期望值一律从文件读取，测试内不出现任何具体版本号；
//   - "八条断言是否都被找到"这个防空转守卫必须排在逐条比对之后。否则一次真实的
//     版本不匹配若同时让某条正则失配，守卫会先报出"断言未找到"这个误导性诊断，
//     把下一个人引向错误方向。
func TestBaselineVersionsMatchCode(t *testing.T) {
	root := moduleRoot(t)
	// 两份基线文档必须逐字节相同（见 TestBaselineDocsAreIdentical），这里仍然各校验一遍：
	// 一致性测试若将来被删或被跳过，版本号一侧不应随之失去防护。
	for _, name := range baselineDocNames {
		t.Run(name, func(t *testing.T) {
			assertBaselineVersions(t, root, name)
		})
	}
}

// baselineDocNames 是两份必须保持逐字节相同的长期基线文档。
var baselineDocNames = []string{"AGENTS.md", "CLAUDE.md"}

// TestBaselineDocsAreIdentical 锁定 `AGENTS.md` 与 `CLAUDE.md` 逐字节相同。
//
// 二者曾因归档时只更新其中一份而分叉：`AGENTS.md` 维护到了 M5E，`CLAUDE.md` 停在 M5A，
// 基线描述滞后四个里程碑且无人察觉。孪生文件的真实失效模式是"只改了一份"，
// 所以这条一致性断言比任何单文件检查都更贴根因。
func TestBaselineDocsAreIdentical(t *testing.T) {
	root := moduleRoot(t)
	if len(baselineDocNames) < 2 {
		t.Fatalf("基线文档只登记了 %d 份，一致性断言无从谈起", len(baselineDocNames))
	}
	reference := baselineDocNames[0]
	referenceText := readBaselineDoc(t, root, reference)
	for _, name := range baselineDocNames[1:] {
		text := readBaselineDoc(t, root, name)
		if text == referenceText {
			continue
		}
		t.Errorf("%s 与 %s 内容不同：两份长期基线文档必须逐字节相同，改任何一份都要同步另一份"+
			"（%s %d 字节，%s %d 字节）",
			reference, name, reference, len(referenceText), name, len(text))
	}
}

// readBaselineDoc 读取一份基线文档，并拒绝空文件——本门禁两侧都靠读文件，
// 空文件会让所有断言静默消失。
func readBaselineDoc(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	if len(data) == 0 {
		t.Fatalf("%s 为空：本门禁两侧都靠读文件，空文件会让所有断言静默消失", path)
	}
	return string(data)
}

// assertBaselineVersions 校验一份基线文档里的契约版本号与代码常量一致。
func assertBaselineVersions(t *testing.T, root, name string) {
	t.Helper()
	guidePath := filepath.Join(root, name)
	guide := readBaselineDoc(t, root, name)
	// 映射表本身也是承重件：删掉一行同样能让门禁静默变松，因此固定条数。
	const expectedMappingCount = 8
	if len(baselineVersionMappings) != expectedMappingCount {
		t.Fatalf("基线映射表有 %d 条，期望 %d 条：新增或删除契约版本号时必须同步本表",
			len(baselineVersionMappings), expectedMappingCount)
	}

	var missing []string
	for _, mapping := range baselineVersionMappings {
		sourcePath := filepath.Join(root, mapping.sourcePath)
		sourceText, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Errorf("%s：读取权威来源 %s 失败: %v（%s）", mapping.name, sourcePath, err, mapping.why)
			continue
		}
		codeValue, err := resolveBaselineConstant(string(sourceText), mapping.codePattern)
		if err != nil {
			t.Errorf("%s：在权威来源 %s 中取值失败: %v（%s）", mapping.name, sourcePath, err, mapping.why)
			continue
		}

		docValues := regexp.MustCompile(mapping.docPattern).FindAllStringSubmatch(guide, -1)
		if len(docValues) == 0 {
			// 先记下，循环结束后统一报错，保证真实的版本不匹配先于"未找到"出现。
			missing = append(missing, fmt.Sprintf("%s（正则 %q）", mapping.name, mapping.docPattern))
			continue
		}
		for _, docValue := range docValues {
			if docValue[1] != codeValue {
				t.Errorf("%s 基线滞后：%s 写 v%s，代码是 %s（权威来源 %s，%s）",
					mapping.name, guidePath, docValue[1], codeValue, mapping.sourcePath, mapping.why)
			}
		}
	}

	// 防空转守卫：正则匹配不到任何东西时循环零次、测试照样绿，门禁就成了全绿的摆设。
	// 措辞改动导致失配必须报红，而不是静默跳过。
	if len(missing) > 0 {
		t.Errorf("%s 中未找到以下基线版本断言，措辞变更后本门禁会静默失效，请同步正则或补回断言：%s",
			guidePath, strings.Join(missing, "；"))
	}
}

// resolveBaselineConstant 从源码文本中取出常量值，并顺着间接定义解析到最终数值。
//
// `currentCompanionSchema uint32 = companionSchemaV4` 这类写法若只取字面捕获，
// 门禁会拿标识符去和版本号比较、或者更糟地"读到标识符就放行"，因此必须继续解析。
// 跳数设上界以防常量互相引用形成死循环。
func resolveBaselineConstant(sourceText, codePattern string) (string, error) {
	matches := regexp.MustCompile(codePattern).FindStringSubmatch(sourceText)
	if matches == nil {
		return "", fmt.Errorf("正则 %q 未匹配到常量定义", codePattern)
	}
	value := matches[1]
	const maxHops = 4
	for hop := 0; !isDecimalDigits(value); hop++ {
		if hop >= maxHops {
			return "", fmt.Errorf("常量 %s 的间接定义超过 %d 跳仍未解析到数值", value, maxHops)
		}
		indirect := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(value) + `\s+uint32\s*=\s*(\w+)`)
		next := indirect.FindStringSubmatch(sourceText)
		if next == nil {
			return "", fmt.Errorf("间接常量 %s 在同一文件内没有定义", value)
		}
		value = next[1]
	}
	return value, nil
}

// isDecimalDigits 判断字符串是否为非空的十进制数字串。
func isDecimalDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
