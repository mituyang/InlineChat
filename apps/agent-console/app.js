const DEFAULT_QUICK_REPLIES = [
  "您好，我来协助您处理这个问题。",
  "请稍等，我正在为您核实订单信息。",
  "已收到您的反馈，我们会尽快处理。",
  "如果方便，请提供一下订单号或手机号。",
  "感谢您的耐心等待，还有其他问题我也可以继续协助。",
];

const STORAGE_KEYS = {
  token: "inlinechat.staff.token",
  quickRepliesPrefix: "inlinechat.agent.quick_replies.",
  readCursorPrefix: "inlinechat.agent.read_cursor.",
};
const LEGACY_TOKEN_KEYS = ["inlinechat.agent.token", "inlinechat.admin.token"];
const LOGIN_PAGE_URL = "/app/staff-login/";
const AGENT_HOME_URL = "/app/agent/";
const ADMIN_HOME_URL = "/app/admin/";
const ALLOWED_ROLES = new Set(["agent"]);
const THEME_STORAGE_KEY = "inlinechat.ui.theme";

const state = {
  token: "",
  me: null,
  theme: "light",
  queueMode: "all",
  queueShortcut: "all",
  conversationSearch: "",
  conversations: [],
  activeConversationId: "",
  messages: [],
  ws: null,
  wsConversationId: "",
  wsConnected: false,
  wsReconnectTimer: null,
  wsReconnectAttempt: 0,
  conversationTimer: null,
  messageTimer: null,
  unreadMap: {},
  readCursor: {},
  unreadSeq: 0,
  quickReplies: [...DEFAULT_QUICK_REPLIES],
};

const els = {
  userBox: document.getElementById("userBox"),
  wsStateBadge: document.getElementById("wsStateBadge"),
  themeToggleBtn: document.getElementById("themeToggleBtn"),
  statusLine: document.getElementById("statusLine"),
  logoutBtn: document.getElementById("logoutBtn"),

  statTotal: document.getElementById("statTotal"),
  statOpen: document.getElementById("statOpen"),
  statWaiting: document.getElementById("statWaiting"),
  statMineOpen: document.getElementById("statMineOpen"),
  statClosed: document.getElementById("statClosed"),
  statUnassigned: document.getElementById("statUnassigned"),
  statUnread: document.getElementById("statUnread"),

  queueShortcuts: document.getElementById("queueShortcuts"),
  queueTabs: document.getElementById("queueTabs"),
  queueTabAll: document.getElementById("queueTabAll"),
  queueTabOpen: document.getElementById("queueTabOpen"),
  queueTabClosed: document.getElementById("queueTabClosed"),
  conversationSearchInput: document.getElementById("conversationSearchInput"),

  refreshConversationsBtn: document.getElementById("refreshConversationsBtn"),
  filterForm: document.getElementById("filterForm"),
  statusFilter: document.getElementById("statusFilter"),
  siteFilterInput: document.getElementById("siteFilterInput"),
  unassignedOnlyCheckbox: document.getElementById("unassignedOnlyCheckbox"),
  mineOnlyCheckbox: document.getElementById("mineOnlyCheckbox"),
  conversationList: document.getElementById("conversationList"),

  activeConversationTitle: document.getElementById("activeConversationTitle"),
  activeConversationMeta: document.getElementById("activeConversationMeta"),
  claimBtn: document.getElementById("claimBtn"),
  transferAgentIdInput: document.getElementById("transferAgentIdInput"),
  transferBtn: document.getElementById("transferBtn"),
  closeBtn: document.getElementById("closeBtn"),

  detailConversationId: document.getElementById("detailConversationId"),
  detailStatus: document.getElementById("detailStatus"),
  detailSiteId: document.getElementById("detailSiteId"),
  detailAssigned: document.getElementById("detailAssigned"),
  detailUpdatedAt: document.getElementById("detailUpdatedAt"),
  detailWaitingDuration: document.getElementById("detailWaitingDuration"),
  detailWsState: document.getElementById("detailWsState"),

  agentMessages: document.getElementById("agentMessages"),
  agentSendForm: document.getElementById("agentSendForm"),
  agentContentInput: document.getElementById("agentContentInput"),
  agentSendBtn: document.getElementById("agentSendBtn"),

  quickReplyList: document.getElementById("quickReplyList"),
  quickReplyForm: document.getElementById("quickReplyForm"),
  quickReplyInput: document.getElementById("quickReplyInput"),
  resetQuickRepliesBtn: document.getElementById("resetQuickRepliesBtn"),
};

