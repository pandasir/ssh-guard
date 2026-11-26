const state = {
  isDesktop: false,
  wsBase: "",
  sessionToken: "",
  platform: "",
  dataDir: "",
  initialized: false,
  hosts: [],
  activeHostId: null,
  activeDesktopSessionId: null,
  ws: null,
  term: null,
  fitAddon: null,
  pendingTofu: null,
  replayTimer: null,
  replayTerm: null,
  replayPaused: false,
};

async function detectRuntime() {
  if (window.go && window.go.main && window.go.main.DesktopApp && typeof window.go.main.DesktopApp.APIRequest === "function") {
    const cfg = await window.go.main.DesktopApp.GetRuntimeConfig();
    state.isDesktop = true;
    state.platform = cfg.platform || "";
    state.dataDir = cfg.data_dir || "";

    document.getElementById("runtime-badge").textContent = `DESKTOP · ${state.platform.toUpperCase()}`;
    document.getElementById("sidebar-mode-pill").textContent = "DESKTOP";
    document.getElementById("btn-pick-key").classList.remove("hidden");
    document.getElementById("btn-open-data").classList.remove("hidden");
    const platEl = document.getElementById("platform-label");
    platEl.textContent = `本地桌面端 · 纯进程内 IPC (${state.platform})`;
    platEl.classList.remove("hidden");

    // Register global Wails SSH event listener once
    if (window.runtime && typeof window.runtime.EventsOn === "function") {
      window.runtime.EventsOn("ssh:event", (payload) => {
        if (!payload || payload.session_id !== state.activeDesktopSessionId) return;
        const host = state.hosts.find((h) => h.id === state.activeHostId);
        if (payload.event === "msg" && payload.msg) {
          handleSSHMessage(state.activeHostId, host, payload.msg);
        } else if (payload.event === "closed") {
          state.activeDesktopSessionId = null;
          setConnStatus("idle", `会话已断开 · ${host ? host.name : ""}`);
        }
      });
    }
  } else {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    state.isDesktop = false;
    state.wsBase = `${proto}//${location.host}`;
  }
}

async function rawRequest(path, opts = {}) {
  const method = (opts.method || "GET").toUpperCase();
  const body = opts.body ? String(opts.body) : "";

  if (state.isDesktop) {
    const resp = await window.go.main.DesktopApp.APIRequest(method, path, body, state.sessionToken || "");
    return { status: resp.status, text: resp.body || "" };
  }

  const headers = Object.assign(
    { "Content-Type": "application/json", "X-Requested-With": "SSHGuard" },
    opts.headers || {}
  );
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  const text = await res.text();
  return { status: res.status, text };
}

async function api(path, opts = {}) {
  const { status, text } = await rawRequest(path, opts);
  if (status === 401) {
    state.sessionToken = "";
    showVaultScreen();
    throw new Error("会话已过期或凭证库已锁定");
  }
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch (_) { data = { raw: text }; }
  if (status < 200 || status >= 300) {
    throw new Error(data.error || `请求失败 (${status})`);
  }
  return data;
}

// --- Vault Bootstrap ---

async function initApp() {
  try {
    await detectRuntime();
    const status = await api("/api/vault/status");
    state.initialized = status.initialized;
    if (status.authenticated) {
      showAppShell();
      await loadHosts();
    } else {
      showVaultScreen();
    }
  } catch (err) {
    showVaultScreen();
    const errEl = document.getElementById("vault-error");
    errEl.textContent = "初始化通信异常: " + (err && err.message ? err.message : String(err));
    errEl.classList.remove("hidden");
  }
}

function showVaultScreen() {
  disconnectSession();
  document.getElementById("app-shell").classList.add("hidden");
  document.getElementById("vault-screen").classList.remove("hidden");
  const title = document.getElementById("vault-title");
  const submit = document.getElementById("vault-submit");
  const confirmWrap = document.getElementById("confirm-wrap");
  if (!state.initialized) {
    title.textContent = "初始化主密钥 (首次启动)";
    submit.textContent = "创建保险库并进入";
    confirmWrap.classList.remove("hidden");
  } else {
    title.textContent = "解锁凭证保险库";
    submit.textContent = "解锁进入面板";
    confirmWrap.classList.add("hidden");
  }
  document.getElementById("master-password").value = "";
  document.getElementById("master-password-confirm").value = "";
  document.getElementById("vault-error").classList.add("hidden");
}

