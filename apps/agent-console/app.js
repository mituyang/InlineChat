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
const MAX_MESSAGE_CONTENT_CHARS = 2000;
const BASE_PAGE_TITLE = document.title || "InlineChat 客服工作台";

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
  conversationRefreshPending: false,
  conversationRefreshInFlight: false,
  messageResyncInFlight: false,
  messageResyncAttempt: 0,
  unreadMap: {},
  readCursor: {},
  unreadSeq: 0,
  quickReplies: [...DEFAULT_QUICK_REPLIES],
  pendingMap: {},
  transferReminderInitialized: false,
  pendingTransferConversationIDs: [],
  actionPending: {
    select: false,
    claim: false,
    transfer: false,
    confirmTransfer: false,
    rejectTransfer: false,
    close: false,
    send: false,
  },
};

const els = {
  agentBrief: document.getElementById("agentBrief"),
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
  transferReminderBox: document.getElementById("transferReminderBox"),
  transferReminderCount: document.getElementById("transferReminderCount"),
  transferReminderList: document.getElementById("transferReminderList"),

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
  confirmTransferBtn: document.getElementById("confirmTransferBtn"),
  rejectTransferBtn: document.getElementById("rejectTransferBtn"),
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

  els.transferReminderList?.addEventListener("click", async (event) => {
    const target = event.target.closest("button.transfer-reminder-item");
    if (!target) {
      return;
    }

    const conversationID = String(target.dataset.conversationId || "").trim();
    if (!conversationID) {
      return;
    }

    try {
      await jumpToTransferReminderConversation(conversationID);
    } catch (error) {
      setStatus(error?.message || "跳转待确认转接会话失败", true);
    }
  });

  els.claimBtn?.addEventListener("click", async () => {
    await claimConversation();
  });

  els.transferBtn?.addEventListener("click", async () => {
    await transferConversation();
  });

  els.confirmTransferBtn?.addEventListener("click", async () => {
    await confirmTransferConversation();
  });

  els.rejectTransferBtn?.addEventListener("click", async () => {
    await rejectTransferConversation();
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

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
      scheduleConversationRefresh(0);
      scheduleMessageResync(0);
    }
  });

  window.addEventListener("focus", () => {
    scheduleConversationRefresh(0);
    scheduleMessageResync(0);
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
  const pendingTransferToAgentID = Number(conversation?.pending_transfer_to_agent_id || 0);
  const hasPendingTransfer = pendingTransferToAgentID > 0;
  const isPendingTransferTarget = meID > 0 && pendingTransferToAgentID === meID;

  return {
    hasConversation,
    isOpen,
    isMine,
    isUnassigned,
    hasPendingTransfer,
    isPendingTransferTarget,
  };
}

function updateConversationActionState() {
  const capability = getActiveConversationCapability();
  const hasConversation = capability.hasConversation;
  const isOpen = capability.isOpen;
  const isMine = capability.isMine;
  const isUnassigned = capability.isUnassigned;
  const hasPendingTransfer = capability.hasPendingTransfer;
  const isPendingTransferTarget = capability.isPendingTransferTarget;

  const selectPending = Boolean(state.actionPending.select);
  const canClaim =
    hasConversation && isOpen && isUnassigned && !selectPending && !state.actionPending.claim;
  const canTransfer =
    hasConversation &&
    isOpen &&
    isMine &&
    !hasPendingTransfer &&
    !selectPending &&
    !state.actionPending.transfer;
  const canConfirmTransfer =
    hasConversation &&
    isOpen &&
    isPendingTransferTarget &&
    !isMine &&
    !selectPending &&
    !state.actionPending.confirmTransfer;
  const canRejectTransfer =
    hasConversation &&
    isOpen &&
    isPendingTransferTarget &&
    !isMine &&
    !selectPending &&
    !state.actionPending.rejectTransfer;
  const showConfirmTransfer = hasConversation && isOpen && isPendingTransferTarget && !isMine;
  const showRejectTransfer = showConfirmTransfer;
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
  if (els.confirmTransferBtn) {
    els.confirmTransferBtn.disabled = !canConfirmTransfer;
    els.confirmTransferBtn.hidden = !showConfirmTransfer;
    els.confirmTransferBtn.style.display = showConfirmTransfer ? "" : "none";
  }
  if (els.rejectTransferBtn) {
    els.rejectTransferBtn.disabled = !canRejectTransfer;
    els.rejectTransferBtn.hidden = !showRejectTransfer;
    els.rejectTransferBtn.style.display = showRejectTransfer ? "" : "none";
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
    renderAgentIdentity(null);
    state.conversations = [];
    state.statsConversations = [];
    state.activeConversationId = "";
    state.activeConversation = null;
    state.selectionSeq = 0;
    state.messages = [];
    state.unreadMap = {};
    state.readCursor = {};
    state.queueShortcut = "all";
    state.transferReminderInitialized = false;
    state.pendingTransferConversationIDs = [];
    state.actionPending = {
      select: false,
      claim: false,
      transfer: false,
      confirmTransfer: false,
      rejectTransfer: false,
      close: false,
      send: false,
    };
    abortMessageRequest();
    renderConversations([]);
    renderMessages([]);
    renderStats();
    renderQueueTabsMeta();
    renderQueueShortcuts();
    renderTransferReminders([]);
    resetConversationDetail();
    updateDocumentTitleByTransferReminderCount(0);
    closeWebSocket();
    stopPolling();
    document.body.classList.add("auth-guard");
    return;
  }

  if (state.me) {
    renderAgentIdentity(state.me);
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
  renderAgentIdentity(data);
}

function renderAgentIdentity(me) {
  if (!me || typeof me !== "object") {
    if (els.agentBrief) {
      els.agentBrief.textContent = "客服ID - · 角色 -";
    }
    if (els.userBox) {
      els.userBox.textContent = "未登录";
    }
    if (els.siteFilterInput) {
      els.siteFilterInput.value = "";
    }
    return;
  }

  const agentID = formatAgentID(me.agent_id);
  const role = String(me.role || "-").trim() || "-";
  const email = String(me.email || "-").trim() || "-";
  const siteID = String(me.site_id || "-").trim() || "-";

  if (els.agentBrief) {
    els.agentBrief.textContent = `客服ID ${agentID} · 角色 ${role} · site ${siteID}`;
  }
  if (els.userBox) {
    els.userBox.textContent = email;
  }
  if (els.siteFilterInput) {
    els.siteFilterInput.value = siteID === "-" ? "" : siteID;
  }
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
  scheduleMessageResync(0);
}

function stopPolling() {
  if (state.conversationTimer) {
    clearTimeout(state.conversationTimer);
    state.conversationTimer = null;
  }
  state.conversationRefreshPending = false;
  state.conversationRefreshInFlight = false;
  if (state.messageTimer) {
    clearTimeout(state.messageTimer);
    state.messageTimer = null;
  }
  state.messageResyncInFlight = false;
  state.messageResyncAttempt = 0;
}

function scheduleConversationRefresh(delayMs = 0) {
  if (!state.token) {
    return;
  }
  const delay = Number(delayMs) > 0 ? Number(delayMs) : 0;
  if (state.conversationTimer) {
    state.conversationRefreshPending = true;
    return;
  }

  state.conversationTimer = setTimeout(async () => {
    state.conversationTimer = null;
    if (!state.token) {
      state.conversationRefreshPending = false;
      return;
    }
    if (state.conversationRefreshInFlight) {
      state.conversationRefreshPending = true;
      scheduleConversationRefresh(300);
      return;
    }

    state.conversationRefreshInFlight = true;
    try {
      await refreshConversations();
    } catch (error) {
      setStatus(error.message || "会话刷新失败", true);
    } finally {
      state.conversationRefreshInFlight = false;
      if (state.conversationRefreshPending) {
        state.conversationRefreshPending = false;
        scheduleConversationRefresh(300);
      }
    }
  }, delay);
}

function shouldResyncMessages() {
  if (!state.token || !state.activeConversationId) {
    return false;
  }
  if (String(state.activeConversation?.status || "").trim().toLowerCase() === "closed") {
    return false;
  }
  return !(state.wsConnected && state.wsConversationId === state.activeConversationId);
}

function computeMessageResyncDelay() {
  const base = Math.min(2500 * 2 ** state.messageResyncAttempt, 15000);
  const jitter = Math.floor(Math.random() * 250);
  return base + jitter;
}

function scheduleMessageResync(delayMs) {
  if (!shouldResyncMessages()) {
    return;
  }
  if (state.messageTimer) {
    return;
  }
  const delay = Number.isFinite(Number(delayMs)) ? Math.max(0, Number(delayMs)) : computeMessageResyncDelay();

  state.messageTimer = setTimeout(async () => {
    state.messageTimer = null;
    if (!shouldResyncMessages()) {
      state.messageResyncAttempt = 0;
      return;
    }
    if (state.messageResyncInFlight) {
      scheduleMessageResync(300);
      return;
    }

    state.messageResyncInFlight = true;
    try {
      await refreshMessages();
      state.messageResyncAttempt = 0;
    } catch (error) {
      state.messageResyncAttempt = Math.min(state.messageResyncAttempt + 1, 6);
      setStatus(error.message || "消息刷新失败", true);
    } finally {
      state.messageResyncInFlight = false;
      if (shouldResyncMessages()) {
        scheduleMessageResync(undefined);
      }
    }
  }, delay);
}

function cancelMessageResync() {
  if (state.messageTimer) {
    clearTimeout(state.messageTimer);
    state.messageTimer = null;
  }
  state.messageResyncInFlight = false;
  state.messageResyncAttempt = 0;
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
  syncTransferReminders(state.statsConversations);
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

function collectPendingTransferConversations(items) {
  const meID = Number(state.me?.agent_id || 0);
  if (meID <= 0 || !Array.isArray(items)) {
    return [];
  }

  return items
    .filter((item) => {
      const status = String(item?.status || "")
        .trim()
        .toLowerCase();
      if (status !== "open") {
        return false;
      }
      const targetAgentID = Number(item?.pending_transfer_to_agent_id || 0);
      return targetAgentID > 0 && targetAgentID === meID;
    })
    .sort((a, b) => {
      const ta = new Date(a.updated_at || a.created_at || 0).getTime();
      const tb = new Date(b.updated_at || b.created_at || 0).getTime();
      return tb - ta;
    });
}

function syncTransferReminders(items) {
  const pendingItems = collectPendingTransferConversations(items);
  const pendingIDs = pendingItems.map((item) => String(item?.id || "").trim()).filter(Boolean);
  const previousIDs = new Set(state.pendingTransferConversationIDs);
  const incomingNewIDs = pendingIDs.filter((id) => !previousIDs.has(id));

  state.pendingTransferConversationIDs = pendingIDs;
  renderTransferReminders(pendingItems);
  updateDocumentTitleByTransferReminderCount(pendingItems.length);

  if (state.transferReminderInitialized && incomingNewIDs.length > 0) {
    const total = pendingItems.length;
    const text = total > 1 ? `你有 ${total} 个待确认转接请求` : "你有新的待确认转接请求";
    setStatus(text);
  }
  state.transferReminderInitialized = true;
}

function renderTransferReminders(items) {
  const list = Array.isArray(items) ? items : [];
  const hasPending = list.length > 0;

  if (els.transferReminderBox) {
    els.transferReminderBox.hidden = !hasPending;
  }
  if (els.transferReminderCount) {
    els.transferReminderCount.hidden = !hasPending;
    els.transferReminderCount.textContent = hasPending ? String(list.length) : "";
  }
  if (!els.transferReminderList) {
    return;
  }

  if (!hasPending) {
    els.transferReminderList.innerHTML = "";
    return;
  }

  const activeConversationID = String(state.activeConversationId || "");
  els.transferReminderList.innerHTML = "";
  for (const item of list) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "transfer-reminder-item";
    button.dataset.conversationId = String(item?.id || "");
    if (String(item?.id || "") === activeConversationID) {
      button.classList.add("active");
    }

    const title = document.createElement("strong");
    title.textContent = `会话 #${item.id}`;

    const fromAgentID = Number(item?.assigned_agent_id || 0);
    const fromText = fromAgentID > 0 ? `来源坐席 ${formatAgentID(fromAgentID)}` : "来源坐席 -";
    const meta = document.createElement("span");
    meta.textContent = `${fromText} · ${formatDurationSince(item.updated_at || item.created_at)}前`;

    button.appendChild(title);
    button.appendChild(meta);
    els.transferReminderList.appendChild(button);
  }
}

function updateDocumentTitleByTransferReminderCount(count) {
  const total = Number(count || 0);
  if (total > 0) {
    document.title = `[待确认转接 ${total}] ${BASE_PAGE_TITLE}`;
    return;
  }
  document.title = BASE_PAGE_TITLE;
}

async function jumpToTransferReminderConversation(conversationID) {
  const id = String(conversationID || "").trim();
  if (!id) {
    return;
  }

  const rawConversation =
    state.statsConversations.find((item) => String(item?.id || "") === id) ||
    state.conversations.find((item) => String(item?.id || "") === id) ||
    null;
  if (!rawConversation) {
    setStatus("待确认转接会话不存在或已关闭", true);
    await refreshConversations();
    return;
  }

  // 跳转提醒会话时放开“仅我的会话/仅未分配”筛选，避免会话被筛掉后详情被清空。
  state.queueMode = "open";
  if (els.unassignedOnlyCheckbox) {
    els.unassignedOnlyCheckbox.checked = false;
  }
  if (els.mineOnlyCheckbox) {
    els.mineOnlyCheckbox.checked = false;
  }
  syncStatusFilterWithQueueMode();
  state.queueShortcut = resolveQueueShortcutFromFilters();
  renderQueueShortcuts();
  renderQueueTabsMeta();
  await refreshConversations();

  const conversation =
    state.conversations.find((item) => String(item?.id || "") === id) ||
    state.statsConversations.find((item) => String(item?.id || "") === id) ||
    rawConversation;
  await selectConversation(conversation);
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
    const assigned = item.assigned_agent_id ? `坐席 ${formatAgentID(item.assigned_agent_id)}` : "未分配";
    const pendingTransferTo = Number(item?.pending_transfer_to_agent_id || 0);
    const pendingText = pendingTransferTo > 0 ? ` · 待确认转接→${formatAgentID(pendingTransferTo)}` : "";
    meta1.textContent = `${assigned}${pendingText} · site=${item.site_id}`;

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
    scheduleMessageResync(300);
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

  const assigned = conversation.assigned_agent_id ? `坐席 ${formatAgentID(conversation.assigned_agent_id)}` : "未分配";
  const pendingTransferTo = Number(conversation?.pending_transfer_to_agent_id || 0);
  const pendingText = pendingTransferTo > 0 ? ` · 转接待确认→坐席 ${formatAgentID(pendingTransferTo)}` : "";
  els.activeConversationTitle.textContent = `会话 #${conversation.id}`;
  els.activeConversationMeta.textContent = `状态 ${conversation.status} · ${assigned}${pendingText} · site=${conversation.site_id}`;

  els.detailConversationId.textContent = String(conversation.id || "-");
  els.detailStatus.textContent = String(conversation.status || "-");
  els.detailSiteId.textContent = String(conversation.site_id || "-");
  els.detailAssigned.textContent =
    pendingTransferTo > 0 ? `${assigned}（待确认转接 -> ${formatAgentID(pendingTransferTo)}）` : assigned;
  els.detailUpdatedAt.textContent = formatTime(conversation.updated_at || conversation.created_at);
  els.detailWaitingDuration.textContent = formatDurationSince(conversation.updated_at || conversation.created_at);
  renderTransferReminders(collectPendingTransferConversations(state.statsConversations));
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
  renderTransferReminders(collectPendingTransferConversations(state.statsConversations));
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
  if (text === "sending" || text === "sent" || text === "read" || text === "failed") {
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

function markAllSendingMessagesFailed() {
  let changed = false;
  state.messages = state.messages.map((item) => {
    if (String(item?.status || "").trim().toLowerCase() !== "sending") {
      return item;
    }
    changed = true;
    return {
      ...item,
      status: "failed",
      updated_at: new Date().toISOString(),
    };
  });
  if (changed) {
    renderMessages(state.messages);
  }
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
  setStatus(extractErrorMessage(payload.error, "发送失败"), true);
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

function applyConversationSnapshotPatch(conversationID, patch) {
  const id = String(conversationID || "").trim();
  if (!id || !patch || typeof patch !== "object") {
    return;
  }

  if (Array.isArray(state.conversations)) {
    state.conversations = state.conversations.map((item) =>
      String(item?.id || "") === id ? { ...item, ...patch } : item
    );
  }
  if (Array.isArray(state.statsConversations)) {
    state.statsConversations = state.statsConversations.map((item) =>
      String(item?.id || "") === id ? { ...item, ...patch } : item
    );
  }

  if (state.activeConversation && String(state.activeConversation.id || "") === id) {
    updateActiveConversationHeader({
      ...state.activeConversation,
      ...patch,
    });
  } else {
    updateConversationActionState();
  }
}

function handleConversationStatusEvent(payload, fallbackStatus = "") {
  const conversationID = String(payload?.conversation_id || state.activeConversationId || "").trim();
  if (!conversationID) {
    return;
  }

  const status = String(payload?.status || fallbackStatus || "")
    .trim()
    .toLowerCase();
  if (status !== "open" && status !== "closed") {
    return;
  }

  applyConversationSnapshotPatch(conversationID, {
    status,
    updated_at: new Date().toISOString(),
  });
  renderConversations(state.conversations);
  renderStats();
  renderQueueTabsMeta();

  if (conversationID === String(state.activeConversationId || "")) {
    if (status === "closed") {
      resetPendingMap();
      markAllSendingMessagesFailed();
      closeWebSocket();
      setStatus("会话已关闭，无法继续发送消息。");
    }
  }

  scheduleConversationRefresh(0);
}

function isWebSocketReady() {
  return Boolean(
    state.ws &&
      state.ws.readyState === WebSocket.OPEN &&
      state.wsConnected &&
      state.wsConversationId === state.activeConversationId
  );
}

async function sendMessageViaHTTP(payload) {
  const conversationID = String(state.activeConversationId || "").trim();
  if (!conversationID) {
    throw new Error("会话状态未就绪，请稍后重试。");
  }
  return apiRequest(`/api/chat/v1/conversations/${conversationID}/messages`, {
    method: "POST",
    auth: true,
    body: payload,
  });
}

async function dispatchAgentMessage(payload) {
  if (isWebSocketReady()) {
    try {
      sendMessageViaWS(payload);
      return "ws";
    } catch {
      // 若发送瞬间实时通道不可用，继续降级 HTTP。
    }
  }

  const created = await sendMessageViaHTTP(payload);
  updateMessageByClientMsgID(payload.client_msg_id, {
    id: Number(created.id || 0),
    status: normalizeMessageStatus(created.status) || "sent",
    updated_at: created.updated_at || new Date().toISOString(),
    sender_id: created.sender_id,
  });
  mergeMessages([created]);
  scheduleConversationRefresh(300);
  return "http";
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
    const mode = await dispatchAgentMessage({
      sender_type: "agent",
      content: message.content || "",
      client_msg_id: key,
    });
    if (mode === "http") {
      setStatus("实时通道未连接，已通过 HTTP 重发");
    } else {
      setStatus("消息重发中...");
    }
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
    const isSystem = item.sender_type === "system";
    if (isSystem) {
      const row = document.createElement("article");
      row.className = "message-row system";

      const notice = document.createElement("div");
      notice.className = "system-message";
      notice.textContent = item.content || "";

      row.appendChild(notice);
      els.agentMessages.appendChild(row);
      continue;
    }

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

  const toAgentIDRaw = normalizeAgentID(els.transferAgentIdInput.value);
  if (!isValidAgentID(toAgentIDRaw)) {
    setStatus("请输入 4 位目标坐席 ID（不能为 0000）", true);
    return;
  }
  const toAgentID = Number.parseInt(toAgentIDRaw, 10);

  setActionPending("transfer", true);
  try {
    const conversationID = String(state.activeConversationId || "").trim();
    await apiRequest(`/api/chat/v1/conversations/${conversationID}/transfer`, {
      method: "POST",
      auth: true,
      body: {
        to_agent_id: toAgentID,
      },
    });
    await refreshConversations();
    if (conversationID && conversationID === String(state.activeConversationId || "")) {
      try {
        await refreshMessages({
          conversationID,
          force: true,
        });
      } catch {
        // 转接发起成功后，消息刷新失败不阻断主流程。
      }
    }
    setStatus(`已发起转接到坐席 ${formatAgentID(toAgentID)}，等待对方确认`);
  } catch (error) {
    setStatus(error.message || "转接失败", true);
  } finally {
    setActionPending("transfer", false);
  }
}

async function confirmTransferConversation() {
  if (state.actionPending.confirmTransfer) {
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
    setStatus("会话已关闭，无法确认转接。", true);
    return;
  }
  if (!capability.isPendingTransferTarget || capability.isMine) {
    setStatus("当前会话没有待你确认的转接。", true);
    return;
  }

  setActionPending("confirmTransfer", true);
  try {
    const conversationID = String(state.activeConversationId || "").trim();
    await apiRequest(`/api/chat/v1/conversations/${conversationID}/transfer/confirm`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    if (conversationID && conversationID === String(state.activeConversationId || "")) {
      try {
        await refreshMessages({
          conversationID,
          force: true,
        });
      } catch {
        // 确认成功后，消息刷新失败不阻断主流程。
      }
    }
    setStatus("已确认转接，当前会话已归你接待");
  } catch (error) {
    setStatus(error.message || "确认转接失败", true);
  } finally {
    setActionPending("confirmTransfer", false);
  }
}

async function rejectTransferConversation() {
  if (state.actionPending.rejectTransfer) {
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
    setStatus("会话已关闭，无法拒绝转接。", true);
    return;
  }
  if (!capability.isPendingTransferTarget || capability.isMine) {
    setStatus("当前会话没有待你处理的转接。", true);
    return;
  }

  setActionPending("rejectTransfer", true);
  try {
    const conversationID = String(state.activeConversationId || "").trim();
    await apiRequest(`/api/chat/v1/conversations/${conversationID}/transfer/reject`, {
      method: "POST",
      auth: true,
    });
    await refreshConversations();
    if (conversationID && conversationID === String(state.activeConversationId || "")) {
      try {
        await refreshMessages({
          conversationID,
          force: true,
        });
      } catch {
        // 拒绝成功后，消息刷新失败不阻断主流程。
      }
    }
    setStatus("已拒绝转接，当前会话维持原坐席接待");
  } catch (error) {
    setStatus(error.message || "拒绝转接失败", true);
  } finally {
    setActionPending("rejectTransfer", false);
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
  if (countMessageChars(content) > MAX_MESSAGE_CONTENT_CHARS) {
    setStatus(`消息过长，最多 ${MAX_MESSAGE_CONTENT_CHARS} 个字符`, true);
    return;
  }
  const clientMsgID = `a_${safeUUID()}`;

  setActionPending("send", true);
  try {
    mergeMessages([createLocalOutgoingMessage(content, clientMsgID, "agent", String(state.me.agent_id))], {
      forceScrollBottom: true,
    });
    const mode = await dispatchAgentMessage({
      sender_type: "agent",
      content,
      client_msg_id: clientMsgID,
    });
    els.agentContentInput.value = "";
    if (mode === "http") {
      setStatus("实时通道未连接，已通过 HTTP 发送");
    } else {
      setStatus("消息发送中...");
    }
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
    cancelMessageResync();
    setWsIndicator("online", `实时通道：已连接 #${conversationID}`);
    setStatus(`WebSocket 已连接，会话 #${conversationID}`);
    scheduleConversationRefresh(400);
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
            scheduleConversationRefresh(500);
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
          scheduleConversationRefresh(600);
          break;
        case "conversation.closed":
          handleConversationStatusEvent(data.payload || {}, "closed");
          break;
        case "conversation.status":
          handleConversationStatusEvent(data.payload || {});
          break;
        case "error":
          setStatus(extractErrorMessage(data.error, "WebSocket 消息异常"), true);
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
    if (String(state.activeConversation?.status || "").trim().toLowerCase() === "closed") {
      setWsIndicator("offline", `实时通道：会话已关闭 #${conversationID}`);
      return;
    }
    setWsIndicator("warn", `实时通道：重连中 #${conversationID}`);
    scheduleWsReconnect(conversationID);
    scheduleMessageResync(300);
    setStatus("WebSocket 已断开，正在自动重连", true);
  });

  ws.addEventListener("error", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = false;
    setWsIndicator("warn", `实时通道：异常 #${conversationID}`);
    scheduleMessageResync(300);
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
  cancelMessageResync();
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
  if (String(state.activeConversation?.status || "").trim().toLowerCase() === "closed") {
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

function extractErrorMessage(value, fallback = "") {
  if (value == null) {
    return fallback;
  }
  if (typeof value === "string") {
    const text = value.trim();
    return text || fallback;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  if (typeof value === "object") {
    const obj = value;
    const nested =
      extractErrorMessage(obj.message, "") ||
      extractErrorMessage(obj.error, "") ||
      extractErrorMessage(obj.detail, "") ||
      extractErrorMessage(obj.reason, "");
    if (nested) {
      return nested;
    }
  }
  return fallback;
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

    const message = extractErrorMessage(data?.error, extractErrorMessage(data?.message, `请求失败 (${response.status})`));
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

function countMessageChars(value) {
  return Array.from(String(value || "")).length;
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

function normalizeAgentID(value) {
  return String(value || "")
    .replace(/\s+/g, "")
    .trim();
}

function isValidAgentID(value) {
  return /^(?!0000)\d{4}$/.test(normalizeAgentID(value));
}

function formatAgentID(value) {
  const num = Number(value);
  if (Number.isInteger(num) && num > 0 && num <= 9999) {
    return String(num).padStart(4, "0");
  }
  const raw = normalizeAgentID(value);
  if (/^\d+$/.test(raw) && Number.parseInt(raw, 10) > 0 && Number.parseInt(raw, 10) <= 9999) {
    return String(Number.parseInt(raw, 10)).padStart(4, "0");
  }
  return raw || "-";
}

window.addEventListener("beforeunload", () => {
  resetPendingMap();
  closeWebSocket();
  stopPolling();
});
