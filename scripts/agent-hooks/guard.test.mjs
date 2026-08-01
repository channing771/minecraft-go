import test from "node:test";
import assert from "node:assert/strict";

import { findBlockedCommand, openSpecRequirementReasons, runCommand } from "./guard.mjs";

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