function showAppShell() {
  document.getElementById("vault-screen").classList.add("hidden");
  document.getElementById("app-shell").classList.remove("hidden");
}

document.getElementById("vault-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const pw = document.getElementById("master-password").value;
  const confirmPw = document.getElementById("master-password-confirm").value;
  const errEl = document.getElementById("vault-error");
  errEl.classList.add("hidden");

  if (!state.initialized && pw !== confirmPw) {
    errEl.textContent = "两次输入的主密码不一致";
    errEl.classList.remove("hidden");
    return;
  }
  try {
    const endpoint = state.initialized ? "/api/vault/unlock" : "/api/vault/setup";
    const resp = await api(endpoint, {
      method: "POST",
      body: JSON.stringify({ master_password: pw }),
    });
    if (resp && resp.session_token) {
      state.sessionToken = resp.session_token;
    }
    state.initialized = true;
    showAppShell();
    await loadHosts();
  } catch (err) {
    errEl.textContent = err.message || String(err);
    errEl.classList.remove("hidden");
  }
});

document.getElementById("btn-lock").addEventListener("click", async () => {
  try {
    await api("/api/vault/lock", { method: "POST" });
  } finally {
    state.sessionToken = "";
    showVaultScreen();
  }
});

document.getElementById("btn-open-data").addEventListener("click", async () => {
  if (state.isDesktop && window.go?.main?.DesktopApp?.OpenDataDir) {
    try {
      await window.go.main.DesktopApp.OpenDataDir();
    } catch (err) {
      alert("无法打开数据目录: " + err);
    }
  }
});

document.getElementById("btn-pick-key").addEventListener("click", async () => {
  if (state.isDesktop && window.go?.main?.DesktopApp?.PickPrivateKeyFile) {
    try {
      const keyContent = await window.go.main.DesktopApp.PickPrivateKeyFile();
      if (keyContent) {
        document.getElementById("f-private-key").value = keyContent;
      }
    } catch (err) {
      const errEl = document.getElementById("host-form-error");
      errEl.textContent = "读取私钥文件失败: " + err;
      errEl.classList.remove("hidden");
    }
  }
});

// --- Hosts Management ---

async function loadHosts() {
  state.hosts = await api("/api/hosts");
  renderHosts();
}

function renderHosts() {
  const container = document.getElementById("host-list");
  if (!state.hosts.length) {
    container.innerHTML = `<div class="muted small" style="padding:16px;text-align:center;">暂无主机，点击右上角「+ 主机」添加</div>`;
    return;
  }
  container.innerHTML = state.hosts.map((h) => {
    const isKey = h.auth_type === "private_key";
    const activeCls = state.activeHostId === h.id ? "active" : "";
    const guardTag = h.guard_enabled ? "🛡️" : "";
    const fpTag = h.known_host_fp ? "已绑定指纹" : "待首次验证";
    return `
      <div class="host-item ${activeCls}">
        <div class="host-top">
          <span class="host-name">${escapeHTML(h.name)} ${guardTag}</span>
          <span class="badge ${isKey ? "badge-key" : "badge-pw"}">${isKey ? "公私钥" : "密码"}</span>
        </div>
        <div class="host-addr">${escapeHTML(h.username)}@${escapeHTML(h.address)}:${h.port}</div>
        <div class="muted small" style="margin-bottom:8px;">${fpTag}</div>
        <div class="host-actions">
          <button class="btn btn-primary btn-sm" onclick="connectHost(${h.id})">连接</button>
          <button class="btn btn-ghost btn-sm" onclick="openEditHost(${h.id})">编辑</button>
          <button class="btn btn-danger btn-sm" onclick="deleteHost(${h.id})">删除</button>
        </div>
      </div>
    `;
  }).join("");
}

