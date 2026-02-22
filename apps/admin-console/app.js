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
  auditActorID: "",
  auditAction: "",
  auditResourceType: "",
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
  agentIdInput: document.getElementById("agentIdInput"),
  agentEmailInput: document.getElementById("agentEmailInput"),
  agentPasswordInput: document.getElementById("agentPasswordInput"),
  agentDisplayNameInput: document.getElementById("agentDisplayNameInput"),
  agentList: document.getElementById("agentList"),

  refreshAuditBtn: document.getElementById("refreshAuditBtn"),
  auditActorInput: document.getElementById("auditActorInput"),
  auditActionFilter: document.getElementById("auditActionFilter"),
  auditResourceFilter: document.getElementById("auditResourceFilter"),
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
    setStatus("站点列表已刷新");
  });

  els.refreshAgentsBtn?.addEventListener("click", async () => {
    await refreshAgents();
    setStatus("坐席列表已刷新");
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
    setStatus("已生成站点ID，可继续手动调整");
  });

  els.createAgentForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await createAgent();
  });

  els.siteList?.addEventListener("click", async (event) => {
    const target = event.target.closest("button");
    if (!target) {
      return;
    }

    const siteID = (target.dataset.siteId || "").trim();
    if (!siteID) {
      return;
    }

    if (target.classList.contains("copy-snippet-btn")) {
      try {
        await copyText(buildEmbedSnippet(siteID));
        setStatus(`站点 ${siteID} 的嵌入脚本已复制`);
      } catch (error) {
        setStatus(error.message || "复制嵌入脚本失败", true);
      }
      return;
    }

    if (target.classList.contains("site-status-btn")) {
      const nextStatus = (target.dataset.nextStatus || "").trim();
      if (!nextStatus) {
        return;
      }
      await updateSiteStatus(siteID, nextStatus);
      return;
    }

    if (target.classList.contains("site-rotate-key-btn")) {
      await rotateSiteWidgetKey(siteID);
    }
  });

  els.agentList?.addEventListener("click", async (event) => {
    const target = event.target.closest("button");
    if (!target) {
      return;
    }
    const rawAgentID = (target.dataset.agentId || "").trim();
    const agentID = Number.parseInt(rawAgentID, 10);
    if (!Number.isInteger(agentID) || agentID <= 0) {
      return;
    }

    if (target.classList.contains("agent-status-btn")) {
      const nextStatus = (target.dataset.nextStatus || "").trim();
      if (!nextStatus) {
        return;
      }
      await updateAgentStatus(agentID, nextStatus);
      return;
    }

    if (target.classList.contains("agent-force-logout-btn")) {
      await forceAgentLogout(agentID);
      return;
    }

    if (target.classList.contains("agent-reset-password-btn")) {
      await resetAgentPassword(agentID);
    }
  });

  els.refreshAuditBtn?.addEventListener("click", async () => {
    await refreshAuditLogs();
    setStatus("审计日志已刷新");
  });

  els.auditActorInput?.addEventListener("change", async () => {
    state.auditActorID = (els.auditActorInput.value || "").trim();
    await refreshAuditLogs();
  });

  els.auditActionFilter?.addEventListener("change", async () => {
    state.auditAction = String(els.auditActionFilter.value || "").trim();
    await refreshAuditLogs();
  });

  els.auditResourceFilter?.addEventListener("change", async () => {
    state.auditResourceType = String(els.auditResourceFilter.value || "").trim();
    await refreshAuditLogs();
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
    state.auditActorID = "";
    state.auditAction = "";
    state.auditResourceType = "";
    state.operationFeed = [];
    renderSites([]);
    renderAgents([]);
    renderStats();
    renderOperationFeed();
    setSensitiveControlsEnabled(false);
    document.body.classList.add("auth-guard");
    return;
  }

  if (state.me) {
    els.userBox.textContent = `${state.me.email} (${state.me.role})`;
    if (els.roleTag) {
      els.roleTag.textContent = state.me.role === "super_admin" ? "超级管理员" : "管理员";
    }
  }
  setSensitiveControlsEnabled(isSuperAdmin());
  document.body.classList.remove("auth-guard");
}

function isSuperAdmin() {
  return String(state.me?.role || "") === "super_admin";
}

function setSensitiveControlsEnabled(enabled) {
  if (!els.createAgentForm) {
    return;
  }
  const controls = els.createAgentForm.querySelectorAll("input, button");
  for (const control of controls) {
    control.disabled = !enabled;
  }
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
  await Promise.all([refreshSites(), refreshAgents(), refreshAuditLogs()]);
  if (els.lastSyncAt) {
    els.lastSyncAt.textContent = formatTime(Date.now());
  }
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

async function refreshAuditLogs() {
  if (!state.token) {
    return;
  }

  const search = new URLSearchParams();
  search.set("limit", "100");
  if (/^\d+$/.test(state.auditActorID)) {
    search.set("actor_agent_id", state.auditActorID);
  }
  if (state.auditAction) {
    search.set("action", state.auditAction);
  }
  if (state.auditResourceType) {
    search.set("resource_type", state.auditResourceType);
  }

  const data = await apiRequest(`/api/admin/v1/admin/audit-logs?${search.toString()}`, { auth: true });
  state.operationFeed = Array.isArray(data.items) ? data.items : [];
  renderOperationFeed();
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
    await Promise.all([refreshSites(), refreshAuditLogs()]);
    setStatus("站点创建成功");
  } catch (error) {
    setStatus(error.message || "创建站点失败", true);
  }
}