init();

async function init() {
  initTheme();
  bindEvents();
  renderConversations([]);
  renderMessages([]);
  renderStats();
  renderQuickReplies();
  renderQueueTabsMeta();
  renderQueueShortcuts();
  resetConversationDetail();
  setWsIndicator("warn", "实时通道：待连接");

  const savedToken = readStaffToken();
  if (!savedToken) {
    redirectToLogin();
    return;
  }

  state.token = savedToken;
  try {
    await fetchMe();
    if (!ALLOWED_ROLES.has(String(state.me?.role || ""))) {
      const role = String(state.me?.role || "");
      if (role === "super_admin" || role === "admin") {
        redirectToAdmin("当前账号没有客服工作台权限，正在跳转管理后台");
        return;
      }
      clearAuth();
      redirectToLogin();
      return;
    }
    restoreAgentPreferences();
    applyAuthUI(true);
    await refreshConversations();
    startPolling();
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

  els.refreshConversationsBtn?.addEventListener("click", async () => {
    await refreshConversations();
  });

  els.filterForm?.addEventListener("change", async () => {
    state.queueShortcut = "all";
    renderQueueShortcuts();
    await refreshConversations();
  });

  els.queueTabs?.addEventListener("click", async (event) => {
    const target = event.target.closest("button.queue-tab");
    if (!target) {
      return;
    }
    const mode = target.dataset.mode || "all";
    if (!["all", "open", "closed"].includes(mode)) {
      return;
    }
    state.queueMode = mode;
    state.queueShortcut = "all";
    renderQueueShortcuts();
    renderQueueTabsMeta();
    await refreshConversations();
  });

  els.queueShortcuts?.addEventListener("click", async (event) => {
    const target = event.target.closest("button.queue-shortcut");
    if (!target) {
      return;
    }
    const shortcut = String(target.dataset.shortcut || "all");
    applyQueueShortcut(shortcut);
    await refreshConversations();
  });

  els.conversationSearchInput?.addEventListener("input", () => {
    state.conversationSearch = (els.conversationSearchInput.value || "").trim().toLowerCase();
    renderConversations(state.conversations);
  });

  els.claimBtn?.addEventListener("click", async () => {
    await claimConversation();
  });

  els.transferBtn?.addEventListener("click", async () => {
    await transferConversation();
  });

  els.closeBtn?.addEventListener("click", async () => {
    await closeConversation();
  });

  els.agentSendForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await sendAgentMessage();
  });

  els.agentContentInput?.addEventListener("keydown", async (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      await sendAgentMessage();
    }
  });

  els.quickReplyForm?.addEventListener("submit", (event) => {
    event.preventDefault();
    addQuickReply();
  });

  els.quickReplyList?.addEventListener("click", (event) => {
    const btn = event.target.closest("button.quick-reply-chip");
    if (!btn) {
      return;
    }
    const text = btn.dataset.text || "";
    insertQuickReply(text);
  });

  els.resetQuickRepliesBtn?.addEventListener("click", () => {
    state.quickReplies = [...DEFAULT_QUICK_REPLIES];
    saveQuickReplies();
    renderQuickReplies();
    setStatus("快捷语已重置");
  });
}

function applyQueueShortcut(shortcut) {
  state.queueShortcut = shortcut;

  if (shortcut === "unassigned-open") {
    state.queueMode = "open";
    els.statusFilter.value = "";
    els.unassignedOnlyCheckbox.checked = true;
    els.mineOnlyCheckbox.checked = false;
  } else if (shortcut === "mine-open") {
    state.queueMode = "open";
    els.statusFilter.value = "";
    els.unassignedOnlyCheckbox.checked = false;
    els.mineOnlyCheckbox.checked = true;
  } else {
    state.queueMode = "all";
    els.statusFilter.value = "";
    els.unassignedOnlyCheckbox.checked = false;
    els.mineOnlyCheckbox.checked = true;
    state.queueShortcut = "all";
  }

  renderQueueShortcuts();
  renderQueueTabsMeta();
}