document.getElementById("btn-add-host").addEventListener("click", () => {
  document.getElementById("host-modal-title").textContent = "新增 SSH 主机";
  document.getElementById("host-id").value = "";
  document.getElementById("host-form").reset();
  document.getElementById("f-port").value = 22;
  document.getElementById("f-guard").checked = true;
  document.getElementById("secret-hint-pw").textContent = "*";
  document.getElementById("secret-hint-key").textContent = "*";
  document.getElementById("host-form-error").classList.add("hidden");
  toggleAuthFields("private_key");
  openModal("host-modal");
});

document.querySelectorAll('input[name="auth_type"]').forEach((r) => {
  r.addEventListener("change", (e) => toggleAuthFields(e.target.value));
});

function toggleAuthFields(authType) {
  const isKey = authType === "private_key";
  document.getElementById("key-field").classList.toggle("hidden", !isKey);
  document.getElementById("password-field").classList.toggle("hidden", isKey);
}

window.openEditHost = function (id) {
  const h = state.hosts.find((x) => x.id === id);
  if (!h) return;
  document.getElementById("host-modal-title").textContent = "编辑 SSH 主机";
  document.getElementById("host-id").value = h.id;
  document.getElementById("f-name").value = h.name;
  document.getElementById("f-username").value = h.username;
  document.getElementById("f-address").value = h.address;
  document.getElementById("f-port").value = h.port;
  document.getElementById("f-guard").checked = h.guard_enabled;
  document.getElementById("f-notes").value = h.notes || "";
  document.getElementById("f-password").value = "";
  document.getElementById("f-private-key").value = "";
  document.getElementById("f-passphrase").value = "";
  document.getElementById("secret-hint-pw").textContent = "（留空则保留原加密密码）";
  document.getElementById("secret-hint-key").textContent = "（留空则保留原加密私钥）";
  document.querySelector(`input[name="auth_type"][value="${h.auth_type}"]`).checked = true;
  toggleAuthFields(h.auth_type);
  document.getElementById("host-form-error").classList.add("hidden");
  openModal("host-modal");
};

document.getElementById("host-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const id = document.getElementById("host-id").value;
  const authType = document.querySelector('input[name="auth_type"]:checked').value;
  const secret = authType === "password"
    ? document.getElementById("f-password").value
    : document.getElementById("f-private-key").value;

  const payload = {
    name: document.getElementById("f-name").value.trim(),
    username: document.getElementById("f-username").value.trim(),
    address: document.getElementById("f-address").value.trim(),
    port: parseInt(document.getElementById("f-port").value, 10) || 22,
    auth_type: authType,
    secret: secret,
    passphrase: document.getElementById("f-passphrase").value,
    guard_enabled: document.getElementById("f-guard").checked,
    notes: document.getElementById("f-notes").value.trim(),
  };

  const errEl = document.getElementById("host-form-error");
  errEl.classList.add("hidden");
  try {
    if (id) {
      await api(`/api/hosts/${id}`, { method: "PUT", body: JSON.stringify(payload) });
    } else {
      await api("/api/hosts", { method: "POST", body: JSON.stringify(payload) });
    }
    closeModal("host-modal");
    await loadHosts();
  } catch (err) {
    errEl.textContent = err.message;
    errEl.classList.remove("hidden");
  }
});

window.deleteHost = async function (id) {
  if (!confirm("确认删除该主机及其加密凭据？")) return;
  if (state.activeHostId === id) disconnectSession();
  await api(`/api/hosts/${id}`, { method: "DELETE" });
  await loadHosts();
};

// --- SSH Terminal (IPC in Desktop / WebSocket in Web) ---

function sendTerminalPayload(obj) {
  const raw = JSON.stringify(obj);
  if (state.isDesktop) {
    if (state.activeDesktopSessionId && window.go?.main?.DesktopApp?.SendSSHMessage) {
      window.go.main.DesktopApp.SendSSHMessage(state.activeDesktopSessionId, raw).catch(() => {});
    }
  } else if (state.ws && state.ws.readyState === WebSocket.OPEN) {
    state.ws.send(raw);
  }
}

window.addEventListener("resize", () => {
  if (!state.fitAddon || !state.term) return;
  state.fitAddon.fit();
  sendTerminalPayload({
    type: "resize",
    cols: state.term.cols,
    rows: state.term.rows,
  });
});

