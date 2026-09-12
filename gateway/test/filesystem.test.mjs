import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { createFilesystem } from "../src/filesystem.mjs";

function tempWorkspace() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "rcodex-fs-"));
  fs.writeFileSync(path.join(dir, "hello.txt"), "hello world");
  fs.mkdirSync(path.join(dir, "nested"));
  return dir;
}

test("lists and reads files inside the allowed root", async () => {
  const dir = tempWorkspace();
  const files = createFilesystem({ allowedPaths: [dir] });
  const listing = await files.list(dir);
  assert.deepEqual(
    listing.entries.map((entry) => entry.name).sort(),
    ["hello.txt", "nested"],
  );
  const read = await files.read(path.join(dir, "hello.txt"));
  assert.equal(read.content, "hello world");
  assert.equal(read.truncated, false);
});

test("refuses paths outside the allowed roots", async () => {
  const dir = tempWorkspace();
  const files = createFilesystem({ allowedPaths: [dir] });
  await assert.rejects(() => files.list(path.join(dir, "..")), (error) => error.statusCode === 403);
  await assert.rejects(() => files.read("/etc/hostname"), (error) => error.statusCode === 403);
});

test("truncates large files instead of blowing up memory", async () => {
  const dir = tempWorkspace();
  fs.writeFileSync(path.join(dir, "big.txt"), "x".repeat(5000));
  const files = createFilesystem({ allowedPaths: [dir], maxReadBytes: 1024 });
  const read = await files.read(path.join(dir, "big.txt"));
  assert.equal(read.truncated, true);
  assert.equal(read.content.length, 1024);
});