function renderQueueShortcuts() {
  const buttons = els.queueShortcuts?.querySelectorAll("button.queue-shortcut") || [];
  for (const button of buttons) {
    const shortcut = String(button.dataset.shortcut || "all");
    button.classList.toggle("active", shortcut === state.queueShortcut);
  }
}

function resolveQueueShortcutFromFilters() {
  const explicitStatus = (els.statusFilter.value || "").trim();
  const status = explicitStatus || state.queueMode;
  const unassignedOnly = Boolean(els.unassignedOnlyCheckbox.checked);
  const mineOnly = Boolean(els.mineOnlyCheckbox.checked);

  if (status === "open" && unassignedOnly) {
    return "unassigned-open";
  }
  if (status === "open" && !unassignedOnly && mineOnly) {
    return "mine-open";
  }
  return "all";
}

function applyAuthUI(loggedIn) {
  els.claimBtn.disabled = !loggedIn;
  els.transferBtn.disabled = !loggedIn;
  els.closeBtn.disabled = !loggedIn;
  els.agentSendBtn.disabled = !loggedIn;

  if (!loggedIn) {
    els.userBox.textContent = "未登录";
    state.conversations = [];
    state.activeConversationId = "";
    state.messages = [];
    state.unreadMap = {};
    state.readCursor = {};
    state.queueShortcut = "all";
    renderConversations([]);
    renderMessages([]);
    renderStats();
    renderQueueTabsMeta();
    renderQueueShortcuts();
    resetConversationDetail();
    closeWebSocket();
    stopPolling();
    document.body.classList.add("auth-guard");
    return;
  }

  if (state.me) {
    els.userBox.textContent = `${state.me.email} (${state.me.role})`;
  }
  document.body.classList.remove("auth-guard");
  renderStats();
}

async function fetchMe() {
  const data = await apiRequest("/api/auth/v1/auth/me", {
    auth: true,
  });
  state.me = data;
  els.userBox.textContent = `${data.email} (${data.role})`;
}

function restoreAgentPreferences() {
  state.readCursor = loadReadCursor();
  state.unreadMap = {};
  state.quickReplies = loadQuickReplies();
  renderQuickReplies();
}

function clearAuth() {
  state.token = "";
  state.me = null;
  localStorage.removeItem(STORAGE_KEYS.token);
  for (const key of LEGACY_TOKEN_KEYS) {
    localStorage.removeItem(key);
  }
}

function readStaffToken() {
  const sharedToken = localStorage.getItem(STORAGE_KEYS.token);
  if (sharedToken) {
    return sharedToken;
  }

  for (const key of LEGACY_TOKEN_KEYS) {
    const legacyToken = localStorage.getItem(key);
    if (!legacyToken) {
      continue;
    }
    localStorage.setItem(STORAGE_KEYS.token, legacyToken);
    localStorage.removeItem(key);
    return legacyToken;
  }

  return "";
}

function redirectToLogin(message = "") {
  const next = encodeURIComponent(AGENT_HOME_URL);
  const target = `${LOGIN_PAGE_URL}?next=${next}`;
  if (message) {
    setStatus(message, true);
  }
  window.location.replace(target);
}

