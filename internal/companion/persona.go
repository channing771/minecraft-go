package companion

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxPersonaBytes 是单个伙伴人设文本的字节长度上界。
//
// 人设是服主手写的自由文本，只作为 Dialogue 提示的数据部分使用；4 KiB 足够
// 容纳一段完整的性格描述，同时保证它不可能撑爆任何提示构造或告警缓冲。
// 该上界同时约束内联 ai.companions[].persona 与配置目录 personas/ 下外部
// 文件两种来源，由 ValidatePersona 统一裁决。
const MaxPersonaBytes = 4096

// ValidatePersona 校验人设自由文本：非空时必须是有效 UTF-8、不超过
// MaxPersonaBytes 字节且不含 NUL；空串合法，等价于"空人设"——伙伴照常触发
// 台词，只是没有风格约束。
//
// 长度按字节而非 rune 计数，与外部文件的磁盘占用及后续提示构造的缓冲上界
// 语义一致。错误信息只描述越界原因（长度、编码、NUL），绝不回显文本内容：
// 人设文本可能随错误进入日志，这里从源头保证 persona 不外泄。
func ValidatePersona(persona string) error {
	if persona == "" {
		return nil
	}
	if !utf8.ValidString(persona) {
		return errors.New("companion: 人设文本不是有效 UTF-8")
	}
	if strings.ContainsRune(persona, 0) {
		return errors.New("companion: 人设文本包含 NUL")
	}
	if len(persona) > MaxPersonaBytes {
		return fmt.Errorf("companion: 人设文本 %d 字节超过上限 %d", len(persona), MaxPersonaBytes)
	}
	return nil
}
