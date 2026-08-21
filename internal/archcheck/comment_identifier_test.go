package archcheck_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// 本文件守住一条经验教训：**注释里提到的标识符必须真的存在**。
//
// 变更 authoritative-fluid 的 F2 组里，这类失真出现了四次——其中一次就发生在
// 删掉该字段的同一个 commit 里（字段删了，同一个方法的 GoDoc 还在声称「不遍历
// 它」），另一次发生在刚对该文件做过专项保真检查的同一轮。人工检查已被证实挡
// 不住，只能机械化。
//
// # 提取规则（只看反引号内）
//
// 只把注释里**反引号包裹**的内容当作「提及的标识符」，且该内容要形如 Go 标识符
// 或点路径（identifierPattern）。反引号之外的裸词一律不解析：本仓的中文注释里
// 大量出现 CamelCase 裸词，实测全仓有 5,442 处，其中 516 处（169 个不同 token）
// 无法解析为已声明名字——绝大多数是常量名的口语化简写（把 ChatEventTaskStarted
// 写成 TaskStarted）、里程碑代号（M5C）、Rust/WGSL 常量、标准库名与单位（KiB）。
// 这些全是误报，逐条豁免的成本远高于门禁本身的价值，因此裸词不在扫描范围内。
//
// # 存在性判定
//
// 点路径只查**末段**（core.IsFluid 查 IsFluid，Queue.pending 查 pending）：本仓
// 没有跨包类型解析，查末段是唯一不需要类型信息就能做的判定。末段要出现在全仓
// 任一 Go 文件（含 _test.go）的这些名字集合里：包名、函数与方法名、类型名、
// 常量与变量名、结构体字段名、接口方法名。**不含**函数参数与局部变量——实测
// 实测全仓当前的反引号提及均可解析，命中数随约定推广而增长；加进参数只会放宽判定。
//
// # 已知盲区（写在这里，别指望这条检查抓）
//
//   - **计数漂移**：枚举式注释写「三个阶段」而常量已有五个，句子里根本没有标识
//     符，本检查抓不到。任务 9.0 修的三处正是这一类，只能靠评审。
//   - **裸词失真**：如上，不扫描。覆盖率因此完全取决于书写约定「注释提及 Go
//     标识符必须用反引号包裹」（见基线文档的实现约定一节）被遵守到什么程度；
//     放宽提取规则不是提升覆盖的正确方向。
//   - **同名掩盖**：点路径只查**末段**，而末段是在全仓 9,000 多个名字的**扁平**
//     集合里查的，不做类型解析。于是短小通用名会被任何无关的同名声明掩盖——
//     注释写 `Queue.pending` 时，只要全仓任何地方还有一个叫 pending 的字段
//     （实际就有：`VisibilityScratch` 的 pending，与流体队列毫无关系），这条
//     提及就永远解析得到。input、due 一类通用名同理。要真正收紧需要跨包类型
//     解析，成本远超这条门禁的价值，故**有意**保留；
//     TestShadowedLastSegmentIsAKnownBlindSpot 把该行为钉成了已知且有意的。
//   - **语义失真**：标识符存在但说明的行为是错的，本检查无能为力。
//   - **Rust/WGSL 注释**：不在本次范围，是后续扩展点（engine/ 下的 .rs 与 .wgsl
//     可以套用同一套「反引号 + 声明名集合」的做法）。这也是 nonGoNameExemptions
//     那几条逐条豁免**没有**自失效守卫的原因：它们指向 Rust 侧的名字，而 Rust
//     源码不在扫描范围内，无从判定这些名字是否还存在。

// identifierPattern 判定反引号内容是否形如 Go 标识符或点路径。
// 含空格、斜杠、冒号（版本区间那种写法）、以数字开头的内容都不匹配，因此这些
// 不必进豁免清单。
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// backtickPattern 抓注释里的反引号片段。刻意不允许跨行，避免块注释里两个相隔
// 很远的反引号被当成一对。
var backtickPattern = regexp.MustCompile("`([^`\n]*)`")

