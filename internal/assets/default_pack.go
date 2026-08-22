package assets

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed packs/pixel_perfection
var defaultPackFS embed.FS

// NewDefaultRegistry 构造带 Mornlea 内嵌默认材质的注册表。
func NewDefaultRegistry() *Registry {
	registry := NewRegistry()
	root, err := fs.Sub(defaultPackFS, "packs/pixel_perfection")
	if err != nil {
		panic(fmt.Sprintf("assets: 打开内嵌默认材质包: %v", err))
	}
	if err := applyPack(registry, root); err != nil {
		panic(fmt.Sprintf("assets: 应用内嵌默认材质包: %v", err))
	}
	return registry
}

// NewRegistryWithOverride 在内嵌默认材质之上应用用户目录覆盖。
func NewRegistryWithOverride(root fs.FS) (*Registry, error) {
	registry := NewDefaultRegistry()
	if err := applyPack(registry, root); err != nil {
		return nil, fmt.Errorf("assets: 应用用户材质包: %w", err)
	}
	return registry, nil
}
