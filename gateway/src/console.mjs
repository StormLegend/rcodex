export function renderConsoleHtml({ gatewayName, version }) {
  return `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>${gatewayName} Console</title>
<style>
  :root { color-scheme: dark; }
  body { margin:0; font:14px/1.5 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif; background:#0b1220; color:#e2e8f0; }
  header { padding:12px 16px; background:#111c33; display:flex; gap:12px; align-items:center; border-bottom:1px solid #1e2a44; }
  header b { font-size:15px; }
  header span { color:#7c8bab; font-size:12px; }
  main { display:grid; grid-template-columns: 280px 1fr; gap:0; height: calc(100vh - 49px); }
  #sidebar { border-right:1px solid #1e2a44; overflow:auto; padding:12px; }
  #detail { display:flex; flex-direction:column; min-width:0; }
  #log { flex:1; overflow:auto; padding:16px; white-space:pre-wrap; font-family:ui-monospace, Menlo, Consolas, monospace; font-size:13px; }
  footer { border-top:1px solid #1e2a44; padding:10px; display:flex; gap:8px; }
  input, button, textarea { font:inherit; background:#0f1a2e; color:#e2e8f0; border:1px solid #263758; border-radius:8px; padding:8px 10px; }
  button { cursor:pointer; background:#1d4ed8; border-color:#1d4ed8; }
  button.secondary { background:#16233c; border-color:#263758; }
  textarea { flex:1; resize:none; height:44px; }
  .session { padding:8px; border-radius:8px; cursor:pointer; }
  .session:hover { background:#16233c; }
  .session.active { background:#1d2f52; }
  .session small { display:block; color:#7c8bab; }
  .row { display:flex; gap:8px; align-items:center; margin-bottom:8px; flex-wrap:wrap; }
  .msg-user { color:#93c5fd; }
  .msg-agent { color:#e2e8f0; }
  .muted { color:#7c8bab; }
  #login { max-width:360px; margin:80px auto; padding:24px; background:#111c33; border:1px solid #1e2a44; border-radius:12px; }
  #login input { width:100%; box-sizing:border-box; margin-bottom:10px; }
  svg { background:#f8fafc; border-radius:8px; }
</style>
</head>
<body>
<header><b>${gatewayName}</b><span>v${version} · clean-room gateway</span></header>
<div id="login">
  <h3 style="margin-top:0">登录控制台</h3>
  <input id="username" placeholder="用户名" autocomplete="username"/>
  <input id="password" type="password" placeholder="密码" autocomplete="current-password"/>
  <div id="captcha" style="margin-bottom:10px"></div>
  <input id="captchaAnswer" placeholder="验证码答案"/>
  <button onclick="doLogin()">登录</button>
  <div id="loginError" class="muted" style="margin-top:8px"></div>
</div>
<main id="app" style="display:none">
  <aside id="sidebar">
    <div class="row">
      <input id="workspace" style="flex:1" placeholder="工作目录"/>
      <button onclick="createSession()">新建</button>
    </div>
    <div id="sessions"></div>
  </aside>
  <section id="detail">
    <div id="log" class="muted">选择一个会话，或在左上角新建。</div>
    <footer>
      <textarea id="prompt" placeholder="输入指令，Enter 发送（Shift+Enter 换行）"></textarea>
      <button onclick="sendTurn()">发送</button>
    </footer>
  </section>
</main>
<script>
let token = localStorage.getItem("rcodex-gateway-token") || "";
let currentSession = "";
let stream;

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + token, ...(options.headers || {}) },
  });
  const text = await response.text();
  const body = text ? JSON.parse(text) : {};
  if (!response.ok) throw new Error(body.message || body.error || response.statusText);
  return body;
}

function logLine(text, cls = "") {
  const el = document.getElementById("log");
  el.classList.remove("muted");
  el.innerHTML += '<span class="' + cls + '">' + text.replace(/[<>&]/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;" }[c])) + "</span>\\n";
  el.scrollTop = el.scrollHeight;
}

async function loadCaptcha() {
  const response = await fetch("/auth/captcha");
  const data = await response.json();
  document.getElementById("captcha").innerHTML = data.imageSvg;
  window.__captchaId = data.captchaId;
  document.getElementById("captchaAnswer").value = "";
}

async function doLogin() {
  try {
    const data = await api("/auth/login", {
      method: "POST",
      body: JSON.stringify({
        username: document.getElementById("username").value,
        password: document.getElementById("password").value,
        captchaId: window.__captchaId,
        captchaAnswer: document.getElementById("captchaAnswer").value,
      }),
    });
    token = data.token;
    localStorage.setItem("rcodex-gateway-token", token);
    document.getElementById("login").style.display = "none";
    document.getElementById("app").style.display = "grid";
    await loadSessions();
  } catch (error) {
    document.getElementById("loginError").textContent = String(error.message || error);
    await loadCaptcha();
  }
}

async function loadSessions() {
  const data = await api("/sessions");
  const list = document.getElementById("sessions");
  list.innerHTML = "";
  for (const session of data.sessions || []) {
    const el = document.createElement("div");
    el.className = "session" + (session.id === currentSession ? " active" : "");
    el.innerHTML = "<b>" + (session.title || session.id) + "</b><small>" + session.status + " · " + (session.modelLabel || "") + "</small>";
    el.onclick = () => openSession(session.id);
    list.appendChild(el);
  }
}

function openSession(id) {
  currentSession = id;
  document.getElementById("log").innerHTML = "";
  document.getElementById("log").classList.remove("muted");
  if (stream) stream.close();
  stream = new EventSource("/sessions/" + encodeURIComponent(id) + "/events?token=" + encodeURIComponent(token));
  stream.onmessage = (event) => {
    const entry = JSON.parse(event.data);
    const payload = entry.payload || {};
    if (entry.type === "session-message-delta") logLine(payload.text || "", "msg-agent");
    else if (entry.type === "session-status") logLine("[status] " + payload.status, "muted");
  };
  loadSessions();
}

async function createSession() {
  const prompt = document.getElementById("prompt").value.trim() || "你好，做个自我介绍";
  const workspace = document.getElementById("workspace").value.trim() || undefined;
  const data = await api("/sessions", { method: "POST", body: JSON.stringify({ workspacePath: workspace, prompt }) });
  document.getElementById("prompt").value = "";
  await loadSessions();
  openSession(data.session.id);
}

async function sendTurn() {
  const prompt = document.getElementById("prompt").value.trim();
  if (!prompt || !currentSession) return;
  document.getElementById("prompt").value = "";
  logLine("> " + prompt, "msg-user");
  await api("/sessions/" + encodeURIComponent(currentSession) + "/turns", { method: "POST", body: JSON.stringify({ prompt }) });
}

document.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && !event.shiftKey && event.target.id === "prompt") {
    event.preventDefault();
    sendTurn();
  }
});

if (token) {
  api("/sessions").then(() => {
    document.getElementById("login").style.display = "none";
    document.getElementById("app").style.display = "grid";
    return loadSessions();
  }).catch(() => { token = ""; loadCaptcha(); });
} else {
  loadCaptcha();
}
</script>
</body>
</html>
`;
}