async function createAgent() {
  if (!isSuperAdmin()) {
    setStatus("仅超级管理员可创建坐席", true);
    return;
  }

  const agentID = normalizeAgentID(els.agentIdInput.value);
  const email = els.agentEmailInput.value.trim();
  const password = els.agentPasswordInput.value;
  const displayName = els.agentDisplayNameInput.value.trim();

  if (!agentID || !email || !password || !displayName) {
    setStatus("请完整填写坐席信息（含 4 位客服ID）", true);
    return;
  }
  if (!isValidAgentID(agentID)) {
    setStatus("客服ID 必须为 4 位数字，且不能为 0000", true);
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
        agent_id: agentID,
        email,
        password,
        display_name: displayName,
        role: "agent",
      },
    });
    els.agentIdInput.value = "";
    els.agentPasswordInput.value = "";
    els.agentDisplayNameInput.value = "";
    await Promise.all([refreshAgents(), refreshAuditLogs()]);
    setStatus("坐席创建成功");
  } catch (error) {
    setStatus(error.message || "创建坐席失败", true);
  }
}

async function updateSiteStatus(siteID, status) {
  if (!isSuperAdmin()) {
    setStatus("仅超级管理员可更新站点状态", true);
    return;
  }
  try {
    await apiRequest(`/api/admin/v1/admin/sites/${encodeURIComponent(siteID)}/status`, {
      method: "PATCH",
      auth: true,
      body: { status },
    });
    await Promise.all([refreshSites(), refreshAuditLogs()]);
    setStatus(`站点 ${siteID} 已切换为 ${status}`);
  } catch (error) {
    setStatus(error.message || "更新站点状态失败", true);
  }
}

async function rotateSiteWidgetKey(siteID) {
  if (!isSuperAdmin()) {
    setStatus("仅超级管理员可轮换站点密钥", true);
    return;
  }
  const confirmed = window.confirm(`确认轮换站点 ${siteID} 的 widget_key 吗？`);
  if (!confirmed) {
    return;
  }
  try {
    await apiRequest(`/api/admin/v1/admin/sites/${encodeURIComponent(siteID)}/rotate-widget-key`, {
      method: "POST",
      auth: true,
    });
    await Promise.all([refreshSites(), refreshAuditLogs()]);
    setStatus(`站点 ${siteID} 密钥已轮换`);
  } catch (error) {
    setStatus(error.message || "轮换站点密钥失败", true);
  }
}

async function updateAgentStatus(agentID, status) {
  if (!isSuperAdmin()) {
    setStatus("仅超级管理员可更新坐席状态", true);
    return;
  }
  try {
    await apiRequest(`/api/admin/v1/admin/agents/${agentID}/status`, {
      method: "PATCH",
      auth: true,
      body: { status },
    });
    await Promise.all([refreshAgents(), refreshAuditLogs()]);
    setStatus(`坐席 ${formatAgentID(agentID)} 状态已切换为 ${status}`);
    if (Number(state.me?.agent_id || 0) === Number(agentID) && status !== "active") {
      clearAuth();
      redirectToLogin("当前账号已被停用，请重新登录");
    }
  } catch (error) {
    setStatus(error.message || "更新坐席状态失败", true);
  }
}

async function forceAgentLogout(agentID) {
  if (!isSuperAdmin()) {
    setStatus("仅超级管理员可强制下线坐席", true);
    return;
  }
  const confirmed = window.confirm(`确认强制下线坐席 ${formatAgentID(agentID)} 吗？`);
  if (!confirmed) {
    return;
  }
  try {
    await apiRequest(`/api/admin/v1/admin/agents/${agentID}/force-logout`, {
      method: "POST",
      auth: true,
    });
    await Promise.all([refreshAgents(), refreshAuditLogs()]);
    setStatus(`坐席 ${formatAgentID(agentID)} 已被强制下线`);
    if (Number(state.me?.agent_id || 0) === Number(agentID)) {
      clearAuth();
      redirectToLogin("当前账号已被强制下线，请重新登录");
    }
  } catch (error) {
    setStatus(error.message || "强制下线失败", true);
  }
}