function redirectToAdmin(message = "") {
  if (message) {
    setStatus(message, true);
  }
  window.location.replace(ADMIN_HOME_URL);
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

function setWsIndicator(mode, text) {
  if (!els.wsStateBadge) {
    return;
  }

  els.wsStateBadge.classList.remove("warn", "offline");
  if (mode === "warn") {
    els.wsStateBadge.classList.add("warn");
  } else if (mode === "offline") {
    els.wsStateBadge.classList.add("offline");
  }

  if (text) {
    els.wsStateBadge.textContent = text;
  }

  if (els.detailWsState) {
    if (mode === "online") {
      els.detailWsState.textContent = "已连接";
    } else if (mode === "warn") {
      els.detailWsState.textContent = "重连中";
    } else {
      els.detailWsState.textContent = "未连接";
    }
  }
}

function startPolling() {
  stopPolling();

  state.conversationTimer = setInterval(() => {
    if (!state.token) {
      return;
    }
    refreshConversations().catch((error) => {
      setStatus(error.message || "会话刷新失败", true);
    });
  }, 7000);

  state.messageTimer = setInterval(() => {
    if (!state.token || !state.activeConversationId) {
      return;
    }
    if (state.wsConnected && state.wsConversationId === state.activeConversationId) {
      return;
    }
    refreshMessages().catch((error) => {
      setStatus(error.message || "消息刷新失败", true);
    });
  }, 3000);
}

function stopPolling() {
  if (state.conversationTimer) {
    clearInterval(state.conversationTimer);
    state.conversationTimer = null;
  }
  if (state.messageTimer) {
    clearInterval(state.messageTimer);
    state.messageTimer = null;
  }
}

async function refreshConversations() {
  if (!state.token) {
    return;
  }

  const search = new URLSearchParams();
  search.set("limit", "120");

  const explicitStatus = (els.statusFilter.value || "").trim();
  const status = explicitStatus || (state.queueMode === "all" ? "" : state.queueMode);
  if (status) {
    search.set("status", status);
  }

  const siteId = els.siteFilterInput.value.trim();
  if (siteId) {
    search.set("site_id", siteId);
  }

  const unassignedOnly = els.unassignedOnlyCheckbox.checked;
  if (unassignedOnly) {
    search.set("unassigned_only", "true");
  }

  if (!unassignedOnly && els.mineOnlyCheckbox.checked && state.me?.agent_id) {
    search.set("assigned_agent_id", String(state.me.agent_id));
  }

  const data = await apiRequest(`/api/chat/v1/conversations?${search.toString()}`, {
    auth: true,
  });

  state.conversations = Array.isArray(data.items) ? data.items : [];
  state.conversations.sort((a, b) => {
    const ta = new Date(a.updated_at || a.created_at || 0).getTime();
    const tb = new Date(b.updated_at || b.created_at || 0).getTime();
    return tb - ta;
  });
  state.queueShortcut = resolveQueueShortcutFromFilters();

  if (state.activeConversationId) {
    const found = state.conversations.find((item) => String(item.id) === state.activeConversationId);
    if (found) {
      updateActiveConversationHeader(found);
    } else {
      state.activeConversationId = "";
      state.messages = [];
      closeWebSocket();
      renderMessages([]);
      resetConversationDetail();
      els.activeConversationTitle.textContent = "未选择会话";
      els.activeConversationMeta.textContent = "请先在左侧选择会话";
    }
  }

  renderConversations(state.conversations);
  renderStats();
  renderQueueTabsMeta();
  renderQueueShortcuts();
  void refreshUnreadCounts(state.conversations);
}

async function refreshUnreadCounts(items) {
  if (!state.token) {
    return;
  }

  const seq = ++state.unreadSeq;
  const openItems = items.filter((item) => item.status === "open").slice(0, 24);
  const nextUnreadMap = {};

  await Promise.all(
    openItems.map(async (item) => {
      const conversationID = String(item.id);
      try {
        const data = await apiRequest(`/api/chat/v1/conversations/${conversationID}/messages?limit=80`, {
          auth: true,
        });
        const messages = Array.isArray(data.items) ? data.items : [];
        const cursor = Number(state.readCursor[conversationID] || 0);
        const unread = messages.reduce((sum, msg) => {
          const id = Number(msg.id || 0);
          if (msg.sender_type === "visitor" && id > cursor) {
            return sum + 1;
          }
          return sum;
        }, 0);
        nextUnreadMap[conversationID] = unread;
      } catch {
        nextUnreadMap[conversationID] = Number(state.unreadMap[conversationID] || 0);
      }
    })
  );

  if (seq !== state.unreadSeq) {
    return;
  }

  if (state.activeConversationId) {
    nextUnreadMap[state.activeConversationId] = 0;
  }
  state.unreadMap = nextUnreadMap;
  renderConversations(state.conversations);
  renderStats();
  renderQueueTabsMeta();
}

function filteredConversations(items) {
  const keyword = state.conversationSearch;
  if (!keyword) {
    return items;
  }

  return items.filter((item) => {
    const idText = String(item.id || "").toLowerCase();
    const siteText = String(item.site_id || "").toLowerCase();
    return idText.includes(keyword) || siteText.includes(keyword);
  });
}

function renderConversations(items) {
  const list = filteredConversations(Array.isArray(items) ? items : []);

  if (list.length === 0) {
    els.conversationList.innerHTML = '<div class="empty">暂无会话</div>';
    return;
  }

  els.conversationList.innerHTML = "";
  for (const item of list) {
    const id = String(item.id);
    const entry = document.createElement("article");
    entry.className = "conversation-item";
    if (id === state.activeConversationId) {
      entry.classList.add("active");
    }

    const row = document.createElement("div");
    row.className = "conversation-title-row";

    const title = document.createElement("strong");
    title.textContent = `#${item.id}`;
    row.appendChild(title);

    const statusChip = document.createElement("span");
    statusChip.className = `status-chip ${item.status === "closed" ? "closed" : "open"}`;
    statusChip.textContent = item.status === "closed" ? "已关闭" : "进行中";
    row.appendChild(statusChip);

    const unread = Number(state.unreadMap[id] || 0);
    if (unread > 0) {
      const badge = document.createElement("span");
      badge.className = "unread-badge";
      badge.textContent = unread > 99 ? "99+" : String(unread);
      row.appendChild(badge);
    }

    const meta1 = document.createElement("div");
    meta1.className = "meta";
    const assigned = item.assigned_agent_id ? `坐席 ${item.assigned_agent_id}` : "未分配";
    meta1.textContent = `${assigned} · site=${item.site_id}`;

    const meta2 = document.createElement("div");
    meta2.className = "meta";
    meta2.textContent = `更新时间 ${formatTime(item.updated_at)} · ${formatDurationSince(item.updated_at || item.created_at)}`;

    entry.appendChild(row);
    entry.appendChild(meta1);
    entry.appendChild(meta2);

    entry.addEventListener("click", async () => {
      await selectConversation(item);
    });

    els.conversationList.appendChild(entry);
  }
}

function renderQueueTabsMeta() {
  const all = state.conversations.length;
  const open = state.conversations.filter((item) => item.status === "open").length;
  const closed = state.conversations.filter((item) => item.status === "closed").length;

  if (els.queueTabAll) {
    els.queueTabAll.textContent = `全部 ${all}`;
    els.queueTabAll.classList.toggle("active", state.queueMode === "all");
  }
  if (els.queueTabOpen) {
    els.queueTabOpen.textContent = `进行中 ${open}`;
    els.queueTabOpen.classList.toggle("active", state.queueMode === "open");
  }
  if (els.queueTabClosed) {
    els.queueTabClosed.textContent = `已关闭 ${closed}`;
    els.queueTabClosed.classList.toggle("active", state.queueMode === "closed");
  }
}

async function selectConversation(conversation) {
  state.activeConversationId = String(conversation.id);
  state.messages = [];
  clearWsReconnectTimer();
  state.wsReconnectAttempt = 0;
  renderConversations(state.conversations);
  updateActiveConversationHeader(conversation);
  renderMessages([]);

  await refreshMessages();
  connectWebSocket();
  setStatus(`已切换到会话 #${conversation.id}`);
}

function updateActiveConversationHeader(conversation) {
  const assigned = conversation.assigned_agent_id ? `坐席 ${conversation.assigned_agent_id}` : "未分配";
  els.activeConversationTitle.textContent = `会话 #${conversation.id}`;
  els.activeConversationMeta.textContent = `状态 ${conversation.status} · ${assigned} · site=${conversation.site_id}`;

  els.detailConversationId.textContent = String(conversation.id || "-");
  els.detailStatus.textContent = String(conversation.status || "-");
  els.detailSiteId.textContent = String(conversation.site_id || "-");
  els.detailAssigned.textContent = assigned;
  els.detailUpdatedAt.textContent = formatTime(conversation.updated_at || conversation.created_at);
  els.detailWaitingDuration.textContent = formatDurationSince(conversation.updated_at || conversation.created_at);
}

function resetConversationDetail() {
  els.detailConversationId.textContent = "-";
  els.detailStatus.textContent = "-";
  els.detailSiteId.textContent = "-";
  els.detailAssigned.textContent = "-";
  els.detailUpdatedAt.textContent = "-";
  els.detailWaitingDuration.textContent = "-";
  els.detailWsState.textContent = "未连接";
}

async function refreshMessages() {
  if (!state.activeConversationId) {
    return;
  }

  const data = await apiRequest(`/api/chat/v1/conversations/${state.activeConversationId}/messages?limit=200`, {
    auth: true,
  });
  const items = Array.isArray(data.items) ? data.items : [];
  mergeMessages(items);
}

function mergeMessages(items) {
  const dict = new Map();
  for (const msg of state.messages) {
    if (msg && msg.id) {
      dict.set(String(msg.id), msg);
    }
  }
  for (const msg of items) {
    if (msg && msg.id) {
      dict.set(String(msg.id), msg);
    }
  }

  state.messages = Array.from(dict.values()).sort((a, b) => Number(a.id) - Number(b.id));
  renderMessages(state.messages);

  if (state.activeConversationId) {
    markConversationRead(state.activeConversationId, state.messages);
  }
}

function renderMessages(items) {
  if (!Array.isArray(items) || items.length === 0) {
    els.agentMessages.innerHTML = '<div class="empty">暂无消息</div>';
    return;
  }

  els.agentMessages.innerHTML = "";
  for (const item of items) {
    const mine =
      item.sender_type === "agent" &&
      item.sender_id &&
      state.me &&
      String(item.sender_id) === String(state.me.agent_id);

    const block = document.createElement("article");
    block.className = `message ${mine ? "mine" : "other"}`;

    const content = document.createElement("div");
    content.textContent = item.content || "";

    const meta = document.createElement("div");
    meta.className = "meta";
    const sender =
      item.sender_type === "visitor"
        ? "访客"
        : item.sender_type === "agent"
          ? `客服 ${item.sender_id || ""}`
          : "系统";
    meta.textContent = `${sender} · ${formatTime(item.created_at)}`;

    block.appendChild(content);
    block.appendChild(meta);
    els.agentMessages.appendChild(block);
  }

  els.agentMessages.scrollTop = els.agentMessages.scrollHeight;
}

function markConversationRead(conversationID, messages) {
  if (!conversationID || !Array.isArray(messages)) {
    return;
  }

  const maxMessageID = messages.reduce((max, msg) => {
    const id = Number(msg.id || 0);
    return id > max ? id : max;
  }, 0);

  if (maxMessageID <= 0) {
    return;
  }

  const prev = Number(state.readCursor[conversationID] || 0);
  if (maxMessageID > prev) {
    state.readCursor[conversationID] = maxMessageID;
    saveReadCursor();
  }

  if (Number(state.unreadMap[conversationID] || 0) !== 0) {
    state.unreadMap[conversationID] = 0;
    renderConversations(state.conversations);
  }
  renderStats();
  renderQueueTabsMeta();
}

function renderStats() {
  const items = Array.isArray(state.conversations) ? state.conversations : [];
  const meID = Number(state.me?.agent_id || 0);

  const total = items.length;
  const open = items.filter((item) => item.status === "open").length;
  const closed = items.filter((item) => item.status === "closed").length;
  const unassigned = items.filter((item) => !item.assigned_agent_id).length;
  const waiting = items.filter((item) => item.status === "open" && !item.assigned_agent_id).length;
  const mineOpen = items.filter(
    (item) => item.status === "open" && meID > 0 && Number(item.assigned_agent_id || 0) === meID
  ).length;
  const unread = Object.values(state.unreadMap).reduce((sum, v) => sum + Number(v || 0), 0);

  els.statTotal.textContent = String(total);
  els.statOpen.textContent = String(open);
  if (els.statWaiting) {
    els.statWaiting.textContent = String(waiting);
  }
  if (els.statMineOpen) {
    els.statMineOpen.textContent = String(mineOpen);
  }
  els.statClosed.textContent = String(closed);
  els.statUnassigned.textContent = String(unassigned);
  els.statUnread.textContent = String(unread);
}

function renderQuickReplies() {
  const list = Array.isArray(state.quickReplies) ? state.quickReplies : [];
  if (list.length === 0) {
    els.quickReplyList.innerHTML = '<div class="empty">暂无快捷语</div>';
    return;
  }

  els.quickReplyList.innerHTML = "";
  for (const reply of list) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "quick-reply-chip";
    btn.dataset.text = reply;
    btn.textContent = reply;
    els.quickReplyList.appendChild(btn);
  }
}

