const STORAGE_KEY = "inlinechat.staff.token";
const LEGACY_TOKEN_KEYS = ["inlinechat.agent.token", "inlinechat.admin.token"];
const LOGIN_PAGE_URL = "/app/staff-login/";
const ADMIN_HOME_URL = "/app/admin/";
const AGENT_HOME_URL = "/app/agent/";
const ADMIN_ALLOWED_ROLES = new Set(["admin", "super_admin"]);

const state = {
  token: "",
  me: null,
  sites: [],
  agents: [],
};

const els = {
  userBox: document.getElementById("userBox"),
  statusLine: document.getElementById("statusLine"),
  logoutBtn: document.getElementById("logoutBtn"),

  siteCount: document.getElementById("siteCount"),
  agentCount: document.getElementById("agentCount"),

  refreshSitesBtn: document.getElementById("refreshSitesBtn"),
  createSiteForm: document.getElementById("createSiteForm"),
  siteNameInput: document.getElementById("siteNameInput"),
  siteDomainInput: document.getElementById("siteDomainInput"),
  siteList: document.getElementById("siteList"),

  refreshAgentsBtn: document.getElementById("refreshAgentsBtn"),
  createAgentForm: document.getElementById("createAgentForm"),
  agentEmailInput: document.getElementById("agentEmailInput"),
  agentPasswordInput: document.getElementById("agentPasswordInput"),
  agentDisplayNameInput: document.getElementById("agentDisplayNameInput"),
  agentList: document.getElementById("agentList"),
};

init();

async function init() {
  bindEvents();
  renderSites([]);
  renderAgents([]);
  renderStats();

  const savedToken = readStaffToken();
  if (!savedToken) {
    redirectToLogin();
    return;
  }

  state.token = savedToken;
  try {
    await fetchMe();
    if (!ADMIN_ALLOWED_ROLES.has(String(state.me?.role || ""))) {
      if (String(state.me?.role || "") === "agent") {
        window.location.replace(AGENT_HOME_URL);
        return;
      }
      clearAuth();
      redirectToLogin();
      return;
    }
    await refreshAll();
    applyAuthUI(true);
    document.body.classList.remove("auth-guard");
    setStatus("登录态校验通过");
  } catch (error) {
    clearAuth();
    redirectToLogin("登录态已失效，请重新登录");
  }
}

function bindEvents() {
  els.logoutBtn?.addEventListener("click", () => {
    clearAuth();
    redirectToLogin("已退出登录");
  });

  els.refreshSitesBtn?.addEventListener("click", async () => {
    await refreshSites();
  });

  els.refreshAgentsBtn?.addEventListener("click", async () => {
    await refreshAgents();
  });

  els.createSiteForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await createSite();
  });

  els.createAgentForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await createAgent();
  });

  els.siteList?.addEventListener("click", async (event) => {
    const btn = event.target.closest("button.copy-snippet-btn");
    if (!btn) {
      return;
    }

    const siteID = (btn.dataset.siteId || "").trim();
    if (!siteID) {
      return;
    }

    try {
      await copyText(buildEmbedSnippet(siteID));
      setStatus(`站点 ${siteID} 的嵌入脚本已复制`);
    } catch (error) {
      setStatus(error.message || "复制嵌入脚本失败", true);
    }
  });
}

function applyAuthUI(loggedIn) {
  if (!loggedIn) {
    els.userBox.textContent = "未登录";
    state.sites = [];
    state.agents = [];
    renderSites([]);
    renderAgents([]);
    renderStats();
    document.body.classList.add("auth-guard");
    return;
  }

  if (state.me) {
    els.userBox.textContent = `${state.me.email} (${state.me.role})`;
  }
  document.body.classList.remove("auth-guard");
}

async function fetchMe() {
  const data = await apiRequest("/api/auth/v1/auth/me", {
    auth: true,
  });
  state.me = data;
}

function clearAuth() {
  state.token = "";
  state.me = null;
  localStorage.removeItem(STORAGE_KEY);
  for (const key of LEGACY_TOKEN_KEYS) {
    localStorage.removeItem(key);
  }
}

function readStaffToken() {
  const sharedToken = localStorage.getItem(STORAGE_KEY);
  if (sharedToken) {
    return sharedToken;
  }

  for (const key of LEGACY_TOKEN_KEYS) {
    const legacyToken = localStorage.getItem(key);
    if (!legacyToken) {
      continue;
    }
    localStorage.setItem(STORAGE_KEY, legacyToken);
    localStorage.removeItem(key);
    return legacyToken;
  }

  return "";
}

function redirectToLogin(message = "") {
  const next = encodeURIComponent(ADMIN_HOME_URL);
  const target = `${LOGIN_PAGE_URL}?next=${next}`;
  if (message) {
    setStatus(message, true);
  }
  window.location.replace(target);
}

async function refreshAll() {
  await Promise.all([refreshSites(), refreshAgents()]);
}

async function refreshSites() {
  if (!state.token) {
    return;
  }
  const data = await apiRequest("/api/admin/v1/admin/sites?limit=100", { auth: true });
  state.sites = Array.isArray(data.items) ? data.items : [];
  renderSites(state.sites);
  renderStats();
}

