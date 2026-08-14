package storage

import (
	"context"
	"errors"

	"github.com/channing771/mornlea/internal/companion"
)

// ErrCompanionsNotFound 表示世界尚无伙伴聚合存档。
var ErrCompanionsNotFound = errors.New("storage: companions not found")

// StoredCompanions 是从聚合存档恢复的伙伴身体快照。
type StoredCompanions struct {
	Revision uint64
	Records  []companion.Body
}

// CompanionSave 是一次伙伴身体聚合保存请求。
type CompanionSave struct {
	Revision uint64
	Records  []companion.Body
}

// CompanionStore 定义伙伴聚合存档的加载与保存边界。
type CompanionStore interface {
	LoadCompanions(context.Context) (StoredCompanions, error)
	SaveCompanions(context.Context, CompanionSave) error
}
