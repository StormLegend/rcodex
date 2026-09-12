import fs from "node:fs/promises";
import path from "node:path";

function isInside(parent, candidate) {
  const relative = path.relative(parent, candidate);
  return relative === "" || (!relative.startsWith("..") && !path.isAbsolute(relative));
}

export function createFilesystem({ allowedPaths, maxReadBytes = 1024 * 1024 }) {
  async function resolveAllowed(requestedPath) {
    const target = path.resolve(requestedPath || allowedPaths[0]);
    let real;
    try {
      real = await fs.realpath(target);
    } catch {
      real = target;
    }
    const allowed = allowedPaths.find((root) => isInside(root, real));
    if (!allowed) {
      const error = new Error(`path is outside the allowed roots: ${allowedPaths.join(", ")}`);
      error.statusCode = 403;
      error.code = "forbidden";
      throw error;
    }
    return { path: real, root: allowed };
  }

  async function roots() {
    return { roots: [...allowedPaths], defaultRoot: allowedPaths[0] };
  }

  async function list(requestedPath) {
    const { path: target } = await resolveAllowed(requestedPath);
    const dirents = await fs.readdir(target, { withFileTypes: true });
    const entries = [];
    for (const dirent of dirents) {
      const entryPath = path.join(target, dirent.name);
      let stats;
      try {
        stats = await fs.lstat(entryPath);
      } catch {
        continue;
      }
      entries.push({
        name: dirent.name,
        path: entryPath,
        type: dirent.isDirectory() ? "directory" : dirent.isSymbolicLink() ? "symlink" : "file",
        size: stats.size,
        modifiedAt: stats.mtime.toISOString(),
      });
    }
    entries.sort((a, b) => (a.type === b.type ? a.name.localeCompare(b.name) : a.type === "directory" ? -1 : 1));
    const parentPath = path.dirname(target);
    return {
      path: target,
      parentPath: allowedPaths.some((root) => path.resolve(root) === path.resolve(target)) ? null : parentPath,
      entries,
    };
  }

  async function read(requestedPath) {
    const { path: target } = await resolveAllowed(requestedPath);
    const stats = await fs.stat(target);
    if (stats.isDirectory()) {
      const error = new Error("path is a directory");
      error.statusCode = 400;
      throw error;
    }
    const handle = await fs.open(target, "r");
    try {
      const length = Math.min(stats.size, maxReadBytes);
      const buffer = Buffer.alloc(length);
      await handle.read(buffer, 0, length, 0);
      return {
        path: target,
        size: stats.size,
        truncated: stats.size > length,
        content: buffer.toString("utf8"),
        modifiedAt: stats.mtime.toISOString(),
      };
    } finally {
      await handle.close();
    }
  }

  return { roots, list, read, resolveAllowed };
}