async function refreshAgents() {
  if (!state.token) {
    return;
  }
  const data = await apiRequest("/api/admin/v1/admin/agents?limit=100", { auth: true });
  state.agents = Array.isArray(data.items) ? data.items : [];
  renderAgents(state.agents);
  renderStats();
}

async function createSite() {
  const name = els.siteNameInput.value.trim();
  const domain = els.siteDomainInput.value.trim();
  if (!name || !domain) {
    setStatus("请填写站点名称和域名", true);
    return;
  }

  try {
    await apiRequest("/api/admin/v1/admin/sites", {
      method: "POST",
      auth: true,
      body: { name, domain },
    });
    els.siteNameInput.value = "";
    els.siteDomainInput.value = "";
    await refreshSites();
    setStatus("站点创建成功");
  } catch (error) {
    setStatus(error.message || "创建站点失败", true);
  }
}

async function createAgent() {
  const email = els.agentEmailInput.value.trim();
  const password = els.agentPasswordInput.value;
  const displayName = els.agentDisplayNameInput.value.trim();

  if (!email || !password || !displayName) {
    setStatus("请完整填写坐席信息", true);
    return;
  }

  try {
    await apiRequest("/api/admin/v1/admin/agents", {
      method: "POST",
      auth: true,
      body: {
        email,
        password,
        display_name: displayName,
        role: "agent",
      },
    });
    els.agentPasswordInput.value = "";
    els.agentDisplayNameInput.value = "";
    await refreshAgents();
    setStatus("坐席创建成功");
  } catch (error) {
    setStatus(error.message || "创建坐席失败", true);
  }
}

function renderStats() {
  els.siteCount.textContent = String(state.sites.length || 0);
  els.agentCount.textContent = String(state.agents.length || 0);
}

function renderSites(items) {
  if (!Array.isArray(items) || items.length === 0) {
    els.siteList.innerHTML = '<div class="empty">暂无站点</div>';
    return;
  }

  els.siteList.innerHTML = "";
  for (const site of items) {
    const siteID = String(site.site_id || "");
    const snippet = buildEmbedSnippet(siteID);
    const node = document.createElement("article");
    node.className = "item";
    node.innerHTML = `
      <strong>${escapeHTML(site.name || "-")}</strong>
      <div class="meta">site_id=${escapeHTML(siteID || "-")} · domain=${escapeHTML(site.domain || "-")}</div>
      <div class="meta">widget_key=${escapeHTML(site.widget_key || "-")} · status=${escapeHTML(site.status || "-")}</div>
      <div class="snippet-box">
        <div class="snippet-title">嵌入脚本</div>
        <pre class="snippet-code">${escapeHTML(snippet)}</pre>
        <button type="button" class="ghost copy-snippet-btn" data-site-id="${escapeHTML(siteID)}">复制嵌入脚本</button>
      </div>
    `;
    els.siteList.appendChild(node);
  }
}

function renderAgents(items) {
  if (!Array.isArray(items) || items.length === 0) {
    els.agentList.innerHTML = '<div class="empty">暂无坐席</div>';
    return;
  }

  els.agentList.innerHTML = "";
  for (const agent of items) {
    const node = document.createElement("article");
    node.className = "item";
    node.innerHTML = `
      <strong>${escapeHTML(agent.display_name || "-")}</strong>
      <div class="meta">id=${escapeHTML(String(agent.id || "-"))} · role=${escapeHTML(agent.role || "-")}</div>
      <div class="meta">email=${escapeHTML(agent.email || "-")} · status=${escapeHTML(agent.status || "-")}</div>
    `;
    els.agentList.appendChild(node);
  }
}

function setStatus(text, isError = false) {
  els.statusLine.textContent = text;
  els.statusLine.classList.toggle("error", Boolean(isError));
}

async function apiRequest(path, options = {}) {
  const headers = {
    "Content-Type": "application/json",
    ...(options.headers || {}),
  };

  if (options.auth) {
    if (!state.token) {
      throw new Error("未登录");
    }
    headers.Authorization = `Bearer ${state.token}`;
  }

  const response = await fetch(path, {
    method: options.method || "GET",
    headers,
    body: options.body ? JSON.stringify(options.body) : undefined,
  });

  const text = await response.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = {};
    }
  }

  if (!response.ok) {
    if (response.status === 401) {
      clearAuth();
      redirectToLogin("登录态已过期，请重新登录");
    }
    const message = data.error || data?.error?.message || `请求失败 (${response.status})`;
    throw new Error(message);
  }

  return data;
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function buildEmbedSnippet(siteID) {
  const normalizedSiteID = String(siteID || "").trim();
  const src = `${window.location.origin}/sdk/inlinechat-widget.js`;
  return `<script src="${src}" data-site-id="${normalizedSiteID}" data-title="在线客服" defer></script>`;
}

async function copyText(text) {
  if (navigator.clipboard && navigator.clipboard.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const input = document.createElement("textarea");
  input.value = text;
  input.setAttribute("readonly", "readonly");
  input.style.position = "fixed";
  input.style.left = "-9999px";
  document.body.appendChild(input);
  input.select();
  const ok = document.execCommand("copy");
  document.body.removeChild(input);
  if (!ok) {
    throw new Error("当前浏览器不支持自动复制，请手动复制");
  }
}