function addQuickReply() {
  const text = els.quickReplyInput.value.trim();
  if (!text) {
    return;
  }

  if (state.quickReplies.includes(text)) {
    els.quickReplyInput.value = "";
    return;
  }

  state.quickReplies.unshift(text);
  state.quickReplies = state.quickReplies.slice(0, 30);
  saveQuickReplies();
  renderQuickReplies();
  els.quickReplyInput.value = "";
  setStatus("快捷语已新增");
}

function insertQuickReply(text) {
  if (!text) {
    return;
  }
  const current = els.agentContentInput.value.trim();
  els.agentContentInput.value = current ? `${current}\n${text}` : text;
  els.agentContentInput.focus();
}

async function claimConversation() {
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }

  try {
    await apiRequest(`/api/chat/v1/conversations/${state.activeConversationId}/claim`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    setStatus("认领成功");
  } catch (error) {
    setStatus(error.message || "认领失败", true);
  }
}

async function transferConversation() {
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }

  const toAgentID = Number(els.transferAgentIdInput.value);
  if (!Number.isInteger(toAgentID) || toAgentID <= 0) {
    setStatus("请输入有效的目标坐席 ID", true);
    return;
  }

  try {
    await apiRequest(`/api/chat/v1/conversations/${state.activeConversationId}/transfer`, {
      method: "POST",
      auth: true,
      body: {
        to_agent_id: toAgentID,
      },
    });
    await refreshConversations();
    setStatus(`已转接到坐席 ${toAgentID}`);
  } catch (error) {
    setStatus(error.message || "转接失败", true);
  }
}