function disposeTerminal() {
  if (state.term) {
    try {
      state.term.dispose();
    } catch (_) {}
    state.term = null;
  }
  state.fitAddon = null;
  const container = document.getElementById("terminal-container");
  if (container) {
    container.innerHTML = "";
    container.classList.add("hidden");
  }
  const emptyEl = document.getElementById("terminal-empty");
  if (emptyEl) {
    emptyEl.classList.remove("hidden");
  }
}

function ensureTerminal() {
  if (state.term) {
    try {
      state.term.dispose();
    } catch (_) {}
    state.term = null;
    state.fitAddon = null;
  }
  const container = document.getElementById("terminal-container");
  container.innerHTML = "";

  const term = new Terminal({
    cursorBlink: false,
    scrollback: 500,
    fontSize: 14,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
    theme: {
      background: "#0a0d12",
      foreground: "#e6edf3",
      cursor: "#58a6ff",
    },
  });
  const fitAddon = new FitAddon.FitAddon();
  term.loadAddon(fitAddon);
  term.open(container);
  fitAddon.fit();

  term.onData((data) => {
    sendTerminalPayload({ type: "input", data });
  });

  state.term = term;
  state.fitAddon = fitAddon;
}

function handleSSHMessage(hostId, host, msg) {
  switch (msg.type) {
    case "output":
      if (state.term) state.term.write(msg.data);
      break;
    case "guard_blocked":
      if (state.term) state.term.write(msg.data);
      break;
    case "status":
      if (msg.data === "CONNECTED") {
        setConnStatus("connected", `已连接 · ${host ? host.name : ""} (${host ? `${host.username}@${host.address}:${host.port}` : ""})`);
        if (state.term) state.term.focus();
      } else {
        if (state.term) state.term.writeln(`\x1b[36m[SSH-Guard]\x1b[0m ${msg.data}`);
      }
      break;
    case "host_key_verify":
      openTofuModal(hostId, msg, false);
      setConnStatus("error", "等待首次指纹信任 (TOFU)");
      break;
    case "host_key_mismatch":
      openTofuModal(hostId, msg, true);
      setConnStatus("error", "安全阻断：检测到主机公钥变更 (疑似 MITM)");
      break;
    case "error":
      if (state.term) state.term.writeln(`\r\n\x1b[1;31m[错误]\x1b[0m ${msg.data}`);
      setConnStatus("error", `连接失败 · ${host ? host.name : ""}`);
      break;
  }
}

