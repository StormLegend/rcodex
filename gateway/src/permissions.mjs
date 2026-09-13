export const PERMISSION_MODES = ["ask", "auto", "full"];

export const PERMISSION_MODE_LABELS = {
  ask: "请求批准",
  auto: "帮我批准",
  full: "完全访问",
};

export function parsePermissionMode(value, fallback = "full") {
  if (value === undefined || value === null || value === "") return fallback;
  if (!PERMISSION_MODES.includes(value)) {
    const error = new Error(`permissionMode 仅支持 ask、auto、full（收到 ${value}）`);
    error.statusCode = 400;
    error.code = "unsupported_permission_mode";
    throw error;
  }
  return value;
}

/**
 * Mode → Codex app-server settings.
 *
 *  ask  (请求批准): sandboxed to the workspace, every escalation is asked of the human
 *  auto (帮我批准): same sandbox, but Codex's own reviewer approves escalations
 *  full (完全访问): no sandbox, never asks
 *
 * Note the asymmetric shapes: `thread/start` takes `sandbox` as a string while
 * `turn/start` takes `sandboxPolicy` as the internally tagged enum.
 */
export function permissionSettingsForMode(mode, workspacePath) {
  if (mode === "full") {
    return {
      approvalPolicy: "never",
      approvalsReviewer: "user",
      sandbox: "danger-full-access",
      sandboxPolicy: { type: "dangerFullAccess" },
    };
  }
  return {
    approvalPolicy: "on-request",
    approvalsReviewer: mode === "auto" ? "auto_review" : "user",
    sandbox: "workspace-write",
    sandboxPolicy: {
      type: "workspaceWrite",
      writableRoots: [workspacePath],
      networkAccess: false,
      excludeTmpdirEnvVar: false,
      excludeSlashTmp: false,
    },
  };
}

export function isApprovalRequestMethod(method) {
  return (
    method === "item/commandExecution/requestApproval" ||
    method === "item/fileChange/requestApproval" ||
    method === "item/permissions/requestApproval" ||
    method === "execCommandApproval" ||
    method === "applyPatchApproval" ||
    method.endsWith("/requestApproval") ||
    method.endsWith("Approval")
  );
}

export function approvalFamily(method) {
  if (method === "execCommandApproval" || method === "item/commandExecution/requestApproval") return "command";
  if (method === "applyPatchApproval" || method === "item/fileChange/requestApproval") return "file-change";
  if (method === "item/permissions/requestApproval") return "permissions";
  return method;
}
