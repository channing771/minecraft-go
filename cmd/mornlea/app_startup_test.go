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
	sideEffectErr := errors.New("unexpected client side effect")
	local := localConnectionOptions()
	remote := remoteConnectionOptions()
	headless := localConnectionOptions()
	headless.Benchmark = true
	for _, test := range []struct {
		name      string
		options   applicationOptions
		configure func(*applicationDependencies, func(string))
	}{
		{name: "本地交互", options: local},
		{name: "远程连接", options: remote},
		{
			name:    "benchmark 无头",
			options: headless,
			configure: func(dependencies *applicationDependencies, called func(string)) {
				dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
					called("openStore")
					return storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: 42}), nil
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := make(map[string]int)
			called := func(name string) { calls[name]++ }
			dependencies := defaultApplicationDependencies()
			dependencies.newRegistry = func(path string) (*assets.Registry, error) {
				if path != "/missing/texture-pack" {
					t.Fatalf("registry path = %q", path)
				}
				return nil, want
			}
			dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
				called("openStore")
				return nil, sideEffectErr
			}
			dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
				called("dialTCP")
				return nil, sideEffectErr
			}
			dependencies.loginClient = func(context.Context, network.ClientPacketStream, network.Identity) (network.ClientEndpoint, uint64, error) {
				called("loginClient")
				return nil, 0, sideEffectErr
			}
			dependencies.newHost = func(context.Context, server.Config, server.Generator, storage.WorldStore) (applicationHost, error) {
				called("newHost")
				return nil, sideEffectErr
			}
			dependencies.newMemoryStreamPair = func(int) (network.ClientPacketStream, network.ServerPacketStream, error) {
				called("newMemoryStreamPair")
				return nil, nil, sideEffectErr
			}
			dependencies.newWindow = func(int, int, string) (applicationWindow, error) {
				called("newWindow")
				return nil, sideEffectErr
			}
			dependencies.newWindowedRenderer = func(applicationWindow) (*client.Renderer, error) {
				called("newWindowedRenderer")
				return nil, sideEffectErr
			}
			dependencies.newOffscreenRenderer = func(int, int) (*client.Renderer, error) {
				called("newOffscreenRenderer")
				return nil, sideEffectErr
			}
			if test.configure != nil {
				test.configure(&dependencies, called)
			}

			test.options.TexturePackPath = "/missing/texture-pack"
			_, err := newApplicationWithDependencies(test.options, dependencies)
			for _, name := range []string{
				"openStore", "dialTCP", "loginClient", "newHost", "newMemoryStreamPair",
				"newWindow", "newWindowedRenderer", "newOffscreenRenderer",
			} {
				if calls[name] != 0 {
					t.Errorf("材质加载失败后 %s calls = %d，want 0", name, calls[name])
				}
			}
			if !errors.Is(err, want) {
				t.Errorf("newApplication error = %v，want %v", err, want)
			}
			if err == nil || !strings.Contains(err.Error(), `加载材质包 "/missing/texture-pack"`) {
				t.Errorf("newApplication error = %q，want path context", err)
			}
		})
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
