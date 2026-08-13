#!/usr/bin/env node

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const scriptPath = fileURLToPath(import.meta.url);
const repositoryRoot = resolve(dirname(scriptPath), "../..");

function shellSegments(command) {
  return command
    .split(/[;&|\n]+/)
    .map((segment) => segment.match(/"[^"]*"|'[^']*'|[^\s]+/g) ?? [])
    .filter((tokens) => tokens.length > 0);
}

function unquote(token) {
  if (
    token.length >= 2 &&
    ((token.startsWith('"') && token.endsWith('"')) ||
      (token.startsWith("'") && token.endsWith("'")))
  ) {
    return token.slice(1, -1);
  }
  return token;
}

export function findBlockedCommand(command) {
  if (typeof command !== "string" || command.trim() === "") {
    return null;
  }

  for (const tokens of shellSegments(command)) {
    const gitIndex = tokens.findIndex((token) => unquote(token) === "git");
    if (gitIndex >= 0) {
      const subcommand = unquote(tokens[gitIndex + 1] ?? "");
      const argumentsAfterGit = tokens.slice(gitIndex + 2).map(unquote);
      if (subcommand === "reset" && argumentsAfterGit.includes("--hard")) {
        return "禁止 git reset --hard；请使用可恢复、范围明确的操作";
      }
      if (
        subcommand === "clean" &&
        argumentsAfterGit.some(
          (argument) =>
            argument === "--force" || /^-[A-Za-z]*f[A-Za-z]*$/.test(argument),
        )
      ) {
        return "禁止强制 git clean；它可能删除用户未跟踪文件";
      }
      if (
        subcommand === "push" &&
        argumentsAfterGit.some((argument) =>
          ["-f", "--force", "--force-with-lease"].includes(argument),
        )
      ) {
        return "禁止强制推送；如确有需要必须由用户在 Hook 外明确执行";
      }
    }

    const rmIndex = tokens.findIndex((token) => unquote(token) === "rm");
    if (rmIndex >= 0) {
      const argumentsAfterRm = tokens.slice(rmIndex + 1).map(unquote);
      const options = argumentsAfterRm.filter((argument) => argument.startsWith("-"));
      const recursive = options.some(
        (option) => option === "--recursive" || /^-[A-Za-z]*r[A-Za-z]*$/.test(option),
      );
      const force = options.some(
        (option) => option === "--force" || /^-[A-Za-z]*f[A-Za-z]*$/.test(option),
      );
      const protectedTargets = new Set(["/", ".", "..", "~", "$HOME", "${HOME}"]);
      if (
        recursive &&
        force &&
        argumentsAfterRm.some((argument) => protectedTargets.has(argument))
      ) {
        return "禁止对根目录、仓库根目录或主目录执行递归强制删除";
      }
    }
  }

  return null;
}

function componentFor(path) {
  const match = path.match(/^(internal|cmd)\/([^/]+)\/.*\.go$/);
  if (!match || path.endsWith("_test.go")) {
    return null;
  }
  return `${match[1]}/${match[2]}`;
}

export function rustValidationRequired(paths) {
  const patterns = [
    /^engine\/.*\.rs$/,
    /^engine\/(?:Cargo\.toml|Cargo\.lock|rust-toolchain\.toml)$/,
    /^engine\/crates\/.*\/Cargo\.toml$/,
    /^engine\/include\/mornlea_engine\.h$/,
    /^internal\/mesh\/native[^/]*\.go$/,
    /^internal\/mesh\/registry\.go$/,
    /^Makefile$/,
    /^\.github\/workflows\/ci\.yml$/,
  ];
  return paths.some((path) => patterns.some((pattern) => pattern.test(path)));
}

export function openSpecRequirementReasons(paths) {
  const reasons = [];
  const highRiskPatterns = [
    /^internal\/network\/(?:packet|registry|codec|codec_primitives|frame|chunk_codec|login|stream|tcp)\.go$/,
    /^internal\/storage\/(?:metadata|region_format|chunk_codec|player_codec|player_migration|player_types)\.go$/,
    /^internal\/network\/testdata\/.*\.bin$/,
    /^internal\/storage\/testdata\/.*\.bin$/,
    /^docs\/notes\/perf-baseline\.(?:json|md)$/,
    /^internal\/archcheck\/deps_test\.go$/,
  ];

  if (paths.some((path) => highRiskPatterns.some((pattern) => pattern.test(path)))) {
    reasons.push("改动涉及协议、存档格式、性能基线或架构依赖门禁");
  }

  const components = new Set(paths.map(componentFor).filter(Boolean));
  if (components.size >= 2) {
    reasons.push(`改动跨越多个实现组件：${[...components].sort().join("、")}`);
  }

  return reasons;
}

export function runCommand(
  command,
  argumentsList,
  timeout = 120_000,
  spawn = spawnSync,
  environment = process.env,
) {
  const options = {
    cwd: repositoryRoot,
    encoding: "utf8",
    timeout,
    env: environment,
  };
  const direct = spawn(command, argumentsList, options);
  if (direct.error?.code !== "ENOENT" || !/^[A-Za-z0-9._+-]+$/.test(command)) {
    return direct;
  }

  if (["go", "gofmt"].includes(command)) {
    const roots = [environment.GOROOT, "/usr/local/go"];
    const gvmRoot = resolve(homedir(), ".gvm/gos");
    if (existsSync(gvmRoot)) {
      roots.push(
        ...readdirSync(gvmRoot)
          .sort((left, right) => right.localeCompare(left, undefined, { numeric: true }))
          .map((name) => resolve(gvmRoot, name)),
      );
    }
    for (const root of roots.filter(Boolean)) {
      const goTool = spawn(resolve(root, "bin", command), argumentsList, options);
      if (goTool.error?.code !== "ENOENT") {
        return goTool;
      }
    }
  }

  for (const directory of ["/opt/homebrew/bin", "/usr/local/bin"]) {
    const installed = spawn(resolve(directory, command), argumentsList, options);
    if (installed.error?.code !== "ENOENT") {
      return installed;
    }
  }

  const shell = environment.SHELL;
  if (!shell) {
    return direct;
  }
  const lookup = spawn(shell, ["-lc", `command -v ${command}`], options);
  if (lookup.error || lookup.status !== 0) {
    return direct;
  }
  const executable = (lookup.stdout ?? "")
    .split(/\r?\n/)
    .map((line) => line.trim())
    .find((line) => line.startsWith("/"));
  return executable ? spawn(executable, argumentsList, options) : direct;
}

const run = runCommand;

function commandFailure(label, result) {
  if (result.error) {
    return `${label} 无法运行：${result.error.message}`;
  }
  if (result.status === 0) {
    return null;
  }
  const details = `${result.stdout ?? ""}\n${result.stderr ?? ""}`.trim();
  return `${label} 失败${details ? `：\n${details}` : ""}`;
}

function nulSeparated(result, label) {
  const failure = commandFailure(label, result);
  if (failure) {
    throw new Error(failure);
  }
  return (result.stdout ?? "").split("\0").filter(Boolean);
}

export function changedPaths(execute = run) {
  const tracked = nulSeparated(
    execute("git", ["diff", "--name-only", "--diff-filter=ACMRD", "-z", "HEAD", "--"]),
    "读取已跟踪改动",
  );
  const untracked = nulSeparated(
    execute("git", ["ls-files", "--others", "--exclude-standard", "-z"]),
    "读取未跟踪改动",
  );
  return [...new Set([...tracked, ...untracked])].filter(
    (path) =>
      !path.startsWith(".worktrees/") &&
      !path.startsWith("midscene_run/") &&
      !path.startsWith("vendor/"),
  );
}

function changedGoFiles(paths) {
  return paths.filter((path) => path.endsWith(".go") && existsSync(resolve(repositoryRoot, path)));
}

function gofmtFailure(paths, execute = run) {
  const files = changedGoFiles(paths);
  if (files.length === 0) {
    return null;
  }
  const result = execute("gofmt", ["-l", ...files], 30_000);
  const failure = commandFailure("gofmt 检查", result);
  if (failure) {
    return failure;
  }
  const unformatted = (result.stdout ?? "").trim();
  return unformatted ? `以下 Go 文件尚未 gofmt：\n${unformatted}` : null;
}

function hasReadyActiveChange() {
  const changesRoot = resolve(repositoryRoot, "openspec/changes");
  if (!existsSync(changesRoot)) {
    return false;
  }

  return readdirSync(changesRoot, { withFileTypes: true }).some((entry) => {
    if (!entry.isDirectory() || entry.name === "archive") {
      return false;
    }
    const root = resolve(changesRoot, entry.name);
    if (!existsSync(resolve(root, "proposal.md")) || !existsSync(resolve(root, "tasks.md"))) {
      return false;
    }
    const metadataPath = resolve(root, ".openspec.yaml");
    const skipsSpecs =
      existsSync(metadataPath) && /(^|\n)\s*skip_specs:\s*true\s*($|\n)/.test(readFileSync(metadataPath, "utf8"));
    if (skipsSpecs) {
      return true;
    }
    const specsRoot = resolve(root, "specs");
    if (!existsSync(specsRoot)) {
      return false;
    }
    const pending = [specsRoot];
    while (pending.length > 0) {
      const directory = pending.pop();
      for (const child of readdirSync(directory, { withFileTypes: true })) {
        const childPath = resolve(directory, child.name);
        if (child.isDirectory()) {
          pending.push(childPath);
        } else if (child.isFile() && child.name.endsWith(".md")) {
          return true;
        }
      }
    }
    return false;
  });
}

export function stopFailures(paths, execute = run, environment = process.env) {
  const failures = [];
  const diffCheck = execute("git", ["diff", "--check"], 30_000);
  const diffFailure = commandFailure("git diff --check", diffCheck);
  if (diffFailure) {
    failures.push(diffFailure);
  }

  const formatFailure = gofmtFailure(paths, execute);
  if (formatFailure) {
    failures.push(formatFailure);
  }

  const specReasons = openSpecRequirementReasons(paths);
  const readyActiveChange = hasReadyActiveChange();
  if (
    specReasons.length > 0 &&
    process.env.MORNLEA_HOOKS_ALLOW_NO_SPEC !== "1" &&
    !readyActiveChange
  ) {
    failures.push(
      `检测到必须走 OpenSpec 的改动，但没有完整的 active change：\n- ${specReasons.join("\n- ")}\n` +
        "请先生成 proposal、delta specs 和 tasks；仅在用户明确批准例外时设置 MORNLEA_HOOKS_ALLOW_NO_SPEC=1。",
    );
  }

  const goFiles = changedGoFiles(paths);
  const needsRustValidation = rustValidationRequired(paths);
  let cargoOverride = [];
  if ((goFiles.length > 0 || needsRustValidation) && environment.SHELL) {
    const lookup = execute(environment.SHELL, ["-lc", "command -v cargo"], 30_000);
    if (!lookup.error && lookup.status === 0) {
      const cargo = (lookup.stdout ?? "")
        .split(/\r?\n/)
        .map((line) => line.trim())
        .find((line) => line.startsWith("/"));
      if (cargo) {
        cargoOverride = [`CARGO=${cargo}`];
      }
    }
  }
  if (goFiles.length > 0 || needsRustValidation) {
    const rust = execute("make", ["rust", ...cargoOverride]);
    const rustFailure = commandFailure("Rust 构建", rust);
    if (rustFailure) {
      failures.push(rustFailure);
    }
  }

  if (needsRustValidation) {
    const failure = commandFailure(
      "Rust 检查",
      execute("make", ["rust-check", ...cargoOverride]),
    );
    if (failure) {
      failures.push(failure);
    }
  }

  if (goFiles.length > 0) {
    const architecture = execute(
      "go",
      ["test", "./internal/archcheck", "-count=1"],
      120_000,
    );
    const architectureFailure = commandFailure("架构门禁", architecture);
    if (architectureFailure) {
      failures.push(architectureFailure);
    }

    const packageArguments = [
      ...new Set(
        goFiles
          .map((path) => dirname(path))
          .filter((directory) => directory !== ".")
          .map((directory) => `./${directory}`),
      ),
    ].sort();
    if (packageArguments.length > 0) {
      const tests = execute(
        "go",
        ["test", "-race", "-count=1", ...packageArguments],
        180_000,
      );
      const testFailure = commandFailure("受影响包测试", tests);
      if (testFailure) {
        failures.push(testFailure);
      }
    }

    const vet = execute("go", ["vet", "./..."], 180_000);
    const vetFailure = commandFailure("go vet", vet);
    if (vetFailure) {
      failures.push(vetFailure);
    }
  } else if (needsRustValidation) {
    const tests = execute(
      "go",
      ["test", "./internal/mesh", "./internal/client", "-race", "-count=1"],
      180_000,
    );
    const testFailure = commandFailure("native 下游测试", tests);
    if (testFailure) {
      failures.push(testFailure);
    }
  }

  if (readyActiveChange || paths.some((path) => path.startsWith("openspec/"))) {
    const validation = execute(
      "openspec",
      ["validate", "--all", "--strict", "--no-interactive"],
      120_000,
    );
    const validationFailure = commandFailure("OpenSpec 严格校验", validation);
    if (validationFailure) {
      failures.push(validationFailure);
    }
  }

  return failures;
}

function fail(message) {
  process.stderr.write(`[mornlea hook] ${message}\n`);
  process.exitCode = 2;
}

async function readInput() {
  let input = "";
  for await (const chunk of process.stdin) {
    input += chunk;
  }
  try {
    return JSON.parse(input || "{}");
  } catch (error) {
    throw new Error(`Hook 输入不是有效 JSON：${error.message}`);
  }
}

async function main() {
  const input = await readInput();
  const event = input.hook_event_name;

  if (event === "PreToolUse") {
    const command = input.tool_input?.command ?? input.tool_input?.cmd;
    const blocked = findBlockedCommand(command);
    if (blocked) {
      fail(blocked);
      return;
    }
    process.stdout.write("{}\n");
    return;
  }

  const paths = changedPaths();
  if (event === "PostToolUse") {
    const failure = gofmtFailure(paths);
    if (failure) {
      fail(failure);
      return;
    }
    process.stdout.write("{}\n");
    return;
  }

  if (event === "Stop") {
    const failures = stopFailures(paths);
    if (failures.length === 0) {
      process.stdout.write("{}\n");
      return;
    }
    const message = failures.map((failure, index) => `${index + 1}. ${failure}`).join("\n\n");
    if (input.stop_hook_active) {
      process.stdout.write(
        `${JSON.stringify({
          continue: true,
          systemMessage: `[mornlea hook] 复检仍未通过；为避免 Stop 循环，本次允许结束：\n${message}`,
        })}\n`,
      );
      return;
    }
    fail(`停止前检查未通过：\n${message}`);
    return;
  }

  process.stdout.write("{}\n");
}

if (resolve(process.argv[1] ?? "") === scriptPath) {
  main().catch((error) => fail(error.stack ?? error.message));
}