// fileExtensionExemptions 是「点路径末段其实是文件扩展名」的豁免。
// 反引号里写文件名是本仓的常见写法（基线文档、Rust 源文件、oracle 测试文件），
// 它们不是 Go 标识符。清单按实际出现驱动（与 nonGoNameExemptions 同一条纪律：
// 零命中的扩展名不预先登记，否则清单会变成许愿池），出现新扩展名时门禁会红并
// 要求登记。
var fileExtensionExemptions = map[string]string{
	"md": "Markdown 文档名，例如基线文档与 OpenSpec 产物",
	"go": "Go 源文件名，例如指向 oracle 实现所在的文件",
	"rs": "Rust 源文件名，engine crate 里的模块",
}

// nonGoNameExemptions 是逐条登记的非 Go 名字，每条附理由。
// 只登记**实际出现过**的；没出现的不预先登记，免得清单变成许愿池。
var nonGoNameExemptions = map[string]string{
	"MGW1":           "worldgen native ABI 的 wire magic，4 字节字面量，定义在 Rust 侧",
	"mornlea_engine": "Rust cdylib crate 名（mesh/light、物理、raycast、worldgen 的生产实现）",
	"mornlea_client": "Rust cdylib crate 名（窗口、事件循环与全部 GPU 渲染）",
	"is_plant":       "Rust engine greedy 模块的函数名，snake_case，Go 侧没有同名声明",
}

// isShoutingName 判断名字是否形如「全大写 + 下划线」的常量命名。
// 这是 Rust/WGSL 常量与环境变量的形状（FRAME_HEADER_BYTES、SEA_LEVEL_Y、
// MORNLEA_ 前缀的环境变量），Go 侧的命名规范不产生这种名字——
// TestShoutingExemptionHidesNoRealGoName 就是在守住这个前提。
func isShoutingName(name string) bool {
	if name == "" || name[0] < 'A' || name[0] > 'Z' || !strings.Contains(name, "_") {
		return false
	}
	for index := range len(name) {
		char := name[index]
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

// scanCommentBacktickIdentifiers 是本门禁的纯函数内核：给一组 Go 源码
// （key 是用于报错的文件名，value 是源码字节），返回排序后的失真清单与全部
// 已声明名字的集合。
//
// 之所以做成「吃 map、不碰磁盘」的形状，是为了让 9.2 的自检能喂一段内嵌的坏
// 样本走**同一条代码路径**：一个恒真的扫描器与不扫描等价，而它看起来像扫过了，
// 因此扫描器本身必须被已知坏样本证伪过。
func scanCommentBacktickIdentifiers(sources map[string][]byte) (findings []string, declared map[string]bool, err error) {
	files := token.NewFileSet()
	parsed := make(map[string]*ast.File, len(sources))
	declared = make(map[string]bool)
	for _, name := range slices.Sorted(maps.Keys(sources)) {
		file, parseErr := parser.ParseFile(files, name, sources[name], parser.ParseComments)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("解析 %s: %w", name, parseErr)
		}
		parsed[name] = file
		declared[file.Name.Name] = true
		collectDeclaredNames(file, declared)
	}
	for _, name := range slices.Sorted(maps.Keys(parsed)) {
		for _, group := range parsed[name].Comments {
			for _, comment := range group.List {
				findings = append(findings, findingsInComment(files, comment, declared)...)
			}
		}
	}
	slices.Sort(findings)
	return findings, declared, nil
}

// collectDeclaredNames 收集一个文件里的声明名字：函数与方法名、类型名、
// 常量与变量名、结构体字段名、接口方法名。
//
// 用 ast.Inspect 而不是只看 file.Decls，是为了把函数体内声明的类型与常量也算
// 进来——它们同样是「注释可以合法提到」的名字，漏掉只会制造误报。
func collectDeclaredNames(file *ast.File, declared map[string]bool) {
	addFieldNames := func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, field := range list.List {
			for _, name := range field.Names {
				declared[name.Name] = true
			}
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch node := node.(type) {
		case *ast.FuncDecl:
			declared[node.Name.Name] = true
		case *ast.TypeSpec:
			declared[node.Name.Name] = true
		case *ast.ValueSpec:
			for _, name := range node.Names {
				declared[name.Name] = true
			}
		case *ast.StructType:
			addFieldNames(node.Fields)
		case *ast.InterfaceType:
			addFieldNames(node.Methods)
		}
		return true
	})
}