async function closeConversation() {
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }

  try {
    await apiRequest(`/api/chat/v1/conversations/${state.activeConversationId}/close`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    setStatus("会话已关闭");
  } catch (error) {
    setStatus(error.message || "关闭失败", true);
  }
}

async function sendAgentMessage() {
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }
  if (!state.me?.agent_id) {
    setStatus("当前登录信息缺少 agent_id", true);
    return;
  }

  const content = els.agentContentInput.value.trim();
  if (!content) {
    return;
  }

  els.agentSendBtn.disabled = true;
  try {
    await apiRequest(`/api/chat/v1/conversations/${state.activeConversationId}/messages`, {
      method: "POST",
      auth: true,
      body: {
        sender_type: "agent",
        sender_id: String(state.me.agent_id),
        content,
        client_msg_id: `a_${safeUUID()}`,
      },
    });
    els.agentContentInput.value = "";
    if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
      await refreshMessages();
    }
    setStatus("消息已发送");
  } catch (error) {
    setStatus(error.message || "发送失败", true);
  } finally {
    els.agentSendBtn.disabled = false;
    els.agentContentInput.focus();
  }
}

function connectWebSocket() {
  if (!state.activeConversationId) {
    setWsIndicator("offline", "实时通道：未绑定会话");
    return;
  }

  clearWsReconnectTimer();
  if (state.ws) {
    state.ws.close();
    state.ws = null;
  }

  const conversationID = state.activeConversationId;
  state.wsConversationId = conversationID;
  state.wsConnected = false;
  setWsIndicator("warn", `实时通道：连接中 #${conversationID}`);

  const wsUrl = `${window.location.protocol === "https:" ? "wss" : "ws"}://${window.location.host}/ws/${conversationID}`;
  const ws = new WebSocket(wsUrl);

  ws.addEventListener("open", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = true;
    state.wsReconnectAttempt = 0;
    setWsIndicator("online", `实时通道：已连接 #${conversationID}`);
    setStatus(`WebSocket 已连接，会话 #${conversationID}`);
  });

  ws.addEventListener("message", (event) => {
    try {
      const data = JSON.parse(event.data);
      if (data.type === "message.new" && data.payload && data.payload.message) {
        const cid = String(data.payload.conversation_id || data.payload.message.conversation_id || "");
        if (!cid || cid !== state.activeConversationId) {
          return;
        }
        mergeMessages([data.payload.message]);
      }
      if (data.type === "error") {
        setStatus(data.error || "WebSocket 消息异常", true);
      }
    } catch {
      setStatus("收到无法解析的 WebSocket 消息", true);
    }
  });

  ws.addEventListener("close", () => {
    if (state.ws !== ws) {
      return;
    }
    state.ws = null;
    state.wsConnected = false;
    if (conversationID !== state.activeConversationId) {
      return;
    }
    setWsIndicator("warn", `实时通道：重连中 #${conversationID}`);
    scheduleWsReconnect(conversationID);
    setStatus("WebSocket 已断开，正在自动重连", true);
  });

  ws.addEventListener("error", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = false;
    setWsIndicator("warn", `实时通道：异常 #${conversationID}`);
    setStatus("WebSocket 异常，正在自动重连", true);
  });

  state.ws = ws;
}

