import assert from "node:assert/strict";
import test from "node:test";
import {
  approvalFamily,
  isApprovalRequestMethod,
  parsePermissionMode,
  permissionSettingsForMode,
} from "../src/permissions.mjs";

test("the three permission modes map onto the documented app-server settings", () => {
  const ask = permissionSettingsForMode("ask", "/work");
  assert.equal(ask.approvalPolicy, "on-request");
  assert.equal(ask.approvalsReviewer, "user");
  assert.equal(ask.sandbox, "workspace-write");
  assert.deepEqual(ask.sandboxPolicy, {
    type: "workspaceWrite",
    writableRoots: ["/work"],
    networkAccess: false,
    excludeTmpdirEnvVar: false,
    excludeSlashTmp: false,
  });

  const auto = permissionSettingsForMode("auto", "/work");
  assert.equal(auto.approvalPolicy, "on-request");
  assert.equal(auto.approvalsReviewer, "auto_review", "auto mode is reviewed by Codex itself");
  assert.equal(auto.sandbox, "workspace-write");

  const full = permissionSettingsForMode("full", "/work");
  assert.equal(full.approvalPolicy, "never");
  assert.equal(full.sandbox, "danger-full-access");
  assert.deepEqual(full.sandboxPolicy, { type: "dangerFullAccess" });
});

test("permission modes are validated, with a configurable default", () => {
  assert.equal(parsePermissionMode(undefined, "full"), "full");
  assert.equal(parsePermissionMode("ask", "full"), "ask");
  assert.throws(() => parsePermissionMode("yolo", "full"), /ask、auto、full/);
});

test("approval request methods are recognised and classified", () => {
  assert.equal(isApprovalRequestMethod("item/commandExecution/requestApproval"), true);
  assert.equal(isApprovalRequestMethod("applyPatchApproval"), true);
  assert.equal(isApprovalRequestMethod("item/tool/requestUserInput"), false);
  assert.equal(approvalFamily("item/commandExecution/requestApproval"), "command");
  assert.equal(approvalFamily("applyPatchApproval"), "file-change");
  assert.equal(approvalFamily("item/permissions/requestApproval"), "permissions");
});
