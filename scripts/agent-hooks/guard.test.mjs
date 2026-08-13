import test from "node:test";
import assert from "node:assert/strict";

import {
  changedPaths,
  findBlockedCommand,
  openSpecRequirementReasons,
  runCommand,
  rustValidationRequired,
  stopFailures,
} from "./guard.mjs";

test("blocks destructive git commands", () => {
  assert.match(findBlockedCommand("git reset --hard HEAD"), /reset --hard/);
  assert.match(findBlockedCommand("git clean -fd"), /git clean/);
  assert.match(findBlockedCommand("git push origin main --force-with-lease"), /强制推送/);
});

test("blocks recursive deletion of broad targets", () => {
  assert.match(findBlockedCommand("sudo rm -rf /"), /递归强制删除/);
  assert.match(findBlockedCommand('rm -fr "$HOME"'), /递归强制删除/);
  assert.match(findBlockedCommand("rm -rf ."), /递归强制删除/);
});

test("allows scoped and read-only commands", () => {
  assert.equal(findBlockedCommand("rm -rf ./bin"), null);
  assert.equal(findBlockedCommand("git status --short"), null);
  assert.equal(findBlockedCommand("go test ./... -race"), null);
});

test("requires OpenSpec for contract and architecture changes", () => {
  assert.deepEqual(openSpecRequirementReasons(["internal/network/packet.go"]), [
    "改动涉及协议、存档格式、性能基线或架构依赖门禁",
  ]);
  assert.deepEqual(openSpecRequirementReasons(["internal/archcheck/deps_test.go"]), [
    "改动涉及协议、存档格式、性能基线或架构依赖门禁",
  ]);
});

test("requires OpenSpec for cross-component implementation changes", () => {
  const reasons = openSpecRequirementReasons([
    "internal/client/receiver.go",
    "internal/server/host.go",
  ]);
  assert.equal(reasons.length, 1);
  assert.match(reasons[0], /internal\/client/);
  assert.match(reasons[0], /internal\/server/);
});

test("does not require OpenSpec for one focused implementation component", () => {
  assert.deepEqual(
    openSpecRequirementReasons(["internal/client/camera.go", "internal/client/camera_test.go"]),
    [],
  );
});

test("requires Rust validation for engine and native mesh changes", () => {
  assert.equal(rustValidationRequired(["engine/crates/mornlea_mesh/src/light.rs"]), true);
  assert.equal(rustValidationRequired(["internal/mesh/native.go"]), true);
  assert.equal(rustValidationRequired(["internal/server/session_ingress.go"]), false);
});

test("runs Rust validation before Go checks for Rust-required changes", () => {
  const calls = [];
  const run = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    return { status: 0, stdout: "" };
  };

  assert.deepEqual(stopFailures(["engine/crates/mornlea_mesh/src/light.rs"], run, {}), []);
  assert.deepEqual(calls.slice(1, 4), [
    ["make", ["rust"]],
    ["make", ["rust-check"]],
    ["go", ["test", "./internal/mesh", "./internal/client", "-race", "-count=1"]],
  ]);
  assert.equal(calls.some(([command]) => command === "cargo"), false);
});

test("passes login-shell Cargo through the full Stop route when PATH is restricted", () => {
  const calls = [];
  const environment = { SHELL: "/bin/zsh", PATH: "/usr/bin:/bin" };
  const spawn = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    if (command === "/bin/zsh") {
      return { status: 0, stdout: "/toolchain/bin/cargo\n" };
    }
    if (
      command === "make" &&
      ["rust", "rust-check"].includes(argumentsList[0]) &&
      !argumentsList.includes("CARGO=/toolchain/bin/cargo")
    ) {
      return { status: 2, stderr: "cargo: command not found" };
    }
    return { status: 0, stdout: "" };
  };
  const run = (command, argumentsList, timeout) =>
    runCommand(command, argumentsList, timeout, spawn, environment);

  assert.deepEqual(
    stopFailures(["engine/crates/mornlea_mesh/src/light.rs"], run, environment),
    [],
  );
  assert.deepEqual(
    calls.filter(([command]) => command === "make"),
    [
      ["make", ["rust", "CARGO=/toolchain/bin/cargo"]],
      ["make", ["rust-check", "CARGO=/toolchain/bin/cargo"]],
    ],
  );
});

test("routes deleted Rust and native paths through Rust validation", () => {
  const collect = (command, argumentsList) => {
    assert.equal(command, "git");
    if (argumentsList[0] === "diff") {
      assert.ok(argumentsList.includes("--diff-filter=ACMRD"));
      return {
        status: 0,
        stdout: "engine/crates/mornlea_mesh/src/removed.rs\0internal/mesh/native_removed.go\0",
      };
    }
    return { status: 0, stdout: "" };
  };
  const paths = changedPaths(collect);
  const calls = [];
  const run = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    return { status: 0, stdout: "" };
  };

  assert.deepEqual(paths, [
    "engine/crates/mornlea_mesh/src/removed.rs",
    "internal/mesh/native_removed.go",
  ]);
  assert.deepEqual(stopFailures(paths, run, {}), []);
  assert.deepEqual(
    calls.filter(([command]) => command === "make"),
    [
      ["make", ["rust"]],
      ["make", ["rust-check"]],
    ],
  );
  assert.equal(calls.some(([command]) => command === "gofmt"), false);
});