function closeWebSocket() {
  clearWsReconnectTimer();
  if (state.ws) {
    state.ws.close();
    state.ws = null;
  }
  state.wsConnected = false;
  state.wsConversationId = "";
  state.wsReconnectAttempt = 0;
  setWsIndicator("offline", "实时通道：未连接");
}

function clearWsReconnectTimer() {
  if (state.wsReconnectTimer) {
    clearTimeout(state.wsReconnectTimer);
    state.wsReconnectTimer = null;
  }
}

function scheduleWsReconnect(conversationID) {
  if (!conversationID || conversationID !== state.activeConversationId) {
    return;
  }
  if (state.wsReconnectTimer) {
    return;
  }

  const baseDelay = Math.min(1000 * 2 ** state.wsReconnectAttempt, 10000);
  const jitter = Math.floor(Math.random() * 250);
  const delay = baseDelay + jitter;
  state.wsReconnectAttempt += 1;

  state.wsReconnectTimer = setTimeout(() => {
    state.wsReconnectTimer = null;
    if (!state.token || !state.activeConversationId || state.activeConversationId !== conversationID) {
      return;
    }
    connectWebSocket();
  }, delay);
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

function loadQuickReplies() {
  const key = quickRepliesStorageKey();
  if (!key) {
    return [...DEFAULT_QUICK_REPLIES];
  }

  const raw = localStorage.getItem(key);
  if (!raw) {
    return [...DEFAULT_QUICK_REPLIES];
  }

  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [...DEFAULT_QUICK_REPLIES];
    }
    const normalized = parsed
      .map((item) => String(item || "").trim())
      .filter((item) => Boolean(item));
    return normalized.length > 0 ? normalized.slice(0, 30) : [...DEFAULT_QUICK_REPLIES];
  } catch {
    return [...DEFAULT_QUICK_REPLIES];
  }
}

