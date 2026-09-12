#!/usr/bin/env node

import { createReadStream } from "node:fs";
import { readdir } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import readline from "node:readline";
import { pathToFileURL } from "node:url";

function argument(name) {
  const index = process.argv.indexOf(name);
  return index >= 0 ? process.argv[index + 1] : undefined;
}

async function jsonlFiles(directory) {
  return (await readdir(directory))
    .filter((name) => name.endsWith(".jsonl"))
    .sort()
    .map((name) => path.join(directory, name));
}

async function forEachEntry(files, callback) {
  for (const file of files) {
    const input = createReadStream(file, "utf8");
    const lines = readline.createInterface({ input, crlfDelay: Infinity });
    for await (const line of lines) {
      if (!line.trim()) continue;
      await callback(JSON.parse(line));
    }
  }
}

async function main() {
  const eventsDirectory = argument("--events");
  const consoleUtilsPath = argument("--console-utils");
  let itemId = argument("--item-id");
  if (!eventsDirectory || !consoleUtilsPath) {
    throw new Error("Usage: --events <session-event-dir> --console-utils <console-utils.js> [--item-id <id>]");
  }

  const files = await jsonlFiles(eventsDirectory);
  let completedTextLength = 0;
  if (!itemId) {
    await forEachEntry(files, async (entry) => {
      const payload = entry?.message?.payload;
      const item = payload?.jsonPayload?.item;
      if (payload?.eventType === "item/completed"
        && item?.type === "agentMessage"
        && typeof item.text === "string"
        && item.text.endsWith("…")) {
        itemId = item.id;
        completedTextLength = item.text.length;
      }
    });
  }

  if (!itemId) throw new Error("No truncated completed agent message was found");

  const selectedEntries = [];
  const selectedDeltaEntries = [];
  let rawDeltaCharacters = 0;
  let displayedStreamCharacters = 0;
  await forEachEntry(files, async (entry) => {
    const payload = entry?.message?.payload;
    const jsonPayload = payload?.jsonPayload;
    const isDelta = payload?.eventType === "item/agentMessage/delta"
      && jsonPayload?.itemId === itemId;
    const isCompletion = payload?.eventType === "item/completed"
      && jsonPayload?.item?.id === itemId;
    if (!isDelta && !isCompletion) return;
    if (isDelta) {
      rawDeltaCharacters += String(jsonPayload.delta || "").length;
      displayedStreamCharacters += String(payload.chunk || jsonPayload.delta || "").length;
      selectedDeltaEntries.push(entry);
    }
    if (isCompletion) completedTextLength = String(jsonPayload.item.text || "").length;
    selectedEntries.push(entry);
  });

  const consoleUtils = await import(pathToFileURL(path.resolve(consoleUtilsPath)).href);
  const streamedViews = consoleUtils.sessionMessageViews(selectedDeltaEntries);
  const streamedAssistant = streamedViews.find(
    (view) => view.kind === "assistant" && view.itemId === itemId,
  );
  if (!streamedAssistant) throw new Error(`No streamed assistant view was reconstructed for ${itemId}`);

  const views = consoleUtils.sessionMessageViews(selectedEntries);
  const assistant = views.find((view) => view.kind === "assistant" && view.itemId === itemId);
  if (!assistant) throw new Error(`No assistant view was reconstructed for ${itemId}`);

  const replayedCharacters = assistant.text.length;
  const streamedViewCharacters = streamedAssistant.text.length;
  console.log(`item_id=${itemId}`);
  console.log(`raw_delta_total_chars=${rawDeltaCharacters}`);
  console.log(`displayed_stream_chars=${displayedStreamCharacters}`);
  console.log(`streamed_view_chars=${streamedViewCharacters}`);
  console.log(`stored_completed_chars=${completedTextLength}`);
  console.log(`replayed_view_chars=${replayedCharacters}`);

  if (replayedCharacters !== streamedViewCharacters) {
    throw new Error(
      `Completed replay length ${replayedCharacters} does not preserve streamed view length ${streamedViewCharacters}`,
    );
  }
  if (replayedCharacters <= completedTextLength) {
    throw new Error(
      `Completed replay ${replayedCharacters} did not preserve text beyond stored completion ${completedTextLength}`,
    );
  }
  console.log("real_event_replay=PASS");
}

main().catch((error) => {
  console.error(`real_event_replay=FAIL: ${error.message}`);
  process.exitCode = 1;
});
