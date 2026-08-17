// Package companion 定义端无关的伙伴身份与身体数据。
package companion

import (
	"errors"
	"fmt"
	"unicode"

	"github.com/channing771/mornlea/internal/core"
)

const (
	// MaxActive 是同时启用的伙伴数量上限。
	MaxActive = 4
	// MaxStored 是存档保留的 active 与 inactive 伙伴总数上限。
	MaxStored = 64
)

// ID 是独立于玩家和会话身份的 UUIDv4 伙伴标识。
type ID [16]byte

// ParseID 只接受标准、小写的 UUIDv4 文本形式。
func ParseID(value string) (ID, error) {
	id, err := core.ParsePlayerID(value)
	if err != nil {
		return ID{}, fmt.Errorf("companion: 解析 ID: %w", err)
	}
	return ID(id), nil
}

// Valid 报告该 ID 是否为非零 UUIDv4。
func (id ID) Valid() bool {
	return core.PlayerID(id).Valid()
}

// String 返回 UUID 的标准小写文本形式。
func (id ID) String() string {
	return core.PlayerID(id).String()
}

// MarshalText 把有效 ID 编码为标准 UUID 文本。
func (id ID) MarshalText() ([]byte, error) {
	if !id.Valid() {
		return nil, errors.New("companion: ID 不是有效 UUIDv4")
	}
	return []byte(id.String()), nil
}

// UnmarshalText 只接受标准、小写的 UUIDv4 文本。
func (id *ID) UnmarshalText(text []byte) error {
	if id == nil {
		return errors.New("companion: nil ID")
	}
	parsed, err := ParseID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Definition 是配置中的伙伴静态定义。
type Definition struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
	// Persona 是该伙伴的人设自由文本（约束见 ValidatePersona；空串=空人设）。
	// 由 config 在加载时从内联 ai.companions[].persona（优先）或配置目录
	// personas/<canonical 名称>.txt 解析并完成校验降级，是后续 Dialogue 输入
	// 的唯一人设来源，绝不进入 Planner 输入。omitempty 保证空人设的配置在
	// Save 往返时不落 "persona": "" 键。
	Persona string `json:"persona,omitempty"`
}

// Body 是伙伴可持久化的权威身体状态。
type Body struct {
	ID        ID
	Dimension core.DimensionID
	Position  [3]float32
	Yaw       float32
	Pitch     float32
	Inventory core.Inventory
}

// ValidateDefinitions 验证一组伙伴定义的数量、身份和名称唯一性。
func ValidateDefinitions(definitions []Definition) error {
	if len(definitions) > MaxActive {
		return fmt.Errorf("companion: 定义数量 %d 超过上限 %d", len(definitions), MaxActive)
	}
	ids := make(map[ID]struct{}, len(definitions))
	names := make(map[string]struct{}, len(definitions))
	for index, definition := range definitions {
		if !definition.ID.Valid() {
			return fmt.Errorf("companion: definitions[%d].id 无效", index)
		}
		if _, exists := ids[definition.ID]; exists {
			return fmt.Errorf("companion: definitions[%d].id 重复", index)
		}
		if err := ValidateName(definition.Name); err != nil {
			return fmt.Errorf("companion: definitions[%d].name: %w", index, err)
		}
		if _, exists := names[definition.Name]; exists {
			return fmt.Errorf("companion: definitions[%d].name 重复", index)
		}
		ids[definition.ID] = struct{}{}
		names[definition.Name] = struct{}{}
	}
	return nil
}

// ValidateName 验证伙伴名称规范且不含任何 Unicode 空白。
func ValidateName(name string) error {
	canonical, err := core.NormalizeDisplayName(name)
	if err != nil {
		return err
	}
	if canonical != name {
		return errors.New("名称不是 canonical 形式")
	}
	for _, r := range name {
		if unicode.IsSpace(r) {
			return errors.New("名称包含 Unicode 空白")
		}
	}
	return nil
}
