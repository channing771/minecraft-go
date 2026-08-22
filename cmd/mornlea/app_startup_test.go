//go:build darwin

package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

func TestNewApplicationReturnsRegistryErrorBeforeClientSideEffects(t *testing.T) {
	want := errors.New("registry failure")
	unexpected := func(name string) {
		t.Helper()
		t.Fatalf("材质加载失败后调用了 %s", name)
	}
	dependencies := defaultApplicationDependencies()
	dependencies.newRegistry = func(path string) (*assets.Registry, error) {
		if path != "/missing/texture-pack" {
			t.Fatalf("registry path = %q", path)
		}
		return nil, want
	}
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		unexpected("openStore")
		return nil, nil
	}
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		unexpected("dialTCP")
		return nil, nil
	}
	dependencies.newHost = func(context.Context, server.Config, server.Generator, storage.WorldStore) (applicationHost, error) {
		unexpected("newHost")
		return nil, nil
	}
	dependencies.newWindow = func(int, int, string) (applicationWindow, error) {
		unexpected("newWindow")
		return nil, nil
	}
	dependencies.newWindowedRenderer = func(applicationWindow) (*client.Renderer, error) {
		unexpected("newWindowedRenderer")
		return nil, nil
	}
	dependencies.newOffscreenRenderer = func(int, int) (*client.Renderer, error) {
		unexpected("newOffscreenRenderer")
		return nil, nil
	}

	options := localConnectionOptions()
	options.TexturePackPath = "/missing/texture-pack"
	_, err := newApplicationWithDependencies(options, dependencies)
	if !errors.Is(err, want) {
		t.Fatalf("newApplication error = %v，want %v", err, want)
	}
	if !strings.Contains(err.Error(), `加载材质包 "/missing/texture-pack"`) {
		t.Fatalf("newApplication error = %q，want path context", err)
	}
}

func TestNewApplicationDefaultRegistryDependencyUsesEmbeddedDefault(t *testing.T) {
	got, err := defaultApplicationDependencies().newRegistry("")
	if err != nil {
		t.Fatalf("newRegistry: %v", err)
	}
	want := assets.NewDefaultRegistry()
	gotLayers, gotPixels := got.AtlasPixels()
	wantLayers, wantPixels := want.AtlasPixels()
	if gotLayers != wantLayers || !bytes.Equal(gotPixels, wantPixels) {
		t.Fatal("空路径没有构造内嵌默认材质注册表")
	}
}

func TestNewApplicationDefaultRegistryDependencyAppliesDirectoryOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "textures"), 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"format":1,"name":"startup test"}`), 0o600); err != nil {
		t.Fatalf("WriteFile manifest: %v", err)
	}
	want := color.NRGBA{R: 17, G: 34, B: 51, A: 68}
	texture := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for offset := 0; offset < len(texture.Pix); offset += 4 {
		copy(texture.Pix[offset:offset+4], []byte{want.R, want.G, want.B, want.A})
	}
	file, err := os.Create(filepath.Join(dir, "textures", "stone.png"))
	if err != nil {
		t.Fatalf("Create texture: %v", err)
	}
	if err := png.Encode(file, texture); err != nil {
		_ = file.Close()
		t.Fatalf("Encode texture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close texture: %v", err)
	}

	got, err := defaultApplicationDependencies().newRegistry(dir)
	if err != nil {
		t.Fatalf("newRegistry: %v", err)
	}
	pixels := got.LayerRGBA(int(assets.LayerStone))
	for offset := 0; offset < len(pixels); offset += 4 {
		if pixel := (color.NRGBA{R: pixels[offset], G: pixels[offset+1], B: pixels[offset+2], A: pixels[offset+3]}); pixel != want {
			t.Fatalf("stone pixel %d = %+v，want %+v", offset/4, pixel, want)
		}
	}
}
