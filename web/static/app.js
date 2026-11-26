const state = {
  isDesktop: false,
  wsBase: "",
  sessionToken: "",
  platform: "",
  dataDir: "",
  initialized: false,
  hosts: [],
  activeHostId: null,
  sessions: [],
  activeSessionId: null,
  sessionSeq: 0,
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
        if (!payload) return;
        const sess = state.sessions.find((s) => s.desktopSessionId === payload.session_id);
        if (!sess) return;
        if (payload.event === "msg" && payload.msg) {
          handleSSHMessage(sess, payload.msg);
        } else if (payload.event === "closed") {
          sess.desktopSessionId = null;
          setSessionStatus(sess, "idle", `会话已断开 · ${sess.host.name}`);
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
  closeAllSessions();
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
    const activeSess = getActiveSession();
    const activeCls = activeSess && activeSess.hostId === h.id ? "active" : "";
    const openCount = state.sessions.filter((s) => s.hostId === h.id).length;
    const guardTag = h.guard_enabled ? "🛡️" : "";
    const fpTag = h.known_host_fp ? "已绑定指纹" : "待首次验证";
    return `
      <div class="host-item ${activeCls}">
        <div class="host-top">
          <span class="host-name">${escapeHTML(h.name)} ${guardTag}</span>
          <span class="badge ${isKey ? "badge-key" : "badge-pw"}">${isKey ? "公私钥" : "密码"}</span>
        </div>
        <div class="host-addr">${escapeHTML(h.username)}@${escapeHTML(h.address)}:${h.port}</div>
        <div class="muted small" style="margin-bottom:8px;">${fpTag}${openCount ? ` · 已开 ${openCount} 个会话` : ""}</div>
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
  for (const sess of state.sessions.filter((s) => s.hostId === id)) {
    closeSession(sess.id);
  }
  await api(`/api/hosts/${id}`, { method: "DELETE" });
  await loadHosts();
};

// --- SSH Session Tabs (IPC in Desktop / WebSocket in Web) ---

function getSession(id) {
  return state.sessions.find((s) => s.id === id) || null;
}

function getActiveSession() {
  return getSession(state.activeSessionId);
}

function newSessionId() {
  state.sessionSeq += 1;
  return `s${state.sessionSeq}`;
}

function setSessionStatus(sess, status, text) {
  if (!sess) return;
  sess.status = status;
  sess.statusText = text;
  if (sess.id === state.activeSessionId) {
    setConnStatus(status, text);
  }
  renderTabs();
}

function renderTabs() {
  const bar = document.getElementById("session-tabs");
  if (!bar) return;
  if (!state.sessions.length) {
    bar.innerHTML = "";
    return;
  }
  const counts = new Map();
  bar.innerHTML = state.sessions.map((s) => {
    const host = s.host || {};
    const seen = (counts.get(s.hostId) || 0) + 1;
    counts.set(s.hostId, seen);
    const label = `${host.name || "SSH"}${seen > 1 ? ` #${seen}` : ""}`;
    const cur = s.id === state.activeSessionId ? " active" : "";
    const title = `${host.username || ""}@${host.address || ""}:${host.port || 22}`;
    return `<div class="session-tab${cur}" data-sess="${s.id}" title="${escapeHTML(title)}">
      <span class="dot dot-${s.status || "idle"}"></span>
      <span class="tab-label">${escapeHTML(label)}</span>
      <span class="tab-close" data-close="${s.id}" title="关闭会话">×</span>
    </div>`;
  }).join("");
}

function updateSessionChrome() {
  const has = state.sessions.length > 0;
  const emptyEl = document.getElementById("terminal-empty");
  if (emptyEl) emptyEl.classList.toggle("hidden", has);
  const tabsEl = document.getElementById("session-tabs");
  if (tabsEl) tabsEl.classList.toggle("hidden", !has);
  const termEl = document.getElementById("terminal-container");
  if (termEl) termEl.classList.toggle("hidden", !has);
  document.getElementById("btn-disconnect").classList.toggle("hidden", !getActiveSession());
  const sess = getActiveSession();
  if (sess) {
    setConnStatus(sess.status || "idle", sess.statusText || "");
  } else {
    setConnStatus("idle", "未连接 · 请从左侧选择主机");
  }
}

function fitSession(sess) {
  if (!sess || !sess.fitAddon) return;
  if (sess.paneEl && sess.paneEl.classList.contains("hidden")) return;
  try {
    sess.fitAddon.fit();
  } catch (_) {}
  scheduleGutter(sess);
}

function activateSession(id) {
  const sess = getSession(id);
  if (!sess) return;
  state.activeSessionId = id;
  for (const s of state.sessions) {
    if (s.paneEl) s.paneEl.classList.toggle("hidden", s.id !== id);
  }
  renderTabs();
  updateSessionChrome();
  renderHosts();
  requestAnimationFrame(() => {
    const cur = getActiveSession();
    if (!cur || cur.id !== id) return;
    fitSession(cur);
    if (cur.term) cur.term.focus();
  });
}

function closeSession(id) {
  const idx = state.sessions.findIndex((s) => s.id === id);
  if (idx < 0) return;
  const sess = state.sessions[idx];
  sess.closing = true;
  if (sess.gutterRaf) {
    cancelAnimationFrame(sess.gutterRaf);
    sess.gutterRaf = 0;
  }
  if (sess.ws) {
    try { sess.ws.close(); } catch (_) {}
    sess.ws = null;
  }
  if (sess.desktopSessionId) {
    const oldId = sess.desktopSessionId;
    sess.desktopSessionId = null;
    if (window.go?.main?.DesktopApp?.CloseSSHSession) {
      window.go.main.DesktopApp.CloseSSHSession(oldId).catch(() => {});
    }
  }
  if (sess.term) {
    try { sess.term.dispose(); } catch (_) {}
    sess.term = null;
  }
  if (sess.paneEl && sess.paneEl.parentElement) {
    sess.paneEl.parentElement.removeChild(sess.paneEl);
  }
  state.sessions.splice(idx, 1);
  if (state.activeSessionId === id) {
    const next = state.sessions[Math.min(idx, state.sessions.length - 1)];
    state.activeSessionId = next ? next.id : null;
    if (next) {
      activateSession(next.id);
      return;
    }
  }
  renderTabs();
  updateSessionChrome();
  renderHosts();
}

function closeAllSessions() {
  for (const sess of state.sessions.slice()) closeSession(sess.id);
  state.activeSessionId = null;
  renderTabs();
  updateSessionChrome();
}

function sendSessionPayload(sess, obj) {
  if (!sess) return;
  const raw = JSON.stringify(obj);
  if (state.isDesktop) {
    if (sess.desktopSessionId && window.go?.main?.DesktopApp?.SendSSHMessage) {
      window.go.main.DesktopApp.SendSSHMessage(sess.desktopSessionId, raw).catch(() => {});
    }
  } else if (sess.ws && sess.ws.readyState === WebSocket.OPEN) {
    sess.ws.send(raw);
  }
}

window.addEventListener("resize", () => {
  fitSession(getActiveSession());
});

// --- 终端左侧时间 / 行号栏 ---

function formatClock(date) {
  const pad = (n) => String(n).padStart(2, "0");
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`;
}

function scheduleGutter(sess) {
  if (!sess || sess.gutterRaf) return;
  sess.gutterRaf = requestAnimationFrame(() => {
    sess.gutterRaf = 0;
    renderTerminalGutter(sess);
  });
}

function renderTerminalGutter(sess) {
  if (!sess) return;
  const gutter = sess.gutterEl;
  const term = sess.term;
  if (!gutter || !term || !term.element) return;
  if (sess.paneEl && sess.paneEl.classList.contains("hidden")) return;
  const screenEl = term.element.querySelector(".xterm-screen");
  const hostEl = gutter.parentElement;
  if (!screenEl || !hostEl) return;
  const screenRect = screenEl.getBoundingClientRect();
  if (screenRect.height <= 0) return;
  const hostRect = hostEl.getBoundingClientRect();
  const cellH = screenRect.height / term.rows;
  gutter.style.paddingTop = `${Math.max(0, screenRect.top - hostRect.top)}px`;
  gutter.style.setProperty("--gutter-cell-h", `${cellH}px`);

  const buf = term.buffer.active;
  const topAbs = buf.viewportY;
  const curAbs = buf.baseY + buf.cursorY;
  const nums = new Map();
  let countNow = 0;
  for (let abs = 0; abs <= curAbs; abs++) {
    const line = buf.getLine(abs);
    if (line && !line.isWrapped) {
      nums.set(abs, countNow);
      countNow += 1;
    }
  }

  const stamps = sess.gutterStamps;
  if (countNow > stamps.length) {
    const now = formatClock(new Date());
    while (stamps.length < countNow) stamps.push(now);
  } else if (countNow < stamps.length) {
    const removed = stamps.length - countNow;
    stamps.splice(0, removed);
    sess.gutterDropped += removed;
  }

  const parts = [];
  for (let r = 0; r < term.rows; r++) {
    const abs = topAbs + r;
    const line = abs <= curAbs ? buf.getLine(abs) : null;
    if (!line || line.isWrapped || !nums.has(abs)) {
      parts.push('<div class="gutter-row"></div>');
      continue;
    }
    const idx = nums.get(abs);
    const time = stamps[idx] || "";
    const lineNo = sess.gutterDropped + idx + 1;
    const curClass = abs === curAbs ? " gutter-row-cur" : "";
    parts.push(
      `<div class="gutter-row${curClass}"><span class="gutter-time">${time}</span>` +
        `<span class="gutter-no">${lineNo}</span></div>`
    );
  }
  gutter.innerHTML = parts.join("");
}

function createSessionTerminal(sess, paneEl) {
  const body = document.createElement("div");
  body.className = "terminal-body";
  const gutter = document.createElement("div");
  gutter.className = "term-gutter";
  const xtermHost = document.createElement("div");
  xtermHost.className = "terminal-xterm";
  body.appendChild(gutter);
  body.appendChild(xtermHost);
  paneEl.appendChild(body);

  sess.gutterEl = gutter;
  sess.gutterStamps = [];
  sess.gutterDropped = 0;

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
  term.open(xtermHost);
  sess.term = term;
  sess.fitAddon = fitAddon;

  term.onData((data) => {
    sendSessionPayload(sess, { type: "input", data });
  });
  term.onScroll(() => scheduleGutter(sess));
  term.onRender(() => scheduleGutter(sess));
  term.onResize((size) => {
    scheduleGutter(sess);
    sendSessionPayload(sess, { type: "resize", cols: size.cols, rows: size.rows });
  });
  fitSession(sess);
}

function handleSSHMessage(sess, msg) {
  if (!sess || !sess.term) return;
  const host = sess.host || {};
  switch (msg.type) {
    case "output":
      sess.term.write(msg.data);
      break;
    case "guard_blocked":
      sess.term.write(msg.data);
      break;
    case "status":
      if (msg.data === "CONNECTED") {
        setSessionStatus(sess, "connected", `已连接 · ${host.name || ""} (${host.username || ""}@${host.address || ""}:${host.port || 22})`);
        if (sess.id === state.activeSessionId) sess.term.focus();
      } else {
        sess.term.writeln(`\x1b[36m[SSH-Guard]\x1b[0m ${msg.data}`);
      }
      break;
    case "host_key_verify":
      openTofuModal(sess, msg, false);
      setSessionStatus(sess, "error", "等待首次指纹信任 (TOFU)");
      break;
    case "host_key_mismatch":
      openTofuModal(sess, msg, true);
      setSessionStatus(sess, "error", "安全阻断：检测到主机公钥变更 (疑似 MITM)");
      break;
    case "error":
      sess.term.writeln(`\r\n\x1b[1;31m[错误]\x1b[0m ${msg.data}`);
      setSessionStatus(sess, "error", `连接失败 · ${host.name || ""}`);
      break;
  }
}

window.connectHost = async function (hostId) {
  const host = state.hosts.find((h) => h.id === hostId);
  if (!host) return;

  const sess = {
    id: newSessionId(),
    hostId,
    host: Object.assign({}, host),
    status: "connecting",
    statusText: "",
    ws: null,
    desktopSessionId: null,
    closing: false,
    term: null,
    fitAddon: null,
    paneEl: null,
    gutterEl: null,
    gutterStamps: [],
    gutterDropped: 0,
    gutterRaf: 0,
  };
  state.sessions.push(sess);

  const paneEl = document.createElement("div");
  paneEl.className = "terminal-pane";
  document.getElementById("terminal-container").appendChild(paneEl);
  sess.paneEl = paneEl;

  updateSessionChrome();
  createSessionTerminal(sess, paneEl);
  activateSession(sess.id);
  setSessionStatus(sess, "connecting", `正在连接 ${host.name} (${host.username}@${host.address}:${host.port})`);

  if (state.isDesktop) {
    const sessID = `sess-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
    sess.desktopSessionId = sessID;
    try {
      await window.go.main.DesktopApp.OpenSSHSession(
        sessID,
        hostId,
        sess.term.cols,
        sess.term.rows,
        state.sessionToken
      );
    } catch (err) {
      sess.desktopSessionId = null;
      sess.term.writeln(`\r\n\x1b[1;31m[错误]\x1b[0m ${err}`);
      setSessionStatus(sess, "error", `连接失败 · ${host.name}`);
    }
    return;
  }

  const params = new URLSearchParams({
    cols: String(sess.term.cols),
    rows: String(sess.term.rows),
  });
  const wsUrl = `${state.wsBase}/ws/ssh/${hostId}?${params.toString()}`;
  const ws = new WebSocket(wsUrl);
  sess.ws = ws;

  ws.onmessage = (ev) => {
    let msg = null;
    try { msg = JSON.parse(ev.data); } catch (_) { return; }
    handleSSHMessage(sess, msg);
  };

  ws.onclose = () => {
    if (sess.closing) return;
    if (sess.ws === ws) sess.ws = null;
    if (sess.term) sess.term.writeln(`\r\n\x1b[36m[SSH-Guard]\x1b[0m 会话已断开`);
    setSessionStatus(sess, "idle", `会话已断开 · ${host.name}`);
  };
};

document.getElementById("session-tabs").addEventListener("click", (e) => {
  const closeEl = e.target.closest("[data-close]");
  if (closeEl) {
    e.stopPropagation();
    closeSession(closeEl.getAttribute("data-close"));
    return;
  }
  const tabEl = e.target.closest("[data-sess]");
  if (tabEl) activateSession(tabEl.getAttribute("data-sess"));
});

document.getElementById("btn-disconnect").addEventListener("click", () => {
  const sess = getActiveSession();
  if (sess) closeSession(sess.id);
});

function setConnStatus(status, label) {
  const dot = document.getElementById("conn-dot");
  dot.className = `dot dot-${status}`;
  document.getElementById("active-host-label").textContent = label;
}

// --- TOFU & MITM Modal ---

function openTofuModal(sess, msg, isMismatch) {
  state.pendingTofu = { sessionId: sess.id, hostId: sess.hostId, msg };
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
  const { sessionId, hostId, msg } = state.pendingTofu;
  state.pendingTofu = null;
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
  closeSession(sessionId);
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
