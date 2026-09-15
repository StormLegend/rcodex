import { execFile } from "node:child_process";
import { promisify } from "node:util";

const execFileAsync = promisify(execFile);

async function git(cwd, args) {
  try {
    const result = await execFileAsync("git", ["-C", cwd, ...args], {
      encoding: "utf8",
      maxBuffer: 2 * 1024 * 1024,
      timeout: 15_000,
    });
    return { ok: true, output: result.stdout };
  } catch (error) {
    if (error?.code === 128 || /not a git repository/i.test(String(error?.stderr || error?.message))) {
      return { ok: false, notRepository: true, output: "" };
    }
    const failure = new Error(`git command failed: ${error?.stderr || error?.message || error}`);
    failure.statusCode = 502;
    failure.code = "git_command_failed";
    throw failure;
  }
}

function parseStatus(output) {
  return output.split(/\r?\n/).filter(Boolean).map((line) => ({
    index: line.slice(0, 2),
    path: line.slice(3),
    staged: line[0] !== " " && line[0] !== "?",
    unstaged: line[1] !== " ",
  }));
}

export function createChangesService({ files }) {
  async function status(workspacePath) {
    const resolved = await files.resolveAllowed(workspacePath);
    const result = await git(resolved.path, ["status", "--short", "--untracked-files=all"]);
    return {
      workspacePath: resolved.path,
      repository: !result.notRepository,
      files: result.notRepository ? [] : parseStatus(result.output),
    };
  }

  async function diff(workspacePath) {
    const resolved = await files.resolveAllowed(workspacePath);
    const result = await git(resolved.path, ["diff", "--no-ext-diff", "--binary"]);
    return { workspacePath: resolved.path, repository: !result.notRepository, diff: result.output };
  }

  return { status, diff };
}