// findingsInComment 抽出一条注释里的失真。位置用反引号片段在注释内的字节偏移
// 换算，因此块注释里第几行出的问题能精确指到那一行。
func findingsInComment(files *token.FileSet, comment *ast.Comment, declared map[string]bool) []string {
	var findings []string
	for _, match := range backtickPattern.FindAllStringSubmatchIndex(comment.Text, -1) {
		mention := comment.Text[match[2]:match[3]]
		if !identifierPattern.MatchString(mention) {
			continue
		}
		if _, exempt := nonGoNameExemptions[mention]; exempt {
			continue
		}
		segments := strings.Split(mention, ".")
		last := segments[len(segments)-1]
		if _, exempt := fileExtensionExemptions[last]; exempt && len(segments) > 1 {
			continue
		}
		if isShoutingName(last) {
			continue
		}
		if declared[last] {
			continue
		}
		position := files.Position(comment.Pos() + token.Pos(match[0]))
		findings = append(findings, fmt.Sprintf("%s: 注释提到的标识符 %q 在全仓 Go 声明中未找到", position, mention))
	}
	return findings
}

// repositoryGoSources 读取全仓 Go 源码（含 _test.go），key 是相对模块根的路径。
//
// 从模块根整棵树扫，而不是写死 cmd/ 与 internal/：写死根目录列表的扫描器在有人
// 新增顶层 Go 目录时会静默漏扫，那正是本文件要防的失败模式。跳过的四个目录都
// 不是本模块源码——.git 是版本库内部数据，.worktrees 是并行分支的工作树副本，
// vendor 与 node_modules 是第三方代码。
func repositoryGoSources(t *testing.T) map[string][]byte {
	t.Helper()
	root := moduleRoot(t)
	sources := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".worktrees", "vendor", "node_modules":
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sources[filepath.ToSlash(relative)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("遍历模块根 %s: %v", root, err)
	}
	// 扫到 0 个文件的扫描器永远通过，且看起来像扫过了。
	if len(sources) < 100 {
		t.Fatalf("只扫到 %d 个 Go 文件，本仓有数百个：遍历被目录改名或跳过规则打断了", len(sources))
	}
	return sources
}

// TestCommentBacktickIdentifiersExist 是全仓门禁：注释里反引号包裹的标识符
// 必须真的存在。判定规则、豁免清单与已知盲区见本文件顶部的说明。
func TestCommentBacktickIdentifiersExist(t *testing.T) {
	findings, _, err := scanCommentBacktickIdentifiers(repositoryGoSources(t))
	if err != nil {
		t.Fatalf("扫描全仓注释: %v", err)
	}
	for _, finding := range findings {
		t.Errorf("%s", finding)
	}
}

// TestShoutingExemptionHidesNoRealGoName 守住 isShoutingName 这条**形状豁免**的
// 前提：Go 侧不产生「全大写 + 下划线」的名字。
//
// 形状豁免比逐条豁免危险得多——它会连未来的名字一起放过。一旦有人在 Go 里声明
// 了 FOO_BAR，这条豁免就开始掩盖真失真，而现场不会有任何信号。这里让它自己失效。
func TestShoutingExemptionHidesNoRealGoName(t *testing.T) {
	_, declared, err := scanCommentBacktickIdentifiers(repositoryGoSources(t))
	if err != nil {
		t.Fatalf("扫描全仓注释: %v", err)
	}
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		if isShoutingName(name) {
			t.Errorf("Go 声明 %s 形如全大写常量，isShoutingName 豁免会掩盖对它的真失真；请改名或把该豁免收窄", name)
		}
	}
}