function saveQuickReplies() {
  const key = quickRepliesStorageKey();
  if (!key) {
    return;
  }
  localStorage.setItem(key, JSON.stringify(state.quickReplies));
}

function loadReadCursor() {
  const key = readCursorStorageKey();
  if (!key) {
    return {};
  }

  const raw = localStorage.getItem(key);
  if (!raw) {
    return {};
  }

  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") {
      return {};
    }
    return parsed;
  } catch {
    return {};
  }
}

function saveReadCursor() {
  const key = readCursorStorageKey();
  if (!key) {
    return;
  }
  localStorage.setItem(key, JSON.stringify(state.readCursor));
}

function quickRepliesStorageKey() {
  if (!state.me?.agent_id) {
    return "";
  }
  return `${STORAGE_KEYS.quickRepliesPrefix}${state.me.agent_id}`;
}

function readCursorStorageKey() {
  if (!state.me?.agent_id) {
    return "";
  }
  return `${STORAGE_KEYS.readCursorPrefix}${state.me.agent_id}`;
}

function safeUUID() {
  if (window.crypto && window.crypto.randomUUID) {
    return window.crypto.randomUUID();
  }
  return `${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
}

function formatDurationSince(value) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }

  const diffMs = Date.now() - date.getTime();
  if (diffMs <= 0) {
    return "刚刚";
  }

  const minutes = Math.floor(diffMs / 60000);
  if (minutes < 1) {
    return "1 分钟内";
  }
  if (minutes < 60) {
    return `${minutes} 分钟`;
  }
  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} 小时 ${minutes % 60} 分钟`;
  }
  const days = Math.floor(hours / 24);
  return `${days} 天 ${hours % 24} 小时`;
}

function formatTime(value) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

window.addEventListener("beforeunload", () => {
  closeWebSocket();
  stopPolling();
});
