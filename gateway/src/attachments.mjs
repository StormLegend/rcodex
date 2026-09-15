import fs from "node:fs/promises";
import path from "node:path";
import { newId, nowIso, trimmed } from "./util.mjs";

const DEFAULT_MAX_BYTES = 10 * 1024 * 1024;
const MAX_PROMPT_BYTES = 128 * 1024;

function safeName(value) {
  const name = path.basename(trimmed(value) || "attachment").replace(/[^a-zA-Z0-9._-]/g, "_");
  return name.slice(0, 160) || "attachment";
}

function decodeBase64(value) {
  const encoded = trimmed(value).replace(/^data:[^;]+;base64,/, "");
  if (!encoded || !/^[a-zA-Z0-9+/]*={0,2}$/.test(encoded) || encoded.length % 4 === 1) {
    const error = new Error("attachment data must be valid base64");
    error.statusCode = 400;
    error.code = "invalid_attachment_data";
    throw error;
  }
  return Buffer.from(encoded, "base64");
}

export function createAttachmentStore({ files, maxBytes = DEFAULT_MAX_BYTES } = {}) {
  async function store(session, input) {
    const resolved = await files.resolveAllowed(session.workspacePath);
    const id = newId();
    const name = safeName(input?.name);
    const data = decodeBase64(input?.data);
    if (data.length === 0) {
      const error = new Error("attachment is empty");
      error.statusCode = 400;
      error.code = "empty_attachment";
      throw error;
    }
    if (data.length > maxBytes) {
      const error = new Error(`attachment exceeds ${maxBytes} bytes`);
      error.statusCode = 413;
      error.code = "attachment_too_large";
      throw error;
    }
    const directory = path.join(resolved.path, ".rcodex", "attachments", session.id);
    await fs.mkdir(directory, { recursive: true, mode: 0o700 });
    const storedName = `${id}-${name}`;
    const target = path.join(directory, storedName);
    await fs.writeFile(target, data, { flag: "wx", mode: 0o600 });
    const relativePath = path.posix.join(".rcodex", "attachments", session.id, storedName);
    return {
      id,
      name,
      mimeType: trimmed(input?.mimeType) || "application/octet-stream",
      size: data.length,
      relativePath,
      kind: trimmed(input?.kind) || (String(input?.mimeType || "").startsWith("image/") ? "image" : "file"),
      createdAt: nowIso(),
    };
  }

  async function promptAttachment(session, attachment) {
    const target = path.resolve(session.workspacePath, attachment.relativePath);
    const resolved = await files.resolveAllowed(target);
    if (resolved.path !== target) {
      const error = new Error("attachment path is outside the allowed roots");
      error.statusCode = 403;
      error.code = "forbidden";
      throw error;
    }
    const withPath = { ...attachment, path: target };
    if (attachment.kind === "image" || attachment.mimeType?.startsWith("image/")) return withPath;
    const data = await fs.readFile(target);
    return { ...withPath, content: data.subarray(0, MAX_PROMPT_BYTES).toString("utf8") };
  }

  async function read(session, attachment) {
    const target = path.resolve(session.workspacePath, attachment.relativePath);
    const resolved = await files.resolveAllowed(target);
    if (resolved.path !== target) {
      const error = new Error("attachment path is outside the allowed roots");
      error.statusCode = 403;
      error.code = "forbidden";
      throw error;
    }
    return { ...attachment, data: await fs.readFile(target) };
  }

  return { store, promptAttachment, read, maxBytes };
}