async function resetAgentPassword(agentID) {
  if (!isSuperAdmin()) {
    setStatus("仅超级管理员可重置坐席密码", true);
    return;
  }
  const newPassword = window.prompt(`请输入坐席 ${formatAgentID(agentID)} 的新密码（12-72 位）`);
  if (newPassword === null) {
    return;
  }
  const passwordError = validateAgentPassword(newPassword);
  if (passwordError) {
    setStatus(passwordError, true);
    return;
  }
  try {
    await apiRequest(`/api/admin/v1/admin/agents/${agentID}/reset-password`, {
      method: "POST",
      auth: true,
      body: { new_password: newPassword },
    });
    await Promise.all([refreshAgents(), refreshAuditLogs()]);
    setStatus(`坐席 ${formatAgentID(agentID)} 密码已重置`);
    if (Number(state.me?.agent_id || 0) === Number(agentID)) {
      clearAuth();
      redirectToLogin("当前账号密码已被重置，请重新登录");
    }
  } catch (error) {
    setStatus(error.message || "重置密码失败", true);
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
    const formattedID = formatAgentID(agent.id).toLowerCase();
    const email = String(agent.email || "").toLowerCase();
    const displayName = String(agent.display_name || "").toLowerCase();
    return (
      id.includes(state.agentSearch) ||
      formattedID.includes(state.agentSearch) ||
      email.includes(state.agentSearch) ||
      displayName.includes(state.agentSearch)
    );
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
    const status = String(site.status || "").toLowerCase();
    const nextStatus = status === "active" ? "disabled" : "active";
    const statusActionLabel = status === "active" ? "停用站点" : "启用站点";
    const canManage = isSuperAdmin();
    const disabledAttr = canManage ? "" : "disabled";
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
        <div class="item-actions">
          <button type="button" class="ghost copy-snippet-btn" data-site-id="${escapeHTML(siteID)}">复制嵌入脚本</button>
          <button
            type="button"
            class="ghost site-status-btn"
            data-site-id="${escapeHTML(siteID)}"
            data-next-status="${escapeHTML(nextStatus)}"
            ${disabledAttr}
          >${escapeHTML(statusActionLabel)}</button>
          <button type="button" class="ghost site-rotate-key-btn" data-site-id="${escapeHTML(siteID)}" ${disabledAttr}>轮换密钥</button>
        </div>
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
    const formattedID = formatAgentID(agent.id);
    const status = String(agent.status || "").toLowerCase();
    const nextStatus = status === "active" ? "inactive" : "active";
    const statusActionLabel = status === "active" ? "停用坐席" : "启用坐席";
    const canManage = isSuperAdmin();
    const disabledAttr = canManage ? "" : "disabled";
    const node = document.createElement("article");
    node.className = "item";
    node.innerHTML = `
      <strong>${escapeHTML(agent.display_name || "-")}</strong>
      <div class="meta">id=${escapeHTML(formattedID)} · role=${escapeHTML(agent.role || "-")}</div>
      <div class="meta">email=${escapeHTML(agent.email || "-")} · status=${escapeHTML(agent.status || "-")}</div>
      <div class="item-actions">
        <button
          type="button"
          class="ghost agent-status-btn"
          data-agent-id="${escapeHTML(agent.id)}"
          data-next-status="${escapeHTML(nextStatus)}"
          ${disabledAttr}
        >${escapeHTML(statusActionLabel)}</button>
        <button type="button" class="ghost agent-force-logout-btn" data-agent-id="${escapeHTML(agent.id)}" ${disabledAttr}>强制下线</button>
        <button type="button" class="ghost agent-reset-password-btn" data-agent-id="${escapeHTML(agent.id)}" ${disabledAttr}>重置密码</button>
      </div>
    `;
    els.agentList.appendChild(node);
  }
}

function renderOperationFeed() {
  if (!els.operationFeed) {
    return;
  }

  if (!Array.isArray(state.operationFeed) || state.operationFeed.length === 0) {
    els.operationFeed.innerHTML = '<li class="empty">暂无审计日志</li>';
    return;
  }

  els.operationFeed.innerHTML = "";
  for (const item of state.operationFeed) {
    const action = String(item.action || "").trim();
    const resourceType = String(item.resource_type || "").trim();
    const resourceID = String(item.resource_id || "").trim();
    const actorAgentID = formatAgentID(item.actor_agent_id);
    const actorEmail = String(item.actor_email || "").trim();
    const metaParts = [];
    if (action) {
      metaParts.push(action);
    }
    if (resourceType || resourceID) {
      metaParts.push(`${resourceType || "-"}:${resourceID || "-"}`);
    }
    if (actorEmail) {
      metaParts.push(`操作者 ${actorEmail} (${actorAgentID})`);
    }
    if (item.ip) {
      metaParts.push(`IP ${item.ip}`);
    }

    const node = document.createElement("li");
    node.innerHTML = `
      <div class="operation-title">${escapeHTML(item.summary || item.title || "-")}</div>
      <div class="operation-meta">${escapeHTML(metaParts.join(" · "))}</div>
      <div class="operation-time">${escapeHTML(formatTime(item.created_at || item.createdAt))}</div>
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

function normalizeAgentID(raw) {
  return String(raw || "")
    .replace(/\s+/g, "")
    .trim();
}

function isValidAgentID(agentID) {
  return /^(?!0000)\d{4}$/.test(String(agentID || ""));
}

function formatAgentID(value) {
  const num = Number(value);
  if (Number.isInteger(num) && num > 0 && num <= 9999) {
    return String(num).padStart(4, "0");
  }
  const text = normalizeAgentID(value);
  return text || "-";
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
