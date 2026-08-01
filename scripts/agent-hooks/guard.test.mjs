import test from "node:test";
import assert from "node:assert/strict";

import { findBlockedCommand, openSpecRequirementReasons } from "./guard.mjs";

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
