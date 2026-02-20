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
const ACK_TIMEOUT_MS = 5000;

const state = {
  token: "",
  me: null,
  theme: "light",
  queueMode: "all",
  queueShortcut: "all",
  conversationSearch: "",
  conversations: [],
  statsConversations: [],
  activeConversationId: "",
  activeConversation: null,
  selectionSeq: 0,
  messages: [],
  messageAbortController: null,
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
  pendingMap: {},
  actionPending: {
    select: false,
    claim: false,
    transfer: false,
    close: false,
    send: false,
  },
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
  updateConversationActionState();

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

  els.filterForm?.addEventListener("change", async (event) => {
    const target = event.target;
    if (target === els.statusFilter) {
      const mode = String(els.statusFilter.value || "").trim();
      state.queueMode = mode === "open" || mode === "closed" ? mode : "all";
    }

    enforceOwnershipFilterExclusivity(target);
    syncStatusFilterWithQueueMode();
    state.queueShortcut = resolveQueueShortcutFromFilters();
    renderQueueShortcuts();
    renderQueueTabsMeta();
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
    syncStatusFilterWithQueueMode();
    state.queueShortcut = resolveQueueShortcutFromFilters();
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
  if (shortcut === "unassigned-open") {
    state.queueMode = "open";
    els.unassignedOnlyCheckbox.checked = true;
    els.mineOnlyCheckbox.checked = false;
  } else if (shortcut === "mine-open") {
    state.queueMode = "open";
    els.unassignedOnlyCheckbox.checked = false;
    els.mineOnlyCheckbox.checked = true;
  } else {
    state.queueMode = "all";
    els.unassignedOnlyCheckbox.checked = false;
    els.mineOnlyCheckbox.checked = false;
  }

  syncStatusFilterWithQueueMode();
  state.queueShortcut = resolveQueueShortcutFromFilters();
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
  const status = state.queueMode;
  const unassignedOnly = Boolean(els.unassignedOnlyCheckbox.checked);
  const mineOnly = Boolean(els.mineOnlyCheckbox.checked);

  if (status === "open" && unassignedOnly && !mineOnly) {
    return "unassigned-open";
  }
  if (status === "open" && !unassignedOnly && mineOnly) {
    return "mine-open";
  }
  return "all";
}

function syncStatusFilterWithQueueMode() {
  const targetStatus = state.queueMode === "all" ? "" : state.queueMode;
  if (els.statusFilter && els.statusFilter.value !== targetStatus) {
    els.statusFilter.value = targetStatus;
  }
}

function enforceOwnershipFilterExclusivity(target) {
  if (target === els.unassignedOnlyCheckbox && els.unassignedOnlyCheckbox.checked) {
    els.mineOnlyCheckbox.checked = false;
    return;
  }
  if (target === els.mineOnlyCheckbox && els.mineOnlyCheckbox.checked) {
    els.unassignedOnlyCheckbox.checked = false;
  }
}

function setActionPending(action, pending) {
  if (!state.actionPending || !(action in state.actionPending)) {
    return;
  }
  state.actionPending[action] = Boolean(pending);
  updateConversationActionState();
}

function getActiveConversationCapability() {
  const loggedIn = Boolean(state.token && state.me);
  const conversation = state.activeConversation;
  const hasConversation =
    loggedIn &&
    Boolean(state.activeConversationId) &&
    Boolean(conversation) &&
    String(conversation.id || "") === String(state.activeConversationId);

  const status = String(conversation?.status || "").trim().toLowerCase();
  const isOpen = status === "open";
  const assignedAgentID = Number(conversation?.assigned_agent_id || 0);
  const meID = Number(state.me?.agent_id || 0);
  const isMine = meID > 0 && assignedAgentID > 0 && assignedAgentID === meID;
  const isUnassigned = assignedAgentID <= 0;

  return {
    hasConversation,
    isOpen,
    isMine,
    isUnassigned,
  };
}

function updateConversationActionState() {
  const capability = getActiveConversationCapability();
  const hasConversation = capability.hasConversation;
  const isOpen = capability.isOpen;
  const isMine = capability.isMine;
  const isUnassigned = capability.isUnassigned;

  const selectPending = Boolean(state.actionPending.select);
  const canClaim =
    hasConversation && isOpen && isUnassigned && !selectPending && !state.actionPending.claim;
  const canTransfer =
    hasConversation && isOpen && isMine && !selectPending && !state.actionPending.transfer;
  const canClose = hasConversation && isOpen && isMine && !selectPending && !state.actionPending.close;
  const canSend = hasConversation && isOpen && isMine && !selectPending && !state.actionPending.send;

  if (els.claimBtn) {
    els.claimBtn.disabled = !canClaim;
  }
  if (els.transferBtn) {
    els.transferBtn.disabled = !canTransfer;
  }
  if (els.transferAgentIdInput) {
    els.transferAgentIdInput.disabled = !canTransfer;
  }
  if (els.closeBtn) {
    els.closeBtn.disabled = !canClose;
  }
  if (els.agentSendBtn) {
    els.agentSendBtn.disabled = !canSend;
  }
  if (els.agentContentInput) {
    els.agentContentInput.disabled = !canSend;
  }
}

function abortMessageRequest() {
  if (!state.messageAbortController) {
    return;
  }
  state.messageAbortController.abort();
  state.messageAbortController = null;
}

function applyAuthUI(loggedIn) {
  if (!loggedIn) {
    els.userBox.textContent = "未登录";
    state.conversations = [];
    state.statsConversations = [];
    state.activeConversationId = "";
    state.activeConversation = null;
    state.selectionSeq = 0;
    state.messages = [];
    state.unreadMap = {};
    state.readCursor = {};
    state.queueShortcut = "all";
    state.actionPending = {
      select: false,
      claim: false,
      transfer: false,
      close: false,
      send: false,
    };
    abortMessageRequest();
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
  updateConversationActionState();
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

  syncStatusFilterWithQueueMode();
  const status = state.queueMode === "all" ? "" : state.queueMode;
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

  const [queueData, statsData] = await Promise.all([
    apiRequest(`/api/chat/v1/conversations?${search.toString()}`, {
      auth: true,
    }),
    // 顶部统计独立于队列筛选，始终基于全量会话快照。
    apiRequest("/api/chat/v1/conversations?limit=200", {
      auth: true,
    }),
  ]);

  state.conversations = Array.isArray(queueData.items) ? queueData.items : [];
  state.conversations.sort((a, b) => {
    const ta = new Date(a.updated_at || a.created_at || 0).getTime();
    const tb = new Date(b.updated_at || b.created_at || 0).getTime();
    return tb - ta;
  });
  state.statsConversations = Array.isArray(statsData.items) ? statsData.items : [];
  state.statsConversations.sort((a, b) => {
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
      state.activeConversation = null;
      state.selectionSeq += 1;
      setActionPending("select", false);
      state.messages = [];
      abortMessageRequest();
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
  void refreshUnreadCounts(state.statsConversations);
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

  if (state.activeConversationId && canAgentMarkConversationRead(state.activeConversationId)) {
    nextUnreadMap[state.activeConversationId] = 0;
  }
  state.unreadMap = nextUnreadMap;
  renderConversations(state.conversations);
  renderStats();
  renderQueueTabsMeta();
}

function canAgentMarkConversationRead(conversationID) {
  const id = String(conversationID || "").trim();
  if (!id) {
    return false;
  }

  const meID = Number(state.me?.agent_id || 0);
  if (meID <= 0) {
    return false;
  }

  let conversation = null;
  if (state.activeConversation && String(state.activeConversation.id || "") === id) {
    conversation = state.activeConversation;
  }
  if (!conversation && Array.isArray(state.conversations)) {
    conversation = state.conversations.find((item) => String(item?.id || "") === id) || null;
  }
  if (!conversation && Array.isArray(state.statsConversations)) {
    conversation = state.statsConversations.find((item) => String(item?.id || "") === id) || null;
  }
  if (!conversation) {
    return false;
  }

  const assignedAgentID = Number(conversation.assigned_agent_id || 0);
  return assignedAgentID > 0 && assignedAgentID === meID;
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
  const items = Array.isArray(state.statsConversations) ? state.statsConversations : [];
  const all = items.length;
  const open = items.filter((item) => item.status === "open").length;
  const closed = items.filter((item) => item.status === "closed").length;

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
  const conversationID = String(conversation?.id || "").trim();
  if (!conversationID) {
    return;
  }

  const selectionSeq = state.selectionSeq + 1;
  state.selectionSeq = selectionSeq;
  setActionPending("select", true);

  state.activeConversationId = conversationID;
  state.activeConversation = conversation;
  resetPendingMap();
  state.messages = [];
  abortMessageRequest();
  clearWsReconnectTimer();
  state.wsReconnectAttempt = 0;
  renderConversations(state.conversations);
  updateActiveConversationHeader(conversation);
  renderMessages([], { forceScrollBottom: true });

  try {
    await refreshMessages({
      conversationID,
      force: true,
      forceScrollBottom: true,
    });
    if (selectionSeq !== state.selectionSeq || conversationID !== String(state.activeConversationId || "")) {
      return;
    }

    connectWebSocket();
    setStatus(`已切换到会话 #${conversation.id}`);
  } catch (error) {
    if (selectionSeq !== state.selectionSeq || conversationID !== String(state.activeConversationId || "")) {
      return;
    }
    setStatus(error.message || "消息加载失败", true);
  } finally {
    if (selectionSeq === state.selectionSeq) {
      setActionPending("select", false);
    }
  }
}

function updateActiveConversationHeader(conversation) {
  state.activeConversation = conversation || null;

  const assigned = conversation.assigned_agent_id ? `坐席 ${conversation.assigned_agent_id}` : "未分配";
  els.activeConversationTitle.textContent = `会话 #${conversation.id}`;
  els.activeConversationMeta.textContent = `状态 ${conversation.status} · ${assigned} · site=${conversation.site_id}`;

  els.detailConversationId.textContent = String(conversation.id || "-");
  els.detailStatus.textContent = String(conversation.status || "-");
  els.detailSiteId.textContent = String(conversation.site_id || "-");
  els.detailAssigned.textContent = assigned;
  els.detailUpdatedAt.textContent = formatTime(conversation.updated_at || conversation.created_at);
  els.detailWaitingDuration.textContent = formatDurationSince(conversation.updated_at || conversation.created_at);
  updateConversationActionState();
}

function resetConversationDetail() {
  state.activeConversation = null;
  els.detailConversationId.textContent = "-";
  els.detailStatus.textContent = "-";
  els.detailSiteId.textContent = "-";
  els.detailAssigned.textContent = "-";
  els.detailUpdatedAt.textContent = "-";
  els.detailWaitingDuration.textContent = "-";
  els.detailWsState.textContent = "未连接";
  updateConversationActionState();
}

async function refreshMessages(options = {}) {
  const conversationID = String(options.conversationID || state.activeConversationId || "").trim();
  if (!conversationID) {
    return;
  }

  if (state.messageAbortController && !options.force) {
    return;
  }
  if (options.force) {
    abortMessageRequest();
  }

  const controller = new AbortController();
  state.messageAbortController = controller;

  try {
    const data = await apiRequest(`/api/chat/v1/conversations/${conversationID}/messages?limit=200`, {
      auth: true,
      signal: controller.signal,
    });
    if (state.messageAbortController !== controller) {
      return;
    }
    if (conversationID !== String(state.activeConversationId || "")) {
      return;
    }
    const items = Array.isArray(data.items) ? data.items : [];
    mergeMessages(items, {
      forceScrollBottom: Boolean(options.forceScrollBottom),
    });
  } catch (error) {
    if (isAbortError(error)) {
      return;
    }
    throw error;
  } finally {
    if (state.messageAbortController === controller) {
      state.messageAbortController = null;
    }
  }
}

function mergeMessages(items, options = {}) {
  const byKey = new Map();
  const clientMsgKey = new Map();

  for (const current of state.messages) {
    const normalized = normalizeMessage(current);
    if (!normalized) {
      continue;
    }
    const key = getMessageKey(normalized);
    byKey.set(key, normalized);
    if (normalized.client_msg_id) {
      clientMsgKey.set(normalized.client_msg_id, key);
    }
  }

  for (const incomingRaw of items) {
    const incoming = normalizeMessage(incomingRaw);
    if (!incoming) {
      continue;
    }

    const clientMsgID = String(incoming.client_msg_id || "");
    const incomingKey = getMessageKey(incoming);
    let hitKey = "";
    if (clientMsgID && clientMsgKey.has(clientMsgID)) {
      hitKey = clientMsgKey.get(clientMsgID);
    } else if (incomingKey && byKey.has(incomingKey)) {
      hitKey = incomingKey;
    }

    if (!hitKey) {
      byKey.set(incomingKey, incoming);
      if (clientMsgID) {
        clientMsgKey.set(clientMsgID, incomingKey);
      }
    } else {
      const merged = mergeMessageRecord(byKey.get(hitKey), incoming);
      const mergedKey = getMessageKey(merged);
      if (mergedKey !== hitKey) {
        byKey.delete(hitKey);
      }
      byKey.set(mergedKey, merged);
      if (merged.client_msg_id) {
        clientMsgKey.set(merged.client_msg_id, mergedKey);
      }
    }

    if (clientMsgID && Number(incoming.id || 0) > 0) {
      clearPending(clientMsgID);
    }
  }

  state.messages = Array.from(byKey.values()).sort(compareMessageOrder);
  renderMessages(state.messages, {
    forceScrollBottom: Boolean(options.forceScrollBottom),
  });

  if (state.activeConversationId) {
    markConversationRead(state.activeConversationId, state.messages);
  }
}

function createLocalOutgoingMessage(content, clientMsgID, senderType, senderID) {
  const now = new Date().toISOString();
  return {
    id: 0,
    conversation_id: Number(state.activeConversationId || 0),
    sender_type: senderType,
    sender_id: senderID || "",
    content,
    client_msg_id: clientMsgID,
    status: "sending",
    created_at: now,
    updated_at: now,
  };
}

function normalizeMessage(message) {
  if (!message || typeof message !== "object") {
    return null;
  }
  const id = Number(message.id || 0);
  const clientMsgID = String(message.client_msg_id || "").trim();
  if (id <= 0 && !clientMsgID) {
    return null;
  }
  return {
    ...message,
    id: id > 0 ? id : 0,
    client_msg_id: clientMsgID,
    sender_type: String(message.sender_type || "").trim().toLowerCase(),
    sender_id: String(message.sender_id || "").trim(),
    status: normalizeMessageStatus(message.status),
    created_at: message.created_at || new Date().toISOString(),
    updated_at: message.updated_at || message.created_at || new Date().toISOString(),
  };
}

function normalizeMessageStatus(status) {
  const text = String(status || "")
    .trim()
    .toLowerCase();
  if (text === "sending" || text === "sent" || text === "delivered" || text === "read" || text === "failed") {
    return text;
  }
  return "";
}

function getMessageKey(message) {
  const id = Number(message?.id || 0);
  if (id > 0) {
    return `id:${id}`;
  }
  return `client:${String(message?.client_msg_id || "")}`;
}

function mergeMessageRecord(current, incoming) {
  const merged = {
    ...current,
    ...incoming,
  };
  if (!incoming.status && current?.status) {
    merged.status = current.status;
  }
  return merged;
}

function compareMessageOrder(a, b) {
  const idA = Number(a?.id || 0);
  const idB = Number(b?.id || 0);
  if (idA > 0 && idB > 0 && idA !== idB) {
    return idA - idB;
  }
  if (idA > 0 && idB <= 0) {
    return -1;
  }
  if (idA <= 0 && idB > 0) {
    return 1;
  }
  const timeA = Date.parse(a?.created_at || "");
  const timeB = Date.parse(b?.created_at || "");
  if (!Number.isNaN(timeA) && !Number.isNaN(timeB) && timeA !== timeB) {
    return timeA - timeB;
  }
  return 0;
}

function sendMessageViaWS(payload) {
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
    throw new Error("实时通道未连接，请重连后重发");
  }
  beginPending(payload.client_msg_id);
  try {
    state.ws.send(
      JSON.stringify({
        type: "message.send",
        payload,
      })
    );
  } catch (error) {
    clearPending(payload.client_msg_id, true);
    throw error instanceof Error ? error : new Error("消息发送失败");
  }
}

function beginPending(clientMsgID) {
  clearPending(clientMsgID, true);
  const timer = setTimeout(() => {
    clearPending(clientMsgID, true);
    markMessageFailedByClientMsgID(clientMsgID);
    setStatus("消息发送超时，请重发", true);
  }, ACK_TIMEOUT_MS);
  state.pendingMap[clientMsgID] = {
    timer,
  };
}

function clearPending(clientMsgID, silent = false) {
  const pending = state.pendingMap[clientMsgID];
  if (pending && pending.timer) {
    clearTimeout(pending.timer);
  }
  delete state.pendingMap[clientMsgID];
  if (!silent) {
    syncSendingStatusText();
  }
}

function resetPendingMap() {
  for (const clientMsgID of Object.keys(state.pendingMap)) {
    clearPending(clientMsgID, true);
  }
}

function syncSendingStatusText() {
  const hasPending = Object.keys(state.pendingMap).length > 0;
  if (hasPending) {
    return;
  }
  const current = String(els.statusLine?.textContent || "").trim();
  if (current === "消息发送中..." || current === "消息重发中...") {
    setStatus("消息已发送");
  }
}

function updateMessageByClientMsgID(clientMsgID, patch) {
  const key = String(clientMsgID || "").trim();
  if (!key) {
    return false;
  }
  let changed = false;
  state.messages = state.messages.map((item) => {
    if (String(item.client_msg_id || "") !== key) {
      return item;
    }
    const next = { ...item };
    if (Number(patch.id || 0) > 0) {
      next.id = Number(patch.id);
    }
    if (patch.status) {
      next.status = normalizeMessageStatus(patch.status) || next.status;
    }
    if (patch.updated_at) {
      next.updated_at = patch.updated_at;
    }
    if (patch.sender_id !== undefined) {
      next.sender_id = String(patch.sender_id || "");
    }
    if (next.id !== item.id || next.status !== item.status || next.updated_at !== item.updated_at || next.sender_id !== item.sender_id) {
      changed = true;
    }
    return next;
  });
  if (changed) {
    state.messages.sort(compareMessageOrder);
    renderMessages(state.messages);
  }
  return changed;
}

function updateMessageByID(messageID, patch) {
  const id = Number(messageID || 0);
  if (id <= 0) {
    return false;
  }
  let changed = false;
  state.messages = state.messages.map((item) => {
    if (Number(item.id || 0) !== id) {
      return item;
    }
    const next = { ...item };
    if (patch.status) {
      next.status = normalizeMessageStatus(patch.status) || next.status;
    }
    if (patch.updated_at) {
      next.updated_at = patch.updated_at;
    }
    if (next.status !== item.status || next.updated_at !== item.updated_at) {
      changed = true;
    }
    return next;
  });
  if (changed) {
    renderMessages(state.messages);
  }
  return changed;
}

function markMessageFailedByClientMsgID(clientMsgID) {
  return updateMessageByClientMsgID(clientMsgID, {
    status: "failed",
    updated_at: new Date().toISOString(),
  });
}

function handleMessageAck(payload) {
  const clientMsgID = String(payload.client_msg_id || "").trim();
  if (!clientMsgID) {
    return;
  }
  clearPending(clientMsgID);
  const status = normalizeMessageStatus(payload.status) || "sent";
  updateMessageByClientMsgID(clientMsgID, {
    id: Number(payload.message_id || 0),
    status,
    updated_at: new Date().toISOString(),
  });
}

function handleMessageNack(payload) {
  const clientMsgID = String(payload.client_msg_id || "").trim();
  if (!clientMsgID) {
    return;
  }
  clearPending(clientMsgID);
  markMessageFailedByClientMsgID(clientMsgID);
  setStatus(payload.error || "发送失败", true);
}

function handleMessageStatusEvent(payload) {
  const conversationID = Number(payload.conversation_id || 0);
  if (conversationID > 0 && String(conversationID) !== String(state.activeConversationId || "")) {
    return;
  }

  const status = normalizeMessageStatus(payload.status);
  if (!status) {
    return;
  }

  const messageID = Number(payload.message_id || 0);
  if (messageID > 0) {
    updateMessageByID(messageID, {
      status,
      updated_at: new Date().toISOString(),
    });
    return;
  }

  const upToMessageID = Number(payload.up_to_message_id || 0);
  const senderType = String(payload.sender_type || "").trim().toLowerCase();
  if (status !== "read" || upToMessageID <= 0 || !senderType) {
    return;
  }

  let changed = false;
  state.messages = state.messages.map((item) => {
    if (String(item.sender_type || "").toLowerCase() !== senderType) {
      return item;
    }
    if (Number(item.id || 0) <= 0 || Number(item.id || 0) > upToMessageID) {
      return item;
    }
    if (item.status === "read") {
      return item;
    }
    changed = true;
    return {
      ...item,
      status: "read",
      updated_at: new Date().toISOString(),
    };
  });
  if (changed) {
    renderMessages(state.messages);
  }
}

async function resendMessage(clientMsgID) {
  const key = String(clientMsgID || "").trim();
  if (!key) {
    return;
  }

  const message = state.messages.find((item) => String(item.client_msg_id || "") === key);
  if (!message || !isMineMessage(message)) {
    return;
  }

  try {
    updateMessageByClientMsgID(key, {
      status: "sending",
      updated_at: new Date().toISOString(),
    });
    sendMessageViaWS({
      sender_type: "agent",
      content: message.content || "",
      client_msg_id: key,
    });
    setStatus("消息重发中...");
  } catch (error) {
    markMessageFailedByClientMsgID(key);
    setStatus(error.message || "重发失败", true);
  }
}

function isMineMessage(message) {
  return (
    message?.sender_type === "agent" &&
    state.me &&
    String(message.sender_id || "") === String(state.me.agent_id)
  );
}

function isNearBottom(container, threshold = 72) {
  if (!container) {
    return true;
  }
  const gap = container.scrollHeight - container.scrollTop - container.clientHeight;
  return gap <= threshold;
}

function renderMessages(items, options = {}) {
  const forceScrollBottom = Boolean(options.forceScrollBottom);
  const shouldStickBottom = forceScrollBottom || isNearBottom(els.agentMessages);

  if (!Array.isArray(items) || items.length === 0) {
    els.agentMessages.innerHTML = '<div class="empty">暂无消息</div>';
    return;
  }

  els.agentMessages.innerHTML = "";
  for (const item of items) {
    const mine = isMineMessage(item);

    const row = document.createElement("article");
    row.className = `message-row ${mine ? "mine" : "other"}`;

    const bubble = document.createElement("div");
    bubble.className = "message";
    bubble.textContent = item.content || "";

    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = formatMessageMeta(item, mine);
    if (mine && item.status === "failed" && item.client_msg_id) {
      const retry = () => {
        void resendMessage(item.client_msg_id);
      };
      meta.classList.add("retryable");
      meta.setAttribute("role", "button");
      meta.tabIndex = 0;
      meta.title = "点击重发";
      meta.addEventListener("click", retry);
      meta.addEventListener("keydown", (event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          retry();
        }
      });
    }

    row.appendChild(bubble);
    row.appendChild(meta);
    els.agentMessages.appendChild(row);
  }

  if (shouldStickBottom) {
    els.agentMessages.scrollTop = els.agentMessages.scrollHeight;
  }
}

function markConversationRead(conversationID, messages) {
  if (!conversationID || !Array.isArray(messages)) {
    return;
  }
  if (!canAgentMarkConversationRead(conversationID)) {
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
    void reportConversationRead(conversationID, maxMessageID);
  }

  if (Number(state.unreadMap[conversationID] || 0) !== 0) {
    state.unreadMap[conversationID] = 0;
    renderConversations(state.conversations);
  }
  renderStats();
  renderQueueTabsMeta();
}

async function reportConversationRead(conversationID, lastReadMessageID) {
  if (!conversationID || !state.token || !Number.isFinite(lastReadMessageID) || lastReadMessageID <= 0) {
    return;
  }
  if (!canAgentMarkConversationRead(conversationID)) {
    return;
  }

  try {
    const resp = await apiRequest(`/api/chat/v1/conversations/${conversationID}/read`, {
      method: "POST",
      auth: true,
      body: {
        last_read_message_id: lastReadMessageID,
      },
    });
    if (Number(resp.updated_count || 0) > 0 && conversationID === state.activeConversationId) {
      await refreshMessages();
    }
  } catch {
    // 仅做状态上报，不打断客服操作。
  }
}

function renderStats() {
  const items = Array.isArray(state.statsConversations) ? state.statsConversations : [];
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
  if (state.actionPending.claim) {
    return;
  }
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }
  const conversationID = String(state.activeConversationId || "").trim();

  setActionPending("claim", true);
  try {
    const claimedConversation = await apiRequest(`/api/chat/v1/conversations/${state.activeConversationId}/claim`, {
      method: "POST",
      auth: true,
    });
    const meID = Number(state.me?.agent_id || 0);
    const claimedByMe = meID > 0 && Number(claimedConversation?.assigned_agent_id || 0) === meID;
    if (conversationID && claimedByMe) {
      if (state.activeConversation && String(state.activeConversation.id || "") === conversationID) {
        state.activeConversation = {
          ...state.activeConversation,
          ...claimedConversation,
          assigned_agent_id: meID,
        };
      }
      if (Array.isArray(state.conversations)) {
        state.conversations = state.conversations.map((item) =>
          String(item?.id || "") === conversationID ? { ...item, ...claimedConversation, assigned_agent_id: meID } : item
        );
      }
      if (Array.isArray(state.statsConversations)) {
        state.statsConversations = state.statsConversations.map((item) =>
          String(item?.id || "") === conversationID ? { ...item, ...claimedConversation, assigned_agent_id: meID } : item
        );
      }
      markConversationRead(conversationID, state.messages);
    }
    await refreshConversations();
    if (conversationID && conversationID === String(state.activeConversationId || "")) {
      try {
        await refreshMessages({
          conversationID,
          force: true,
        });
      } catch {
        // 认领已成功，消息刷新失败不阻断主流程。
      }
      markConversationRead(conversationID, state.messages);
    }
    setStatus("认领成功");
  } catch (error) {
    setStatus(error.message || "认领失败", true);
  } finally {
    setActionPending("claim", false);
  }
}

async function transferConversation() {
  if (state.actionPending.transfer) {
    return;
  }
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }

  const toAgentID = Number(els.transferAgentIdInput.value);
  if (!Number.isInteger(toAgentID) || toAgentID <= 0) {
    setStatus("请输入有效的目标坐席 ID", true);
    return;
  }

  setActionPending("transfer", true);
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
  } finally {
    setActionPending("transfer", false);
  }
}