window.connectHost = async function (hostId) {
  const host = state.hosts.find((h) => h.id === hostId);
  if (!host) return;

  disconnectSession();
  state.activeHostId = hostId;
  renderHosts();

  document.getElementById("terminal-empty").classList.add("hidden");
  document.getElementById("terminal-container").classList.remove("hidden");
  document.getElementById("btn-disconnect").classList.remove("hidden");
  setConnStatus("connecting", `正在连接 ${host.name} (${host.username}@${host.address}:${host.port})`);

  ensureTerminal();
  state.fitAddon.fit();

  if (state.isDesktop) {
    const sessID = `sess-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
    state.activeDesktopSessionId = sessID;
    try {
      await window.go.main.DesktopApp.OpenSSHSession(
        sessID,
        hostId,
        state.term.cols,
        state.term.rows,
        state.sessionToken
      );
    } catch (err) {
      state.activeDesktopSessionId = null;
      state.term.writeln(`\r\n\x1b[1;31m[错误]\x1b[0m ${err}`);
      setConnStatus("error", `连接失败 · ${host.name}`);
    }
    return;
  }

  const params = new URLSearchParams({
    cols: String(state.term.cols),
    rows: String(state.term.rows),
  });
  const wsUrl = `${state.wsBase}/ws/ssh/${hostId}?${params.toString()}`;
  const ws = new WebSocket(wsUrl);
  state.ws = ws;

  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data);
    handleSSHMessage(hostId, host, msg);
  };

  ws.onclose = () => {
    if (state.ws === ws) {
      state.ws = null;
      setConnStatus("idle", `会话已断开 · ${host.name}`);
    }
  };
};

function disconnectSession() {
  if (state.isDesktop && state.activeDesktopSessionId) {
    const oldId = state.activeDesktopSessionId;
    state.activeDesktopSessionId = null;
    if (window.go?.main?.DesktopApp?.CloseSSHSession) {
      window.go.main.DesktopApp.CloseSSHSession(oldId).catch(() => {});
    }
  }
  if (state.ws) {
    state.ws.close();
    state.ws = null;
  }
  disposeTerminal();
  state.activeHostId = null;
  document.getElementById("btn-disconnect").classList.add("hidden");
  setConnStatus("idle", "未连接 · 请从左侧选择主机");
  renderHosts();
}

document.getElementById("btn-disconnect").addEventListener("click", disconnectSession);

function setConnStatus(status, label) {
  const dot = document.getElementById("conn-dot");
  dot.className = `dot dot-${status}`;
  document.getElementById("active-host-label").textContent = label;
}

// --- TOFU & MITM Modal ---

function openTofuModal(hostId, msg, isMismatch) {
  state.pendingTofu = { hostId, msg };
  const titleEl = document.getElementById("tofu-title");
  const msgEl = document.getElementById("tofu-message");
  const expWrap = document.getElementById("tofu-expected-wrap");
  const trustBtn = document.getElementById("btn-tofu-trust");

  document.getElementById("tofu-key-type").textContent = msg.key_type || "-";
  document.getElementById("tofu-actual-fp").textContent = msg.fingerprint || "-";

  if (isMismatch) {
    titleEl.textContent = "🚨 警告：主机公钥指纹变更 (疑似 MITM)";
    msgEl.textContent = msg.data + " 若确认是服务器重装系统导致，可点击下方按钮覆盖信任新指纹。";
    expWrap.classList.remove("hidden");
    document.getElementById("tofu-expected-fp").textContent = msg.expected_fp || "-";
    trustBtn.className = "btn btn-danger";
    trustBtn.textContent = "我已核实风险，覆盖信任新指纹";
  } else {
    titleEl.textContent = "首次连接主机公钥验证 (TOFU)";
    msgEl.textContent = msg.data;
    expWrap.classList.add("hidden");
    trustBtn.className = "btn btn-primary";
    trustBtn.textContent = "确认指纹无误并信任";
  }
  openModal("tofu-modal");
}

document.getElementById("btn-tofu-trust").addEventListener("click", async () => {
  if (!state.pendingTofu) return;
  const { hostId, msg } = state.pendingTofu;
  await api(`/api/hosts/${hostId}/trust-key`, {
    method: "POST",
    body: JSON.stringify({
      key_type: msg.key_type,
      fingerprint: msg.fingerprint,
      public_key_base64: msg.public_key_base64,
    }),
  });
  closeModal("tofu-modal");
  await loadHosts();
  connectHost(hostId);
});

// --- Audit Logs & Recordings ---

document.getElementById("btn-audit").addEventListener("click", async () => {
  const logs = await api("/api/audit-logs");
  document.getElementById("audit-tbody").innerHTML = logs.map((l) => `
    <tr>
      <td>${new Date(l.created_at).toLocaleString()}</td>
      <td class="sev-${l.severity}">${escapeHTML(l.severity)}</td>
      <td><code>${escapeHTML(l.event_type)}</code></td>
      <td>${escapeHTML(l.host_name)}</td>
      <td>${escapeHTML(l.client_ip)}</td>
      <td>${escapeHTML(l.detail)}</td>
    </tr>
  `).join("") || `<tr><td colspan="6" class="muted">暂无审计日志</td></tr>`;
  openModal("audit-modal");
});

document.getElementById("btn-recordings").addEventListener("click", async () => {
  const recs = await api("/api/recordings");
  document.getElementById("recordings-tbody").innerHTML = recs.map((r) => `
    <tr>
      <td><code>${escapeHTML(r.id)}</code></td>
      <td>${escapeHTML(r.host_name)}</td>
      <td>${new Date(r.started_at).toLocaleString()}</td>
      <td>${(r.size_bytes / 1024).toFixed(1)} KB</td>
      <td>
        <button class="btn btn-primary btn-sm" onclick="playRecording('${escapeHTML(r.id)}', '${escapeHTML(r.host_name)}')">回放</button>
      </td>
    </tr>
  `).join("") || `<tr><td colspan="5" class="muted">暂无会话录像</td></tr>`;
  openModal("recordings-modal");
});

// --- Built-in asciinema v2 (.cast) Replay Player ---

function cleanupReplayState() {
  if (state.replayTimer) {
    clearTimeout(state.replayTimer);
    state.replayTimer = null;
  }
  const toggleBtn = document.getElementById("btn-replay-toggle");
  if (toggleBtn) {
    toggleBtn.onclick = null;
  }
  if (state.replayTerm) {
    try {
      state.replayTerm.dispose();
    } catch (_) {}
    state.replayTerm = null;
  }
  const container = document.getElementById("replay-terminal");
  if (container) {
    container.innerHTML = "";
  }
  state.replayPaused = false;
}

function parseCastPayload(text) {
  const lines = text.trim().split("\n");
  if (lines.length < 1 || !lines[0]) return null;
  const header = JSON.parse(lines[0]);
  const events = [];
  for (let i = 1; i < lines.length; i++) {
    if (lines[i]) events.push(JSON.parse(lines[i]));
  }
  return { header, events };
}

window.playRecording = async function (id, hostName) {
  cleanupReplayState();
  closeModal("recordings-modal");
  openModal("replay-modal");
  document.getElementById("replay-title").textContent = `会话回放 · ${hostName} (${id})`;
  document.getElementById("replay-progress").textContent = "0.0s";

  const resp = await rawRequest(`/api/recordings/${id}`);
  if (resp.status < 200 || resp.status >= 300) {
    alert("读取录像失败");
    return;
  }
  const parsed = parseCastPayload(resp.text);
  resp.text = "";
  if (!parsed) return;
  const { header, events } = parsed;

  const container = document.getElementById("replay-terminal");
  container.innerHTML = "";
  const rterm = new Terminal({
    cols: header.width || 100,
    rows: header.height || 26,
    scrollback: 500,
    fontSize: 13,
    disableStdin: true,
    theme: { background: "#0a0d12", foreground: "#e6edf3" },
  });
  rterm.open(container);
  state.replayTerm = rterm;
  state.replayPaused = false;
  document.getElementById("btn-replay-toggle").textContent = "暂停";

  let idx = 0;
  let elapsed = 0;

  function scheduleNext() {
    if (state.replayPaused || state.replayTerm !== rterm || idx >= events.length) return;
    const [t, kind, data] = events[idx];
    const speed = parseFloat(document.getElementById("replay-speed").value) || 1;
    const delay = Math.min(Math.max((t - elapsed) * 1000 / speed, 0), 1500);
    state.replayTimer = setTimeout(() => {
      if (state.replayTerm !== rterm) return;
      elapsed = t;
      if (kind === "o") rterm.write(data);
      document.getElementById("replay-progress").textContent = `${t.toFixed(1)}s`;
      idx++;
      scheduleNext();
    }, delay);
  }

  document.getElementById("btn-replay-toggle").onclick = () => {
    state.replayPaused = !state.replayPaused;
    document.getElementById("btn-replay-toggle").textContent = state.replayPaused ? "继续" : "暂停";
    if (!state.replayPaused) scheduleNext();
    else {
      clearTimeout(state.replayTimer);
      state.replayTimer = null;
    }
  };

  scheduleNext();
};

window.stopReplay = function () {
  cleanupReplayState();
  closeModal("replay-modal");
};

// --- Helpers ---

function openModal(id) { document.getElementById(id).classList.remove("hidden"); }
window.closeModal = function (id) {
  document.getElementById(id).classList.add("hidden");
  if (id === "audit-modal") {
    const el = document.getElementById("audit-tbody");
    if (el) el.innerHTML = "";
  } else if (id === "recordings-modal") {
    const el = document.getElementById("recordings-tbody");
    if (el) el.innerHTML = "";
  } else if (id === "replay-modal" && state.replayTerm) {
    cleanupReplayState();
  }
};

function escapeHTML(str) {
  return String(str || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

initApp();
