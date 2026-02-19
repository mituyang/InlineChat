const STORAGE_KEY = "inlinechat.staff.token";
const LEGACY_TOKEN_KEYS = ["inlinechat.agent.token", "inlinechat.admin.token"];
const LOGIN_PAGE_URL = "/app/staff-login/";
const ADMIN_HOME_URL = "/app/admin/";
const AGENT_HOME_URL = "/app/agent/";
const ADMIN_ALLOWED_ROLES = new Set(["admin", "super_admin"]);
const THEME_STORAGE_KEY = "inlinechat.ui.theme";
const WEAK_PASSWORD_BLACKLIST = new Set([
  "123456",
  "12345678",
  "password",
  "password123",
  "qwerty123",
  "admin123456",
  "letmein123",
  "agent12345!",
  "superadmin123!",
  "changeme123!",
  "changeme",
]);

const state = {
  token: "",
  me: null,
  theme: "light",
  sites: [],
  agents: [],
  siteSearch: "",
  agentSearch: "",
  agentStatusFilter: "all",
  operationFeed: [],
};

const els = {
  roleTag: document.getElementById("roleTag"),
  userBox: document.getElementById("userBox"),
  themeToggleBtn: document.getElementById("themeToggleBtn"),
  statusLine: document.getElementById("statusLine"),
  logoutBtn: document.getElementById("logoutBtn"),

  siteCount: document.getElementById("siteCount"),
  agentCount: document.getElementById("agentCount"),
  agentActiveCount: document.getElementById("agentActiveCount"),
  agentInactiveCount: document.getElementById("agentInactiveCount"),
  lastSyncAt: document.getElementById("lastSyncAt"),

  refreshSitesBtn: document.getElementById("refreshSitesBtn"),
  siteSearchInput: document.getElementById("siteSearchInput"),
  createSiteForm: document.getElementById("createSiteForm"),
  siteIdInput: document.getElementById("siteIdInput"),
  generateSiteIdBtn: document.getElementById("generateSiteIdBtn"),
  siteNameInput: document.getElementById("siteNameInput"),
  siteDomainInput: document.getElementById("siteDomainInput"),
  siteList: document.getElementById("siteList"),

  refreshAgentsBtn: document.getElementById("refreshAgentsBtn"),
  agentSearchInput: document.getElementById("agentSearchInput"),
  agentStatusFilter: document.getElementById("agentStatusFilter"),
  createAgentForm: document.getElementById("createAgentForm"),
  agentEmailInput: document.getElementById("agentEmailInput"),
  agentPasswordInput: document.getElementById("agentPasswordInput"),
  agentDisplayNameInput: document.getElementById("agentDisplayNameInput"),
  agentList: document.getElementById("agentList"),

  clearFeedBtn: document.getElementById("clearFeedBtn"),
  operationFeed: document.getElementById("operationFeed"),
};

init();

async function init() {
  initTheme();
  bindEvents();
  renderSites([]);
  renderAgents([]);
  renderStats();
  renderOperationFeed();

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
  els.themeToggleBtn?.addEventListener("click", () => {
    toggleTheme();
  });

  els.logoutBtn?.addEventListener("click", () => {
    clearAuth();
    redirectToLogin("已退出登录");
  });

  els.refreshSitesBtn?.addEventListener("click", async () => {
    await refreshSites();
    appendFeed("手动刷新站点列表");
  });

  els.refreshAgentsBtn?.addEventListener("click", async () => {
    await refreshAgents();
    appendFeed("手动刷新坐席列表");
  });

  els.siteSearchInput?.addEventListener("input", () => {
    state.siteSearch = (els.siteSearchInput.value || "").trim().toLowerCase();
    renderSites(state.sites);
  });

  els.agentSearchInput?.addEventListener("input", () => {
    state.agentSearch = (els.agentSearchInput.value || "").trim().toLowerCase();
    renderAgents(state.agents);
  });

  els.agentStatusFilter?.addEventListener("change", () => {
    state.agentStatusFilter = String(els.agentStatusFilter.value || "all");
    renderAgents(state.agents);
  });

  els.createSiteForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await createSite();
  });

  els.generateSiteIdBtn?.addEventListener("click", () => {
    const siteID = buildSiteIDCandidate();
    els.siteIdInput.value = siteID;
    appendFeed(`生成站点ID：${siteID}`);
    setStatus("已生成站点ID，可继续手动调整");
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
      appendFeed(`复制嵌入脚本：${siteID}`);
      setStatus(`站点 ${siteID} 的嵌入脚本已复制`);
    } catch (error) {
      setStatus(error.message || "复制嵌入脚本失败", true);
    }
  });

  els.clearFeedBtn?.addEventListener("click", () => {
    state.operationFeed = [];
    renderOperationFeed();
    setStatus("操作动态已清空");
  });
}