async function closeConversation() {
  if (state.actionPending.close) {
    return;
  }
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }
  const capability = getActiveConversationCapability();
  if (!capability.hasConversation) {
    setStatus("会话状态未就绪，请稍后重试。", true);
    return;
  }
  if (!capability.isOpen) {
    setStatus("会话已关闭，无法继续操作。", true);
    return;
  }
  if (!capability.isMine) {
    setStatus("请先认领会话后再关闭。", true);
    return;
  }
  if (!window.confirm("确认关闭当前会话吗？关闭后将不可继续接待。")) {
    return;
  }

  setActionPending("close", true);
  try {
    await apiRequest(`/api/chat/v1/conversations/${state.activeConversationId}/close`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    setStatus("会话已关闭");
  } catch (error) {
    setStatus(error.message || "关闭失败", true);
  } finally {
    setActionPending("close", false);
  }
}

async function sendAgentMessage() {
  if (state.actionPending.send) {
    return;
  }
  if (!state.activeConversationId) {
    setStatus("请先选择会话", true);
    return;
  }
  const capability = getActiveConversationCapability();
  if (!capability.hasConversation) {
    setStatus("会话状态未就绪，请稍后重试。", true);
    return;
  }
  if (!capability.isOpen) {
    setStatus("会话已关闭，无法继续发送，请切换其他会话。", true);
    return;
  }
  if (!capability.isMine) {
    setStatus("请先认领会话后再发送消息。", true);
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
  const clientMsgID = `a_${safeUUID()}`;

  setActionPending("send", true);
  try {
    mergeMessages([createLocalOutgoingMessage(content, clientMsgID, "agent", String(state.me.agent_id))], {
      forceScrollBottom: true,
    });
    sendMessageViaWS({
      sender_type: "agent",
      content,
      client_msg_id: clientMsgID,
    });
    els.agentContentInput.value = "";
    setStatus("消息发送中...");
  } catch (error) {
    markMessageFailedByClientMsgID(clientMsgID);
    setStatus(error.message || "发送失败", true);
  } finally {
    setActionPending("send", false);
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

  const wsUrl = `${window.location.protocol === "https:" ? "wss" : "ws"}://${window.location.host}/ws/${conversationID}?access_token=${encodeURIComponent(state.token)}`;
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
      switch (data.type) {
        case "message.new":
          if (data.payload && data.payload.message) {
            const cid = String(data.payload.conversation_id || data.payload.message.conversation_id || "");
            if (!cid || cid !== state.activeConversationId) {
              return;
            }
            mergeMessages([data.payload.message]);
          }
          break;
        case "message.ack":
          handleMessageAck(data.payload || {});
          break;
        case "message.nack":
          handleMessageNack(data.payload || {});
          break;
        case "message.status":
          handleMessageStatusEvent(data.payload || {});
          break;
        case "error":
          setStatus(data.error || "WebSocket 消息异常", true);
          break;
        default:
          break;
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

function isAbortError(error) {
  return Boolean(error && typeof error === "object" && error.name === "AbortError");
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
    signal: options.signal,
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

function formatMessageTime(value) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }

  const now = new Date();
  const timeText = date.toLocaleTimeString("zh-CN", {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
  });

  const isSameYear = date.getFullYear() === now.getFullYear();
  const isSameDay =
    isSameYear && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
  if (isSameDay) {
    return timeText;
  }

  const dateText = date.toLocaleDateString("zh-CN", {
    year: isSameYear ? undefined : "numeric",
    month: "numeric",
    day: "numeric",
  });
  return `${dateText} ${timeText}`;
}

function formatMessageMeta(message, mine) {
  const timeText = formatMessageTime(message?.created_at);
  if (!mine) {
    return timeText;
  }
  const statusText = formatMessageStatus(message?.status);
  if (timeText && statusText) {
    return `${timeText} ${statusText}`;
  }
  return timeText || statusText;
}

function formatMessageStatus(status) {
  if (status === "sending") {
    return "发送中";
  }
  if (status === "failed") {
    return "发送失败";
  }
  if (status === "read") {
    return "已读";
  }
  return "";
}

window.addEventListener("beforeunload", () => {
  resetPendingMap();
  closeWebSocket();
  stopPolling();
});