// 下面四段是自检用的内嵌样本。用普通字符串拼接而不是原始字符串字面量，是因为
// 样本本身必须含反引号，而 Go 的原始字符串字面量正是用反引号定界的。
//
// 样本是**字符串常量**、不是本文件的注释，因此不会被全仓扫描当成提及。
const (
	// selfCheckDeclarationsSource 提供自检用的已声明名字。
	selfCheckDeclarationsSource = "package sample\n" +
		"\n" +
		"type sampleQueue struct {\n" +
		"\tdue []int\n" +
		"}\n" +
		"\n" +
		"func sampleWorker() {}\n"

	// selfCheckBadSource 是「注释提到一个根本不存在的名字」的坏样本。
	selfCheckBadSource = "package sample\n" +
		"\n" +
		"// sampleTick 推进一次。\n" +
		"//\n" +
		"// 这一行提到 `DefinitelyNotARealIdentifier`，全仓没有这个名字。\n" +
		"func sampleTick() {}\n"

	// selfCheckGoodSource 是「注释提到的名字都存在」的对照样本，含跨文件解析
	// 与点路径末段解析两种情况。
	selfCheckGoodSource = "package sample\n" +
		"\n" +
		"// sampleDrain 清空队列；见 `sampleQueue.due` 与 `sampleWorker`。\n" +
		"func sampleDrain() {}\n"

	// selfCheckRenamedFieldSource 复刻 F2 那次坏样本的**形状**：字段改了名，
	// 同一段注释还在提旧名。这正是人工专项检查放过去的那一类。
	//
	// 注意「形状」二字：这条样本能红，前提是旧名在扫描输入里**唯一**。真实仓库
	// 里 pending 并不唯一（见盲区一节的同名掩盖），因此 F2 那次即便加了反引号，
	// 本门禁也抓不到——两件事分开看，别把这条样本的绿灯当成对 F2 的覆盖证明。
	selfCheckRenamedFieldSource = "package sample\n" +
		"\n" +
		"// sampleAdvance 取出到期项；它不遍历 `pending`，只看堆顶。\n" +
		"func sampleAdvance(queue *sampleQueue) int { return len(queue.due) }\n"

	// selfCheckShadowedRenamedSource 是「字段改了名，注释仍提旧名」——与
	// selfCheckRenamedFieldSource 完全同形，唯一区别是旧名 hidden 在另一个文件里
	// 还有一个**无关的**同名声明。
	selfCheckShadowedRenamedSource = "package sample\n" +
		"\n" +
		"// sampleShadowed 的字段原名 hidden，已改名为 renamed；本行仍写 `sampleShadowed.hidden`。\n" +
		"type sampleShadowed struct {\n" +
		"\trenamed []int\n" +
		"}\n"

	// selfCheckShadowingSource 提供那个无关的同名字段。
	selfCheckShadowingSource = "package sample\n" +
		"\n" +
		"// sampleUnrelated 与 sampleShadowed 毫无关系，只是恰好也有一个 hidden 字段。\n" +
		"type sampleUnrelated struct {\n" +
		"\thidden int\n" +
		"}\n"

	// selfCheckExemptSource 覆盖三类豁免，必须一条都不报。
	selfCheckExemptSource = "package sample\n" +
		"\n" +
		"// sampleNotes 提到 `MGW1` 这个 wire magic、`CLAUDE.md` 这个文档名，\n" +
		"// 以及 `SEA_LEVEL_Y` 这个 Rust 侧常量。\n" +
		"func sampleNotes() {}\n"
)

