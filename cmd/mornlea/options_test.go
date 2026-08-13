//go:build darwin

package main

import "testing"

func TestParseMainOptionsRejectsRemoteLocalConflicts(t *testing.T) {
	for _, args := range [][]string{
		{"--connect", "127.0.0.1:25565", "--world", "worlds/demo"},
		{"--connect", "127.0.0.1:25565", "--benchmark", "--perf-output", "x.json"},
		{"--benchmark", "--perf-output", "x.json", "--name", "Chen"},
	} {
		if _, err := parseMainOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestParseMainOptionsAllowsRemoteWithDefaultWorld(t *testing.T) {
	options, err := parseMainOptions([]string{"--connect", "127.0.0.1:25565"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Application.Connect != "127.0.0.1:25565" || options.Application.WorldPath != "worlds/default" {
		t.Fatalf("options=%+v", options.Application)
	}
}

func TestParseMainOptionsBenchmarkTransport(t *testing.T) {
	defaults, err := parseMainOptions([]string{"--benchmark", "--perf-output", "x.json"})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Application.BenchmarkTransport != "memory" {
		t.Fatalf("default benchmark transport=%q, want memory", defaults.Application.BenchmarkTransport)
	}
	tcp, err := parseMainOptions([]string{
		"--benchmark", "--benchmark-transport", "tcp", "--perf-output", "x.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tcp.Application.BenchmarkTransport != "tcp" {
		t.Fatalf("TCP benchmark transport=%q", tcp.Application.BenchmarkTransport)
	}
	for _, args := range [][]string{
		{"--benchmark-transport", "tcp"},
		{"--benchmark", "--benchmark-transport", "udp", "--perf-output", "x.json"},
	} {
		if _, err := parseMainOptions(args); err == nil {
			t.Fatalf("accepted invalid benchmark transport args %v", args)
		}
	}
}

func TestParseMainOptionsCaptureDir(t *testing.T) {
	opts, err := parseMainOptions([]string{"--capture", "/tmp/shots"})
	if err != nil {
		t.Fatalf("解析 --capture 失败: %v", err)
	}
	if opts.CaptureDir != "/tmp/shots" {
		t.Fatalf("CaptureDir = %q，想要 %q", opts.CaptureDir, "/tmp/shots")
	}
	if opts.Application.CaptureDir != "/tmp/shots" {
		t.Fatalf("Application.CaptureDir = %q，想要 %q", opts.Application.CaptureDir, "/tmp/shots")
	}
}

func TestParseMainOptionsCaptureRejectsConflicts(t *testing.T) {
	// --capture 与 --benchmark 都会独占无头渲染路径并各自驱动场景，
	// 同时开启的语义无法定义，必须直接拒绝而不是让某一方静默胜出。
	tests := []struct {
		name string
		args []string
	}{
		{"与 benchmark 互斥", []string{"--capture", "/tmp/shots", "--benchmark", "--perf-output", "/tmp/p.json"}},
		{"与 connect 互斥", []string{"--capture", "/tmp/shots", "--connect", "127.0.0.1:25565"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMainOptions(tc.args); err == nil {
				t.Fatal("想要报错，实际通过")
			}
		})
	}
}

func TestParseMainOptionsUpdateGoldenRequiresCapture(t *testing.T) {
	if _, err := parseMainOptions([]string{"--update-golden"}); err == nil {
		t.Fatal("--update-golden 缺少 --capture 时想要报错，实际通过")
	}
}

func TestParseMainOptionsUpdateGoldenWithCapturePropagates(t *testing.T) {
	opts, err := parseMainOptions([]string{"--capture", "/tmp/shots", "--update-golden"})
	if err != nil {
		t.Fatalf("解析 --capture --update-golden 失败: %v", err)
	}
	if !opts.UpdateGolden {
		t.Fatal("UpdateGolden = false，想要 true")
	}
}

func TestParseMainOptionsWithoutUpdateGoldenDefaultsFalse(t *testing.T) {
	opts, err := parseMainOptions([]string{"--capture", "/tmp/shots"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.UpdateGolden {
		t.Fatal("UpdateGolden = true，想要默认 false")
	}
}

func TestParseMainOptionsWithoutCaptureLeavesDirEmpty(t *testing.T) {
	opts, err := parseMainOptions(nil)
	if err != nil {
		t.Fatalf("解析空参数失败: %v", err)
	}
	if opts.CaptureDir != "" {
		t.Fatalf("CaptureDir = %q，想要空", opts.CaptureDir)
	}
}

func TestParseOptionsDefaultsDevOff(t *testing.T) {
	options, err := parseMainOptions([]string{})
	if err != nil {
		t.Fatalf("parseMainOptions: %v", err)
	}
	if options.Dev {
		t.Fatal("--dev 默认必须关闭")
	}
}

func TestParseOptionsAcceptsDevAndConfig(t *testing.T) {
	options, err := parseMainOptions([]string{"--dev", "--config", "/tmp/x.json"})
	if err != nil {
		t.Fatalf("parseMainOptions: %v", err)
	}
	if !options.Dev {
		t.Fatal("--dev 必须被解析")
	}
	if options.ConfigPath != "/tmp/x.json" {
		t.Fatalf("ConfigPath = %q", options.ConfigPath)
	}
}