function applyAuthUI(loggedIn) {
  if (!loggedIn) {
    els.userBox.textContent = "未登录";
    if (els.roleTag) {
      els.roleTag.textContent = "管理角色";
    }
    state.sites = [];
    state.agents = [];
    state.operationFeed = [];
    renderSites([]);
    renderAgents([]);
    renderStats();
    renderOperationFeed();
    document.body.classList.add("auth-guard");
    return;
  }

  if (state.me) {
    els.userBox.textContent = `${state.me.email} (${state.me.role})`;
    if (els.roleTag) {
      els.roleTag.textContent = state.me.role === "super_admin" ? "超级管理员" : "管理员";
    }
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

function initTheme() {
  const theme = resolveInitialTheme();
  applyTheme(theme, false);
}

function resolveInitialTheme() {
  const stored = localStorage.getItem(THEME_STORAGE_KEY);
  if (stored === "dark" || stored === "light") {
    return stored;
  }

  const prefersDark =
    typeof window.matchMedia === "function" && window.matchMedia("(prefers-color-scheme: dark)").matches;
  return prefersDark ? "dark" : "light";
}

function toggleTheme() {
  const next = state.theme === "dark" ? "light" : "dark";
  applyTheme(next, true);
  appendFeed(`切换主题：${next === "dark" ? "暗色" : "亮色"}`);
  setStatus(`已切换为${next === "dark" ? "暗色" : "亮色"}模式`);
}

function applyTheme(theme, persist) {
  const normalized = theme === "dark" ? "dark" : "light";
  state.theme = normalized;
  document.documentElement.setAttribute("data-theme", normalized);
  if (persist) {
    localStorage.setItem(THEME_STORAGE_KEY, normalized);
  }
  updateThemeToggleLabel();
}

function updateThemeToggleLabel() {
  if (!els.themeToggleBtn) {
    return;
  }
  els.themeToggleBtn.textContent = state.theme === "dark" ? "亮色模式" : "暗色模式";
}

async function refreshAll() {
  await Promise.all([refreshSites(), refreshAgents()]);
  if (els.lastSyncAt) {
    els.lastSyncAt.textContent = formatTime(Date.now());
  }
  appendFeed("完成一次全量同步");
}

async function refreshSites() {
  if (!state.token) {
    return;
  }
  const data = await apiRequest("/api/admin/v1/admin/sites?limit=100", { auth: true });
  state.sites = Array.isArray(data.items) ? data.items : [];
  renderSites(state.sites);
  renderStats();
  if (els.lastSyncAt) {
    els.lastSyncAt.textContent = formatTime(Date.now());
  }
}

async function refreshAgents() {
  if (!state.token) {
    return;
  }
  const data = await apiRequest("/api/admin/v1/admin/agents?limit=100", { auth: true });
  state.agents = Array.isArray(data.items) ? data.items : [];
  renderAgents(state.agents);
  renderStats();
  if (els.lastSyncAt) {
    els.lastSyncAt.textContent = formatTime(Date.now());
  }
}

async function createSite() {
  const siteID = normalizeSiteID(els.siteIdInput.value);
  const name = els.siteNameInput.value.trim();
  const domain = els.siteDomainInput.value.trim();
  if (!siteID || !name || !domain) {
    setStatus("请填写站点ID、名称和域名", true);
    return;
  }
  if (!isValidSiteID(siteID)) {
    setStatus("站点ID格式无效：仅允许小写字母、数字、下划线、连字符，长度 4-64", true);
    return;
  }

  try {
    await apiRequest("/api/admin/v1/admin/sites", {
      method: "POST",
      auth: true,
      body: { site_id: siteID, name, domain },
    });
    els.siteIdInput.value = "";
    els.siteNameInput.value = "";
    els.siteDomainInput.value = "";
    await refreshSites();
    appendFeed(`创建站点：${name} (${domain}) [${siteID}]`);
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
  const passwordError = validateAgentPassword(password);
  if (passwordError) {
    setStatus(passwordError, true);
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
    appendFeed(`创建坐席：${email}`);
    setStatus("坐席创建成功");
  } catch (error) {
    setStatus(error.message || "创建坐席失败", true);
  }
}

function validateAgentPassword(password) {
  if (!password || password.trim() === "") {
    return "密码不能为空";
  }
  if (password.length < 12 || password.length > 72) {
    return "密码长度需为 12-72 位";
  }
  if (/\s/.test(password)) {
    return "密码不能包含空白字符";
  }
  if (!/[A-Z]/.test(password) || !/[a-z]/.test(password) || !/[0-9]/.test(password) || !/[^A-Za-z0-9]/.test(password)) {
    return "密码需同时包含大写字母、小写字母、数字和特殊字符";
  }
  if (WEAK_PASSWORD_BLACKLIST.has(password.toLowerCase())) {
    return "密码过于常见，请更换为更强的密码";
  }
  return "";
}

function renderStats() {
  const active = state.agents.filter((agent) => String(agent.status || "") === "active").length;
  const inactive = state.agents.length - active;

  els.siteCount.textContent = String(state.sites.length || 0);
  els.agentCount.textContent = String(state.agents.length || 0);
  if (els.agentActiveCount) {
    els.agentActiveCount.textContent = String(active);
  }
  if (els.agentInactiveCount) {
    els.agentInactiveCount.textContent = String(inactive);
  }
}

function filteredSites(items) {
  const list = Array.isArray(items) ? items : [];
  if (!state.siteSearch) {
    return list;
  }
  return list.filter((site) => {
    const name = String(site.name || "").toLowerCase();
    const siteID = String(site.site_id || "").toLowerCase();
    const domain = String(site.domain || "").toLowerCase();
    return name.includes(state.siteSearch) || siteID.includes(state.siteSearch) || domain.includes(state.siteSearch);
  });
}

function filteredAgents(items) {
  let list = Array.isArray(items) ? items : [];
  if (state.agentStatusFilter !== "all") {
    list = list.filter((agent) => String(agent.status || "").toLowerCase() === state.agentStatusFilter);
  }
  if (!state.agentSearch) {
    return list;
  }
  return list.filter((agent) => {
    const id = String(agent.id || "").toLowerCase();
    const email = String(agent.email || "").toLowerCase();
    const displayName = String(agent.display_name || "").toLowerCase();
    return id.includes(state.agentSearch) || email.includes(state.agentSearch) || displayName.includes(state.agentSearch);
  });
}

function renderSites(items) {
  const list = filteredSites(items);
  if (list.length === 0) {
    els.siteList.innerHTML = '<div class="empty">暂无站点</div>';
    return;
  }

  els.siteList.innerHTML = "";
  for (const site of list) {
    const siteID = String(site.site_id || "");
    const snippet = buildEmbedSnippet(siteID);
    const demoURL = "/app/demo/";
    const node = document.createElement("article");
    node.className = "item";
    node.innerHTML = `
      <strong>${escapeHTML(site.name || "-")}</strong>
      <div class="meta">site_id=${escapeHTML(siteID || "-")} · domain=${escapeHTML(site.domain || "-")}</div>
      <div class="meta">widget_key=${escapeHTML(site.widget_key || "-")} · status=${escapeHTML(site.status || "-")}</div>
      <a class="demo-link" href="${demoURL}" target="_blank" rel="noreferrer">打开 Demo 预览</a>
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
  const list = filteredAgents(items);
  if (list.length === 0) {
    els.agentList.innerHTML = '<div class="empty">暂无坐席</div>';
    return;
  }

  els.agentList.innerHTML = "";
  for (const agent of list) {
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

function appendFeed(title) {
  if (!title) {
    return;
  }
  state.operationFeed.unshift({
    title: String(title),
    createdAt: Date.now(),
  });
  state.operationFeed = state.operationFeed.slice(0, 30);
  renderOperationFeed();
}

function renderOperationFeed() {
  if (!els.operationFeed) {
    return;
  }

  if (!Array.isArray(state.operationFeed) || state.operationFeed.length === 0) {
    els.operationFeed.innerHTML = '<li class="empty">暂无操作记录</li>';
    return;
  }

  els.operationFeed.innerHTML = "";
  for (const item of state.operationFeed) {
    const node = document.createElement("li");
    node.innerHTML = `
      <div class="operation-title">${escapeHTML(item.title)}</div>
      <div class="operation-time">${escapeHTML(formatTime(item.createdAt))}</div>
    `;
    els.operationFeed.appendChild(node);
  }
}

function formatTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "--";
  }
  return date.toLocaleString("zh-CN", { hour12: false });
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

function normalizeSiteID(raw) {
  return String(raw || "")
    .trim()
    .toLowerCase();
}

function isValidSiteID(siteID) {
  return /^[a-z0-9][a-z0-9_-]{3,63}$/.test(siteID);
}

function buildSiteIDCandidate() {
  const domainText = String(els.siteDomainInput?.value || "")
    .trim()
    .toLowerCase();
  const domainCore = domainText.split(".")[0] || "site";
  const normalizedCore =
    domainCore
      .replace(/[^a-z0-9_-]+/g, "_")
      .replace(/^[_-]+|[_-]+$/g, "")
      .slice(0, 24) || "site";
  const suffix = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
  const candidate = `site_${normalizedCore}_${suffix}`;
  return candidate.slice(0, 64);
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