test("builds Rust before existing Go gates without unrelated cargo checks", () => {
  const calls = [];
  const run = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    return { status: 0, stdout: "" };
  };

  assert.deepEqual(stopFailures(["internal/server/session_ingress.go"], run, {}), []);
  assert.deepEqual(calls.slice(1, 5), [
    ["gofmt", ["-l", "internal/server/session_ingress.go"]],
    ["make", ["rust"]],
    ["go", ["test", "./internal/archcheck", "-count=1"]],
    ["go", ["test", "-race", "-count=1", "./internal/server"]],
  ]);
  assert.equal(calls.some(([command]) => command === "cargo"), false);
});

test("finds tools through the login shell when the hook PATH is incomplete", () => {
  const calls = [];
  const spawn = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    if (
      command === "gofmt" ||
      (command.endsWith("/gofmt") && command !== "/toolchain/bin/gofmt")
    ) {
      return { error: Object.assign(new Error("spawnSync gofmt ENOENT"), { code: "ENOENT" }) };
    }
    if (command === "/bin/zsh") {
      return { status: 0, stdout: "/toolchain/bin/gofmt\n" };
    }
    return { status: 0, stdout: "" };
  };

  const result = runCommand(
    "gofmt",
    ["-l", "internal/server/example.go"],
    30_000,
    spawn,
    { SHELL: "/bin/zsh", PATH: "/usr/bin:/bin" },
  );

  assert.equal(result.status, 0);
  assert.deepEqual(calls[0], ["gofmt", ["-l", "internal/server/example.go"]]);
  assert.deepEqual(calls.at(-2), ["/bin/zsh", ["-lc", "command -v gofmt"]]);
  assert.deepEqual(calls.at(-1), [
    "/toolchain/bin/gofmt",
    ["-l", "internal/server/example.go"],
  ]);
});

test("finds cargo through the login shell when PATH is incomplete", () => {
  const calls = [];
  const spawn = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    if (
      command === "cargo" ||
      (command.endsWith("/cargo") && command !== "/toolchain/bin/cargo")
    ) {
      return { error: Object.assign(new Error("spawnSync cargo ENOENT"), { code: "ENOENT" }) };
    }
    if (command === "/bin/zsh") {
      return { status: 0, stdout: "/toolchain/bin/cargo\n" };
    }
    return { status: 0, stdout: "" };
  };

  const result = runCommand("cargo", ["fmt", "--check"], 30_000, spawn, {
    SHELL: "/bin/zsh",
    PATH: "/usr/bin:/bin",
  });

  assert.equal(result.status, 0);
  assert.deepEqual(calls[0], ["cargo", ["fmt", "--check"]]);
  assert.deepEqual(calls.at(-2), ["/bin/zsh", ["-lc", "command -v cargo"]]);
  assert.deepEqual(calls.at(-1), ["/toolchain/bin/cargo", ["fmt", "--check"]]);
});

test("finds Go tools through GOROOT when the hook PATH is incomplete", () => {
  const calls = [];
  const spawn = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    if (calls.length === 1) {
      return { error: Object.assign(new Error("spawnSync go ENOENT"), { code: "ENOENT" }) };
    }
    return { status: 0, stdout: "" };
  };

  const result = runCommand("go", ["vet", "./..."], 30_000, spawn, {
    GOROOT: "/toolchain",
    PATH: "/usr/bin:/bin",
  });

  assert.equal(result.status, 0);
  assert.deepEqual(calls, [
    ["go", ["vet", "./..."]],
    ["/toolchain/bin/go", ["vet", "./..."]],
  ]);
});

test("finds tools in common installation directories when the hook PATH is incomplete", () => {
  const calls = [];
  const spawn = (command, argumentsList) => {
    calls.push([command, argumentsList]);
    if (calls.length === 1) {
      return { error: Object.assign(new Error("spawnSync openspec ENOENT"), { code: "ENOENT" }) };
    }
    return { status: 0, stdout: "" };
  };

  const result = runCommand("openspec", ["validate", "--all"], 30_000, spawn, {
    PATH: "/usr/bin:/bin",
  });

  assert.equal(result.status, 0);
  assert.deepEqual(calls, [
    ["openspec", ["validate", "--all"]],
    ["/opt/homebrew/bin/openspec", ["validate", "--all"]],
  ]);
});