// selfCheckFindings 用内嵌样本跑一遍扫描器内核。extra 是样本文件名到源码的
// 映射，与固定的 declarations.go 一起构成这次扫描的全部输入。
func selfCheckFindings(t *testing.T, extra map[string]string) []string {
	t.Helper()
	sources := map[string][]byte{"declarations.go": []byte(selfCheckDeclarationsSource)}
	for name, source := range extra {
		sources[name] = []byte(source)
	}
	findings, _, err := scanCommentBacktickIdentifiers(sources)
	if err != nil {
		t.Fatalf("扫描内嵌样本: %v", err)
	}
	return findings
}

// TestCommentIdentifierScannerCatchesKnownBadSamples 是 9.2 的常驻自检：
// **先证明检测器会红，再信任它报出的「0 条」**。
//
// 没有这一步，全仓那条门禁的绿灯不构成任何证据——提取规则写错一个字符、豁免
// 清单写宽一格，扫描器就会永远返回空清单，而它看起来像扫过了。把自检做成常驻
// 测试而不是一次性验证，是因为规则今后还会被人改。
func TestCommentIdentifierScannerCatchesKnownBadSamples(t *testing.T) {
	t.Run("不存在的名字必须报错", func(t *testing.T) {
		findings := selfCheckFindings(t, map[string]string{"bad.go": selfCheckBadSource})
		if len(findings) != 1 {
			t.Fatalf("想要恰好 1 条失真，得到 %d 条: %v", len(findings), findings)
		}
		if !strings.Contains(findings[0], "DefinitelyNotARealIdentifier") {
			t.Errorf("失真没有指向坏样本的名字: %s", findings[0])
		}
		// 坏名字写在样本的第 5 行；指错行的报告等于没有报告。
		if !strings.Contains(findings[0], "bad.go:5:") {
			t.Errorf("失真没有指向坏样本所在行: %s", findings[0])
		}
	})

	t.Run("存在的名字不得报错", func(t *testing.T) {
		if findings := selfCheckFindings(t, map[string]string{"good.go": selfCheckGoodSource}); len(findings) != 0 {
			t.Errorf("对存在的名字误报: %v", findings)
		}
	})

	t.Run("字段改名而注释未改必须报错", func(t *testing.T) {
		findings := selfCheckFindings(t, map[string]string{"renamed.go": selfCheckRenamedFieldSource})
		if len(findings) != 1 {
			t.Fatalf("想要恰好 1 条失真，得到 %d 条: %v", len(findings), findings)
		}
		if !strings.Contains(findings[0], `"pending"`) {
			t.Errorf("失真没有指向被改名的字段: %s", findings[0])
		}
	})

	t.Run("豁免清单内的名字不得报错", func(t *testing.T) {
		if findings := selfCheckFindings(t, map[string]string{"exempt.go": selfCheckExemptSource}); len(findings) != 0 {
			t.Errorf("对已豁免的名字误报: %v", findings)
		}
	})
}

// TestShadowedLastSegmentIsAKnownBlindSpot 把「同名掩盖」这个盲区钉成**已知且
// 有意**的行为（Ruling 54）。
//
// 样本与 selfCheckRenamedFieldSource 完全同形——字段改名、注释仍提旧名——唯一
// 区别是旧名在另一个文件里还有一个无关的同名声明。因为点路径只查末段、且在扁平
// 的全仓名字集合里查，这条真失真**查不出来**，门禁按设计保持绿。
//
// 断言「必须绿」看起来别扭，它守的不是正确性而是**文档与实现的一致性**：谁要是
// 哪天把判定收紧成查全路径或做了类型解析，这条会红，提醒他同步更新盲区一节。
// 没有它，盲区就只是一段可能已经过时的散文。
func TestShadowedLastSegmentIsAKnownBlindSpot(t *testing.T) {
	findings := selfCheckFindings(t, map[string]string{
		"shadowed.go":  selfCheckShadowedRenamedSource,
		"shadowing.go": selfCheckShadowingSource,
	})
	if len(findings) != 0 {
		t.Errorf("同名掩盖这个盲区不再存在（判定被收紧了？）；请同步更新本文件顶部的盲区一节。实测失真: %v", findings)
	}
}
