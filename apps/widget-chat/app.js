const params = new URLSearchParams(window.location.search);
const ACK_TIMEOUT_MS = 5000;
const CONVERSATION_META_SYNC_MIN_INTERVAL_MS = 2000;
const WS_AUTO_RETRY_MAX = 2;
const BACKFILL_SYNC_INTERVAL_MS = 12000;
const VISITOR_TOKEN_RECORD_VERSION = 1;
const VISITOR_TOKEN_IDLE_TTL_MS = 1000 * 60 * 60 * 24 * 90;
const VISITOR_TOKEN_TOUCH_MIN_INTERVAL_MS = 1000 * 60;

const state = {
  siteID: (params.get("site_id") || "").trim(),
  title: (params.get("title") || "在线客服").trim(),
  parentOrigin: (params.get("parent_origin") || "*").trim(),
  visitorToken: "",
  visitorTokenIssuedAt: 0,
  visitorTokenLastTouchedAt: 0,
  conversationHistory: [],
  conversationID: "",
  conversationStatus: "",
  composeMode: false,
  viewMode: "history",
  messages: [],
  ws: null,
  wsConversationID: "",
  wsConnected: false,
  wsReconnectTimer: null,
  wsReconnectAttempt: 0,
  pollTimer: null,
  pollInFlight: false,
  pollAttempt: 0,
  backfillTimer: null,
  backfillInFlight: false,
  conversationMetaSyncTimer: null,
  conversationMetaSyncInFlight: false,
  conversationMetaSyncedAt: 0,
  lastReadReported: 0,
  lastReadInFlight: 0,
  pendingMap: {},
  sendPending: false,
};

const els = {
  titleText: document.getElementById("titleText"),
  statusText: document.getElementById("statusText"),
  backBtn: document.getElementById("backBtn"),
  closeBtn: document.getElementById("closeBtn"),
  historyView: document.getElementById("historyView"),
  historyList: document.getElementById("historyList"),
  historyEmpty: document.getElementById("historyEmpty"),
  startChatBtn: document.getElementById("startChatBtn"),
  chatView: document.getElementById("chatView"),
  messages: document.getElementById("messages"),
  sessionNotice: document.getElementById("sessionNotice"),
  sessionNoticeText: document.getElementById("sessionNoticeText"),
  newSessionBtn: document.getElementById("newSessionBtn"),
  sendForm: document.getElementById("sendForm"),
  contentInput: document.getElementById("contentInput"),
  sendBtn: document.getElementById("sendBtn"),
};

bootstrap().catch((error) => {
  setStatus(error.message || "初始化失败");
});

async function bootstrap() {
  els.titleText.textContent = state.title || "在线客服";
  bindEvents();

  if (!state.siteID) {
    throw new Error("缺少 site_id 参数，无法加载会话");
  }

  state.visitorToken = loadVisitorToken();
  state.conversationHistory = loadConversationHistory();

  const storedConversationID = localStorage.getItem(conversationKey()) || "";
  if (storedConversationID) {
    try {
      const conversation = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${storedConversationID}`));
      if (conversation.site_id === state.siteID) {
        upsertConversationHistoryEntry({
          conversation_id: storedConversationID,
          status: normalizeConversationStatus(conversation.status) || "open",
          assigned_agent_id: conversation.assigned_agent_id,
          updated_at: String(conversation.updated_at || conversation.created_at || "").trim() || new Date().toISOString(),
        });
      } else {
        upsertConversationHistoryEntry({
          conversation_id: storedConversationID,
          status: "open",
        });
      }
    } catch {
      upsertConversationHistoryEntry({
        conversation_id: storedConversationID,
        status: "open",
      });
    }
  } else {
    renderConversationHistory();
  }

  setViewMode("history");
  updateSessionUI();
  void refreshHistoryConversationStatuses();
  if (state.conversationHistory.length > 0) {
    setStatus("请选择历史会话，或点击“向我们发送消息”。");
  } else {
    setStatus("暂无历史会话，点击“向我们发送消息”开始。");
  }
}

function bindEvents() {
  els.closeBtn.addEventListener("click", () => {
    window.parent.postMessage({ type: "inlinechat.widget.close" }, state.parentOrigin || "*");
  });

  els.backBtn.addEventListener("click", () => {
    setViewMode("history");
  });

  els.historyList.addEventListener("click", async (event) => {
    const target = event.target;
    if (!(target instanceof Element)) {
      return;
    }
    const trigger = target.closest("[data-conversation-id]");
    if (!trigger) {
      return;
    }
    const conversationID = String(trigger.getAttribute("data-conversation-id") || "").trim();
    if (!conversationID) {
      return;
    }
    await openHistoryConversation(conversationID);
  });

  els.startChatBtn.addEventListener("click", async () => {
    await startNewConversation();
  });

  els.sendForm.addEventListener("submit", async (event) => {
    event.preventDefault();
    await sendMessage();
  });

  els.contentInput.addEventListener("keydown", async (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      await sendMessage();
    }
  });

  els.newSessionBtn.addEventListener("click", async () => {
    await startNewConversation();
  });

  updateSessionUI();
  renderConversationHistory();
  setViewMode("history");
}

function visitorTokenKey() {
  return `inlinechat.widget.visitor_token.${state.siteID}`;
}

function withVisitorToken(path) {
  const rawPath = String(path || "").trim();
  if (!rawPath) {
    return rawPath;
  }
  touchVisitorToken();
  const token = String(state.visitorToken || "").trim();
  if (!token) {
    return rawPath;
  }

  const hashIndex = rawPath.indexOf("#");
  const base = hashIndex >= 0 ? rawPath.slice(0, hashIndex) : rawPath;
  const hash = hashIndex >= 0 ? rawPath.slice(hashIndex) : "";
  if (/[?&]visitor_token=/.test(base)) {
    return rawPath;
  }
  const sep = base.includes("?") ? "&" : "?";
  return `${base}${sep}visitor_token=${encodeURIComponent(token)}${hash}`;
}

function extractErrorMessage(value, fallback) {
  if (typeof value === "string") {
    const text = value.trim();
    if (text) {
      return text;
    }
  }
  if (value && typeof value === "object") {
    const message = typeof value.message === "string" ? value.message.trim() : "";
    if (message) {
      return message;
    }
  }
  return fallback;
}

function conversationKey() {
  return `inlinechat.widget.conversation.${state.siteID}.${state.visitorToken}`;
}

function historyKey() {
  return `inlinechat.widget.conversation_history.${state.siteID}.${state.visitorToken}`;
}

function loadConversationHistory() {
  const raw = localStorage.getItem(historyKey());
  if (!raw) {
    return [];
  }
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) {
      return [];
    }
    const items = parsed.map(normalizeHistoryEntry).filter(Boolean);
    return sortHistoryEntries(items);
  } catch {
    return [];
  }
}

function saveConversationHistory() {
  const snapshot = state.conversationHistory.slice(0, 50);
  localStorage.setItem(historyKey(), JSON.stringify(snapshot));
}

function normalizeHistoryEntry(item) {
  if (!item || typeof item !== "object") {
    return null;
  }
  const conversationID = String(item.conversation_id || item.id || "")
    .trim()
    .replace(/\s+/g, "");
  if (!conversationID) {
    return null;
  }
  return {
    conversation_id: conversationID,
    status: normalizeConversationStatus(item.status) || "open",
    assigned_agent_id: normalizeAssignedAgentID(item.assigned_agent_id),
    preview: String(item.preview || "").trim().slice(0, 120),
    updated_at: String(item.updated_at || "").trim(),
  };
}

function sortHistoryEntries(items) {
  return items.slice().sort((a, b) => {
    const timeA = Date.parse(a.updated_at || "");
    const timeB = Date.parse(b.updated_at || "");
    if (!Number.isNaN(timeA) || !Number.isNaN(timeB)) {
      const safeA = Number.isNaN(timeA) ? 0 : timeA;
      const safeB = Number.isNaN(timeB) ? 0 : timeB;
      if (safeA !== safeB) {
        return safeB - safeA;
      }
    }
    return Number(b.conversation_id || 0) - Number(a.conversation_id || 0);
  });
}

function upsertConversationHistoryEntry(entry) {
  const normalized = normalizeHistoryEntry(entry);
  if (!normalized) {
    return;
  }
  const hasAssignedAgentID =
    Boolean(entry) && typeof entry === "object" && Object.prototype.hasOwnProperty.call(entry, "assigned_agent_id");

  let found = false;
  const next = state.conversationHistory.map((item) => {
    if (String(item.conversation_id || "") !== normalized.conversation_id) {
      return item;
    }
    found = true;
    return {
      ...item,
      status: normalized.status || item.status,
      assigned_agent_id: hasAssignedAgentID ? normalized.assigned_agent_id : normalizeAssignedAgentID(item.assigned_agent_id),
      preview: normalized.preview || item.preview,
      updated_at: normalized.updated_at || item.updated_at,
    };
  });
  if (!found) {
    next.push(normalized);
  }

  state.conversationHistory = sortHistoryEntries(next).slice(0, 50);
  saveConversationHistory();
  renderConversationHistory();
}

function removeConversationHistoryEntry(conversationID) {
  const targetID = String(conversationID || "").trim();
  if (!targetID) {
    return;
  }
  state.conversationHistory = state.conversationHistory.filter((item) => String(item.conversation_id || "") !== targetID);
  saveConversationHistory();
  renderConversationHistory();
}

function summarizeMessagePreview(text) {
  return String(text || "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 80);
}

function normalizeAssignedAgentID(value) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric) || numeric <= 0) {
    return 0;
  }
  return Math.floor(numeric);
}

function formatAgentID4(value) {
  const numeric = normalizeAssignedAgentID(value);
  if (numeric <= 0) {
    return "";
  }
  return String(numeric).padStart(4, "0");
}

function formatHistoryTime(value) {
  if (!value) {
    return "--";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "--";
  }
  const diff = Date.now() - date.getTime();
  if (diff >= 0) {
    const minute = 60 * 1000;
    const hour = 60 * minute;
    if (diff < hour) {
      const mins = Math.max(1, Math.floor(diff / minute));
      return `${mins}分钟前`;
    }
    if (diff < 24 * hour) {
      const hours = Math.floor(diff / hour);
      return `${hours}小时前`;
    }
  }
  return formatTime(value);
}

function renderConversationHistory() {
  if (!els.historyList || !els.historyEmpty) {
    return;
  }
  els.historyList.innerHTML = "";

  const items = sortHistoryEntries(state.conversationHistory);
  if (items.length === 0) {
    els.historyEmpty.hidden = false;
    return;
  }

  els.historyEmpty.hidden = true;

  for (const item of items) {
    const entry = document.createElement("button");
    entry.type = "button";
    entry.className = "history-item";
    if (String(item.conversation_id || "") === String(state.conversationID || "")) {
      entry.classList.add("active");
    }
    entry.setAttribute("data-conversation-id", String(item.conversation_id || ""));

    const head = document.createElement("div");
    head.className = "history-item-head";

    const title = document.createElement("div");
    title.className = "history-item-title";
    const formattedAgentID = formatAgentID4(item.assigned_agent_id);
    title.textContent = formattedAgentID ? `客服${formattedAgentID}` : "客服待接入";

    const time = document.createElement("div");
    time.className = "history-item-time";
    time.textContent = formatHistoryTime(item.updated_at);

    head.appendChild(title);
    head.appendChild(time);

    const preview = document.createElement("div");
    preview.className = "history-item-preview";
    preview.textContent = item.preview || "暂无消息，点击进入会话。";

    const meta = document.createElement("div");
    meta.className = "history-item-meta";

    const status = document.createElement("span");
    status.className = `history-status-chip ${item.status === "closed" ? "closed" : "open"}`;
    status.textContent = item.status === "closed" ? "已关闭" : "进行中";

    const id = document.createElement("span");
    id.className = "history-id";
    id.textContent = `#${item.conversation_id}`;

    meta.appendChild(status);
    meta.appendChild(id);

    entry.appendChild(head);
    entry.appendChild(preview);
    entry.appendChild(meta);
    els.historyList.appendChild(entry);
  }
}

async function refreshHistoryConversationStatuses() {
  const snapshot = state.conversationHistory.slice(0, 20);
  for (const item of snapshot) {
    const conversationID = String(item?.conversation_id || "").trim();
    if (!conversationID) {
      continue;
    }
    try {
      const conversation = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${conversationID}`));
      if (conversation.site_id !== state.siteID) {
        continue;
      }
      upsertConversationHistoryEntry({
        conversation_id: conversationID,
        status: normalizeConversationStatus(conversation.status) || item.status || "open",
        assigned_agent_id: conversation.assigned_agent_id,
        updated_at: String(conversation.updated_at || conversation.created_at || item.updated_at || "").trim() || new Date().toISOString(),
      });
    } catch {
      // 历史项同步失败时保留本地记录，避免把已关闭会话从列表中移除。
    }
  }
}

function setViewMode(mode) {
  const nextMode = mode === "chat" ? "chat" : "history";
  state.viewMode = nextMode;

  if (els.historyView) {
    els.historyView.hidden = nextMode !== "history";
  }
  if (els.chatView) {
    els.chatView.hidden = nextMode !== "chat";
  }
  if (els.backBtn) {
    els.backBtn.hidden = nextMode !== "chat";
  }
  if (nextMode === "chat") {
    void reportReadProgress();
  }
}

function shouldReportReadProgress() {
  if (state.viewMode !== "chat") {
    return false;
  }
  if (state.composeMode) {
    return false;
  }
  if (typeof document !== "undefined" && document.visibilityState && document.visibilityState !== "visible") {
    return false;
  }
  return true;
}

function normalizeConversationStatus(status) {
  const text = String(status || "")
    .trim()
    .toLowerCase();
  if (text === "open" || text === "closed") {
    return text;
  }
  return "";
}

function updateSessionUI() {
  const hasConversation = Boolean(state.conversationID);
  const isClosed = state.conversationStatus === "closed";
  const canSend = !state.sendPending && ((hasConversation && !isClosed) || state.composeMode);

  if (state.composeMode) {
    els.contentInput.placeholder = "请输入消息，发送后将创建新会话";
  } else if (!hasConversation) {
    els.contentInput.placeholder = "请点击“新建聊天”开始会话";
  } else if (isClosed) {
    els.contentInput.placeholder = "会话已关闭，请点击“新建聊天”开始新的对话";
  } else {
    els.contentInput.placeholder = "请输入消息（回车发送，Shift+回车换行）";
  }

  els.contentInput.disabled = !canSend;
  els.sendBtn.disabled = !canSend;

  if (!els.sessionNotice) {
    renderConversationHistory();
    return;
  }
  if (state.composeMode) {
    els.sessionNotice.hidden = true;
  } else if (!hasConversation) {
    els.sessionNotice.hidden = false;
    if (els.sessionNoticeText) {
      els.sessionNoticeText.textContent = "尚未进入会话，点击“新建聊天”开始对话。";
    }
  } else if (isClosed) {
    els.sessionNotice.hidden = false;
    if (els.sessionNoticeText) {
      els.sessionNoticeText.textContent = `会话 #${state.conversationID} 已关闭，无法继续发送消息。`;
    }
  } else {
    els.sessionNotice.hidden = true;
  }
  renderConversationHistory();
}

function applyConversationStatus(nextStatus, announceClosed) {
  const normalized = normalizeConversationStatus(nextStatus);
  if (!normalized) {
    return;
  }
  const prev = state.conversationStatus;
  state.conversationStatus = normalized;

  if (normalized === "closed") {
    closeWebSocket();
    stopPolling();
    resetPendingMap();
    state.sendPending = false;
    state.composeMode = false;
    if (announceClosed && prev !== "closed") {
      setStatus("会话已关闭，请点击“新建聊天”。");
    }
  }

  if (state.conversationID) {
    upsertConversationHistoryEntry({
      conversation_id: state.conversationID,
      status: normalized,
      updated_at: new Date().toISOString(),
    });
  }

  updateSessionUI();
}

function clearConversationMetaSyncTimer() {
  if (state.conversationMetaSyncTimer) {
    clearTimeout(state.conversationMetaSyncTimer);
    state.conversationMetaSyncTimer = null;
  }
}

function scheduleConversationMetaSync(force = false) {
  if (!state.conversationID) {
    return;
  }
  if (state.conversationMetaSyncInFlight || state.conversationMetaSyncTimer) {
    return;
  }

  let delay = 0;
  if (!force) {
    const elapsed = Date.now() - Number(state.conversationMetaSyncedAt || 0);
    if (elapsed < CONVERSATION_META_SYNC_MIN_INTERVAL_MS) {
      delay = CONVERSATION_META_SYNC_MIN_INTERVAL_MS - elapsed;
    }
  }

  state.conversationMetaSyncTimer = setTimeout(async () => {
    state.conversationMetaSyncTimer = null;
    if (!state.conversationID || state.conversationMetaSyncInFlight) {
      return;
    }

    state.conversationMetaSyncInFlight = true;
    try {
      await syncConversationStatus();
    } catch {
      // 会话元信息同步失败时不影响消息流，等待下次事件重试。
    } finally {
      state.conversationMetaSyncInFlight = false;
    }
  }, delay);
}

async function syncConversationStatus() {
  if (!state.conversationID) {
    return;
  }
  const conversation = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${state.conversationID}`));
  if (String(conversation.id || "") !== String(state.conversationID || "")) {
    return;
  }
  applyConversationStatus(conversation.status, true);
  upsertConversationHistoryEntry({
    conversation_id: state.conversationID,
    status: normalizeConversationStatus(conversation.status) || state.conversationStatus || "open",
    assigned_agent_id: conversation.assigned_agent_id,
    updated_at: String(conversation.updated_at || conversation.created_at || "").trim(),
  });
  state.conversationMetaSyncedAt = Date.now();
}

function loadVisitorToken() {
  const now = Date.now();
  const record = parseVisitorTokenRecord(localStorage.getItem(visitorTokenKey()), now);
  if (record && !shouldRotateVisitorToken(record, now)) {
    state.visitorTokenIssuedAt = record.issuedAt;
    state.visitorTokenLastTouchedAt = now;
    persistVisitorTokenRecord({
      token: record.token,
      issuedAt: record.issuedAt,
      lastSeenAt: now,
    });
    return record.token;
  }

  const generated = `vt_${safeUUID()}`;
  state.visitorTokenIssuedAt = now;
  state.visitorTokenLastTouchedAt = now;
  persistVisitorTokenRecord({
    token: generated,
    issuedAt: now,
    lastSeenAt: now,
  });
  return generated;
}

function touchVisitorToken(force = false) {
  const token = String(state.visitorToken || "").trim();
  if (!isValidVisitorToken(token)) {
    return;
  }
  const now = Date.now();
  if (!force && now - Number(state.visitorTokenLastTouchedAt || 0) < VISITOR_TOKEN_TOUCH_MIN_INTERVAL_MS) {
    return;
  }

  const issuedAt = toSafeEpoch(state.visitorTokenIssuedAt, now);
  state.visitorTokenIssuedAt = issuedAt;
  state.visitorTokenLastTouchedAt = now;
  persistVisitorTokenRecord({
    token,
    issuedAt,
    lastSeenAt: now,
  });
}

function shouldRotateVisitorToken(record, now) {
  if (!record || !isValidVisitorToken(record.token)) {
    return true;
  }
  return now < record.lastSeenAt || now - record.lastSeenAt > VISITOR_TOKEN_IDLE_TTL_MS;
}

function parseVisitorTokenRecord(raw, now) {
  const text = String(raw || "").trim();
  if (!text) {
    return null;
  }

  try {
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object") {
      return null;
    }
    const token = String(parsed.token || "").trim();
    if (!isValidVisitorToken(token)) {
      return null;
    }
    const issuedAt = toSafeEpoch(parsed.issued_at, now);
    const lastSeenAt = toSafeEpoch(parsed.last_seen_at, issuedAt);
    return {
      token,
      issuedAt,
      lastSeenAt,
    };
  } catch {
    if (!isValidVisitorToken(text)) {
      return null;
    }
    return {
      token: text,
      issuedAt: now,
      lastSeenAt: now,
    };
  }
}

function persistVisitorTokenRecord(record) {
  const token = String(record?.token || "").trim();
  if (!isValidVisitorToken(token)) {
    return;
  }
  const issuedAt = toSafeEpoch(record?.issuedAt, Date.now());
  const lastSeenAt = toSafeEpoch(record?.lastSeenAt, issuedAt);
  const payload = {
    version: VISITOR_TOKEN_RECORD_VERSION,
    token,
    issued_at: issuedAt,
    last_seen_at: lastSeenAt,
  };
  try {
    localStorage.setItem(visitorTokenKey(), JSON.stringify(payload));
  } catch {
    // localStorage 不可写时保持内存态，不阻断消息流程。
  }
}

function toSafeEpoch(value, fallback) {
  const num = Number(value);
  if (!Number.isFinite(num) || num <= 0) {
    return Math.max(1, Math.floor(Number(fallback) || Date.now()));
  }
  return Math.floor(num);
}

function isValidVisitorToken(token) {
  return /^vt_[a-z0-9_-]{12,}$/i.test(String(token || "").trim());
}

async function prepareConversation(forceNew, createWhenMissing = true) {
  const storedConversationID = localStorage.getItem(conversationKey()) || "";
  let conversationID = "";
  let conversationMeta = null;

  if (!forceNew && storedConversationID) {
    try {
      const conversation = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${storedConversationID}`));
      if (conversation.site_id === state.siteID) {
        conversationID = String(conversation.id);
        conversationMeta = conversation;
      } else {
        localStorage.removeItem(conversationKey());
      }
    } catch {
      conversationID = "";
      localStorage.removeItem(conversationKey());
    }
  }

  if (!conversationID && createWhenMissing) {
    const created = await apiRequest("/api/chat/v1/conversations", {
      method: "POST",
      body: {
        site_id: state.siteID,
        visitor_token: state.visitorToken,
      },
    });
    conversationID = String(created.id);
    conversationMeta = created;
  }
  if (!conversationID) {
    throw new Error("历史会话不可用，请点击“向我们发送消息”新建会话。");
  }

  state.conversationID = conversationID;
  state.conversationStatus = normalizeConversationStatus(conversationMeta?.status) || "open";
  state.composeMode = false;
  state.lastReadReported = 0;
  state.lastReadInFlight = 0;
  resetPendingMap();
  state.messages = [];
  renderMessages([]);
  localStorage.setItem(conversationKey(), conversationID);
  upsertConversationHistoryEntry({
    conversation_id: conversationID,
    status: state.conversationStatus,
    assigned_agent_id: conversationMeta?.assigned_agent_id,
    updated_at: String(conversationMeta?.updated_at || conversationMeta?.created_at || "").trim() || new Date().toISOString(),
  });
  updateSessionUI();
}

async function startNewConversation() {
  closeWebSocket();
  stopPolling();
  resetPendingMap();
  localStorage.removeItem(conversationKey());
  state.conversationID = "";
  state.conversationStatus = "";
  state.composeMode = true;
  state.messages = [];
  state.sendPending = false;
  renderMessages([]);
  updateSessionUI();
  setViewMode("chat");
  setStatus("请输入消息，发送后将创建新会话。");
  els.contentInput.focus();
}

function isWebSocketReady() {
  return (
    state.ws &&
    state.ws.readyState === WebSocket.OPEN &&
    state.wsConversationID === state.conversationID &&
    state.conversationStatus === "open"
  );
}

function waitForWebSocketReady(timeoutMs = 4000) {
  if (isWebSocketReady()) {
    return Promise.resolve();
  }
  const ws = state.ws;
  if (!ws) {
    return Promise.reject(new Error("实时通道未建立，请稍后重试。"));
  }

  return new Promise((resolve, reject) => {
    let done = false;
    const timeout = setTimeout(() => {
      cleanup();
      reject(new Error("实时通道连接超时，请稍后重试。"));
    }, Math.max(500, Number(timeoutMs) || 4000));

    function cleanup() {
      if (done) {
        return;
      }
      done = true;
      clearTimeout(timeout);
      ws.removeEventListener("open", onOpen);
      ws.removeEventListener("close", onCloseOrError);
      ws.removeEventListener("error", onCloseOrError);
    }

    function onOpen() {
      if (!isWebSocketReady()) {
        return;
      }
      cleanup();
      resolve();
    }

    function onCloseOrError() {
      cleanup();
      reject(new Error("实时通道连接失败，请稍后重试。"));
    }

    ws.addEventListener("open", onOpen);
    ws.addEventListener("close", onCloseOrError);
    ws.addEventListener("error", onCloseOrError);
    onOpen();
  });
}

async function openHistoryConversation(conversationID) {
  const targetID = String(conversationID || "").trim();
  if (!targetID) {
    return;
  }
  if (targetID === String(state.conversationID || "") && state.messages.length > 0) {
    setViewMode("chat");
    return;
  }

  closeWebSocket();
  stopPolling();
  resetPendingMap();
  state.conversationID = "";
  state.conversationStatus = "";
  state.composeMode = false;
  state.messages = [];
  state.sendPending = false;
  renderMessages([]);
  updateSessionUI();

  try {
    setStatus(`正在进入会话 #${targetID}...`);
    localStorage.setItem(conversationKey(), targetID);
    await prepareConversation(false, false);
    await refreshMessages();
    if (state.conversationStatus === "open") {
      connectWebSocket();
      setStatus(`已进入会话 #${state.conversationID}`);
    } else {
      setStatus(`会话 #${state.conversationID} 已关闭。`);
    }
    startPolling();
    setViewMode("chat");
  } catch (error) {
    const errorText = String(error?.message || "");
    if (errorText.toLowerCase().includes("closed") || errorText.includes("已关闭")) {
      upsertConversationHistoryEntry({
        conversation_id: targetID,
        status: "closed",
        updated_at: new Date().toISOString(),
      });
    }
    localStorage.removeItem(conversationKey());
    state.conversationID = "";
    state.conversationStatus = "";
    state.composeMode = false;
    state.messages = [];
    renderMessages([]);
    updateSessionUI();
    setViewMode("history");
    setStatus(error.message || `会话 #${targetID} 加载失败，请稍后重试或新建聊天。`);
  }
}

async function sendMessage() {
  const content = els.contentInput.value.trim();
  if (!content) {
    return;
  }
  if (state.sendPending) {
    return;
  }

  if (!state.conversationID && !state.composeMode) {
    setStatus("请先选择历史会话，或点击“向我们发送消息”。");
    return;
  }

  if (state.conversationStatus === "closed" && !state.composeMode) {
    updateSessionUI();
    setStatus("会话已关闭，无法继续发送，请点击“新建聊天”。");
    return;
  }

  let clientMsgID = "";
  state.sendPending = true;
  updateSessionUI();

  try {
    if (!state.conversationID) {
      setStatus("正在创建会话...");
      await prepareConversation(true, true);
      await refreshMessages();
      if (state.conversationStatus !== "open") {
        throw new Error("新会话暂不可用，请稍后重试。");
      }
      connectWebSocket();
      await waitForWebSocketReady();
      startPolling();
      setStatus("会话已创建，正在发送消息...");
    }

    if (state.conversationStatus === "closed") {
      throw new Error("会话已关闭，无法继续发送，请点击“新建聊天”。");
    }

    touchVisitorToken(true);
    clientMsgID = `cw_${safeUUID()}`;
    const payload = {
      sender_type: "visitor",
      content,
      client_msg_id: clientMsgID,
      visitor_token: state.visitorToken,
    };
    mergeMessages([createLocalOutgoingMessage(content, clientMsgID, "visitor")]);
    setStatus("消息发送中...");
    await dispatchOutgoingMessage(payload);
    els.contentInput.value = "";
  } catch (error) {
    if (clientMsgID) {
      markMessageFailedByClientMsgID(clientMsgID);
    }
    setStatus(error.message || "发送失败");
  } finally {
    state.sendPending = false;
    updateSessionUI();
    els.contentInput.focus();
  }
}

async function dispatchOutgoingMessage(payload) {
  try {
    sendMessageViaWS(payload);
    return;
  } catch {
    // WS 发送失败时降级到 HTTP，保证消息尽量不丢。
  }

  const message = await sendMessageViaHTTP(payload);
  clearPending(payload.client_msg_id, true);
  mergeMessages([message]);
  setStatus("实时通道不稳定，已切换备用通道发送");
}

async function sendMessageViaHTTP(payload) {
  if (!state.conversationID) {
    throw new Error("会话不存在，无法发送消息");
  }
  const reqBody = {
    sender_type: "visitor",
    content: String(payload?.content || ""),
    client_msg_id: String(payload?.client_msg_id || "").trim(),
    visitor_token: state.visitorToken,
  };
  if (!reqBody.client_msg_id) {
    throw new Error("client_msg_id is required");
  }

  return apiRequest(`/api/chat/v1/conversations/${state.conversationID}/messages`, {
    method: "POST",
    body: reqBody,
  });
}

async function refreshMessages() {
  if (!state.conversationID) {
    return;
  }
  const data = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${state.conversationID}/messages?limit=200`));
  const items = Array.isArray(data.items) ? data.items : [];
  mergeMessages(items);
}

function connectWebSocket() {
  if (!state.conversationID) {
    return;
  }
  if (state.conversationStatus === "closed") {
    return;
  }

  clearWsReconnectTimer();
  if (state.ws) {
    state.ws.close();
    state.ws = null;
  }

  const conversationID = state.conversationID;
  state.wsConversationID = conversationID;
  state.wsConnected = false;

  const protocol = window.location.protocol === "https:" ? "wss" : "ws";
  const wsURL = `${protocol}://${window.location.host}/ws/${conversationID}?visitor_token=${encodeURIComponent(state.visitorToken)}`;
  const ws = new WebSocket(wsURL);

  ws.addEventListener("open", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = true;
    state.wsReconnectAttempt = 0;
    stopPolling();
    void runBackfillSync(false);
    startBackfillSync(0);
    setStatus("实时连接已建立");
  });

  ws.addEventListener("message", (event) => {
    try {
      const data = JSON.parse(event.data);
      switch (data.type) {
        case "message.new":
          if (data.payload && data.payload.message) {
            mergeMessages([data.payload.message]);
            const senderType = String(data.payload.message.sender_type || "")
              .trim()
              .toLowerCase();
            if (senderType === "agent" || senderType === "system") {
              scheduleConversationMetaSync();
            }
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
        case "conversation.closed":
          applyConversationStatus("closed", true);
          break;
        case "conversation.status":
          if (data.payload && data.payload.status) {
            applyConversationStatus(data.payload.status, true);
          }
          break;
        case "error":
          setStatus(extractErrorMessage(data.error, "实时消息异常"));
          break;
        default:
          break;
      }
    } catch {
      setStatus("收到无法解析的实时消息");
    }
  });

  ws.addEventListener("close", () => {
    if (state.ws !== ws) {
      return;
    }
    state.ws = null;
    state.wsConnected = false;
    stopBackfillSync();
    if (conversationID !== state.conversationID) {
      return;
    }
    if (state.conversationStatus === "closed") {
      return;
    }
    startPolling(0);
    scheduleWsReconnect(conversationID);
    setStatus("实时连接已断开，正在自动重连");
  });

  ws.addEventListener("error", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = false;
    stopBackfillSync();
    startPolling(0);
    setStatus("实时连接异常，正在自动重连");
  });

  state.ws = ws;
}

function closeWebSocket() {
  clearWsReconnectTimer();
  clearConversationMetaSyncTimer();
  stopBackfillSync();
  if (state.ws) {
    state.ws.close();
    state.ws = null;
  }
  state.wsConnected = false;
  state.wsConversationID = "";
  state.wsReconnectAttempt = 0;
}

function clearWsReconnectTimer() {
  if (state.wsReconnectTimer) {
    clearTimeout(state.wsReconnectTimer);
    state.wsReconnectTimer = null;
  }
}

function scheduleWsReconnect(conversationID) {
  if (!conversationID || conversationID !== state.conversationID) {
    return;
  }
  if (state.conversationStatus === "closed") {
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
    if (!state.conversationID || state.conversationID !== conversationID) {
      return;
    }
    connectWebSocket();
  }, delay);
}

function startPolling(delayMs = 1200) {
  stopPolling();
  schedulePollingSync(delayMs);
}

function shouldRunPollingSync() {
  if (!state.conversationID) {
    return false;
  }
  if (state.conversationStatus === "closed") {
    return false;
  }
  return !(state.wsConnected && state.wsConversationID === state.conversationID);
}

function nextPollingDelay(success) {
  if (success) {
    state.pollAttempt = 0;
    return 2500;
  }
  state.pollAttempt = Math.min(state.pollAttempt + 1, 6);
  const base = Math.min(1000 * 2 ** state.pollAttempt, 15000);
  const jitter = Math.floor(Math.random() * 250);
  return base + jitter;
}

function schedulePollingSync(delayMs) {
  if (!shouldRunPollingSync()) {
    return;
  }
  if (state.pollTimer) {
    return;
  }
  const delay = Number.isFinite(Number(delayMs)) ? Math.max(0, Number(delayMs)) : nextPollingDelay(true);
  state.pollTimer = setTimeout(async () => {
    state.pollTimer = null;
    if (!shouldRunPollingSync()) {
      state.pollAttempt = 0;
      return;
    }
    if (state.pollInFlight) {
      schedulePollingSync(300);
      return;
    }

    state.pollInFlight = true;
    let success = true;
    try {
      await syncConversationStatus();
      if (!state.conversationID) {
        state.pollAttempt = 0;
        return;
      }
      if (state.conversationStatus === "closed") {
        await refreshMessages();
        state.pollAttempt = 0;
        return;
      }
      if (state.wsConnected && state.wsConversationID === state.conversationID) {
        state.pollAttempt = 0;
        return;
      }
      await refreshMessages();
    } catch {
      success = false;
      setStatus("消息同步失败");
    } finally {
      state.pollInFlight = false;
      if (shouldRunPollingSync()) {
        schedulePollingSync(nextPollingDelay(success));
      }
    }
  }, delay);
}

function stopPolling() {
  if (state.pollTimer) {
    clearTimeout(state.pollTimer);
    state.pollTimer = null;
  }
  state.pollInFlight = false;
  state.pollAttempt = 0;
}

function shouldRunBackfillSync() {
  if (!state.conversationID) {
    return false;
  }
  if (state.conversationStatus !== "open") {
    return false;
  }
  return state.wsConnected && state.wsConversationID === state.conversationID;
}

function startBackfillSync(delayMs = BACKFILL_SYNC_INTERVAL_MS) {
  stopBackfillSync();
  scheduleBackfillSync(delayMs);
}

function stopBackfillSync() {
  if (state.backfillTimer) {
    clearTimeout(state.backfillTimer);
    state.backfillTimer = null;
  }
  state.backfillInFlight = false;
}

function scheduleBackfillSync(delayMs = BACKFILL_SYNC_INTERVAL_MS) {
  if (!shouldRunBackfillSync()) {
    return;
  }
  if (state.backfillTimer) {
    return;
  }
  const delay = Number.isFinite(Number(delayMs)) ? Math.max(0, Number(delayMs)) : BACKFILL_SYNC_INTERVAL_MS;
  state.backfillTimer = setTimeout(async () => {
    state.backfillTimer = null;
    if (!shouldRunBackfillSync()) {
      return;
    }
    await runBackfillSync(false);
    scheduleBackfillSync(BACKFILL_SYNC_INTERVAL_MS);
  }, delay);
}

async function runBackfillSync(announceError) {
  if (!shouldRunBackfillSync() || state.backfillInFlight) {
    return;
  }
  state.backfillInFlight = true;
  try {
    await syncConversationStatus();
    await refreshMessages();
  } catch {
    if (announceError) {
      setStatus("消息补拉失败，稍后自动重试");
    }
  } finally {
    state.backfillInFlight = false;
  }
}

function mergeMessages(items) {
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
  renderMessages(state.messages);
  updateConversationHistoryFromMessages();
  void reportReadProgress();
}

function updateConversationHistoryFromMessages() {
  if (!state.conversationID) {
    return;
  }
  const latest = state.messages[state.messages.length - 1];
  const summary = summarizeMessagePreview(latest?.content || "");
  upsertConversationHistoryEntry({
    conversation_id: state.conversationID,
    status: state.conversationStatus || "open",
    preview: summary,
    updated_at: String(latest?.created_at || latest?.updated_at || "").trim() || new Date().toISOString(),
  });
}

function createLocalOutgoingMessage(content, clientMsgID, senderType) {
  const now = new Date().toISOString();
  return {
    id: 0,
    conversation_id: Number(state.conversationID || 0),
    sender_type: senderType,
    sender_id: "",
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
  if (state.conversationStatus === "closed") {
    throw new Error("会话已关闭，请点击“新建聊天”");
  }
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) {
    throw new Error("实时通道未连接，请重连后重发");
  }
  beginPending(payload);
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

function beginPending(payload) {
  const clientMsgID = String(payload?.client_msg_id || "").trim();
  if (!clientMsgID) {
    return;
  }
  clearPending(clientMsgID, true);
  state.pendingMap[clientMsgID] = {
    payload: {
      sender_type: String(payload.sender_type || "").trim().toLowerCase() || "visitor",
      content: String(payload.content || ""),
      client_msg_id: clientMsgID,
      visitor_token: String(payload.visitor_token || state.visitorToken || "").trim(),
    },
    attempt: 0,
    fallbackUsed: false,
    timer: null,
  };
  schedulePendingTimeout(clientMsgID, ACK_TIMEOUT_MS);
}

function schedulePendingTimeout(clientMsgID, delayMs) {
  const pending = state.pendingMap[clientMsgID];
  if (!pending) {
    return;
  }
  if (pending.timer) {
    clearTimeout(pending.timer);
  }
  pending.timer = setTimeout(() => {
    void handlePendingTimeout(clientMsgID);
  }, Math.max(200, Number(delayMs) || ACK_TIMEOUT_MS));
}

async function handlePendingTimeout(clientMsgID) {
  const pending = state.pendingMap[clientMsgID];
  if (!pending) {
    return;
  }

  if (isWebSocketReady() && pending.attempt < WS_AUTO_RETRY_MAX) {
    pending.attempt += 1;
    try {
      state.ws.send(
        JSON.stringify({
          type: "message.send",
          payload: pending.payload,
        })
      );
      setStatus("网络抖动，正在自动重试...");
      schedulePendingTimeout(clientMsgID, ACK_TIMEOUT_MS + pending.attempt * 1000);
      return;
    } catch {
      // 发送失败时继续走备用通道兜底。
    }
  }

  if (!pending.fallbackUsed) {
    pending.fallbackUsed = true;
    try {
      const message = await sendMessageViaHTTP(pending.payload);
      clearPending(clientMsgID, true);
      mergeMessages([message]);
      setStatus("实时通道不稳定，已切换备用通道发送");
      return;
    } catch (error) {
      clearPending(clientMsgID, true);
      markMessageFailedByClientMsgID(clientMsgID);
      setStatus(error?.message || "消息发送失败，请重发");
      return;
    }
  }

  clearPending(clientMsgID, true);
  markMessageFailedByClientMsgID(clientMsgID);
  setStatus("消息发送超时，请重发");
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
  const current = String(els.statusText?.textContent || "").trim();
  if (
    current === "消息发送中..." ||
    current === "消息重发中..." ||
    current === "网络抖动，正在自动重试..."
  ) {
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
      next.sender_id = patch.sender_id;
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
  const errorText = String(payload.error || "").toLowerCase();
  if (errorText.includes("closed") || String(payload.error || "").includes("已关闭")) {
    applyConversationStatus("closed", true);
  }
  setStatus(extractErrorMessage(payload.error, "发送失败"));
}

function handleMessageStatusEvent(payload) {
  const conversationID = Number(payload.conversation_id || 0);
  if (conversationID > 0 && String(conversationID) !== String(state.conversationID || "")) {
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
  if (!message || message.sender_type !== "visitor") {
    return;
  }

  try {
    updateMessageByClientMsgID(key, {
      status: "sending",
      updated_at: new Date().toISOString(),
    });
    touchVisitorToken(true);
    setStatus("消息重发中...");
    await dispatchOutgoingMessage({
      sender_type: "visitor",
      content: message.content || "",
      client_msg_id: key,
      visitor_token: state.visitorToken,
    });
  } catch (error) {
    markMessageFailedByClientMsgID(key);
    setStatus(error.message || "重发失败");
  }
}

async function reportReadProgress() {
  if (!shouldReportReadProgress() || !state.conversationID || !state.visitorToken || !Array.isArray(state.messages) || state.messages.length === 0) {
    return;
  }

  const maxMessageID = state.messages.reduce((max, item) => {
    const id = Number(item?.id || 0);
    return id > max ? id : max;
  }, 0);
  if (maxMessageID <= 0) {
    return;
  }
  if (maxMessageID <= Number(state.lastReadReported || 0) || maxMessageID <= Number(state.lastReadInFlight || 0)) {
    return;
  }

  state.lastReadInFlight = maxMessageID;
  try {
    const resp = await apiRequest(`/api/chat/v1/conversations/${state.conversationID}/read`, {
      method: "POST",
      body: {
        last_read_message_id: maxMessageID,
        visitor_token: state.visitorToken,
      },
    });
    state.lastReadReported = Math.max(Number(state.lastReadReported || 0), maxMessageID);
    if (Number(resp.updated_count || 0) > 0) {
      await refreshMessages();
    }
  } catch {
    // read 上报失败时不前移游标，等待下次继续上报。
  } finally {
    if (Number(state.lastReadInFlight || 0) === maxMessageID) {
      state.lastReadInFlight = 0;
    }
  }
}

function renderMessages(items) {
  if (!Array.isArray(items) || items.length === 0) {
    if (state.composeMode) {
      els.messages.innerHTML = '<div class="empty">请输入消息，发送后将创建新会话。</div>';
    } else {
      els.messages.innerHTML = '<div class="empty">你好，我是在线客服助手，请输入消息。</div>';
    }
    return;
  }

  els.messages.innerHTML = "";

  for (const item of items) {
    const isSystem = item.sender_type === "system";
    if (isSystem) {
      const row = document.createElement("article");
      row.className = "message-row system";

      const notice = document.createElement("div");
      notice.className = "system-message";
      notice.textContent = item.content || "";

      row.appendChild(notice);
      els.messages.appendChild(row);
      continue;
    }

    const isSelf = item.sender_type === "visitor";
    const row = document.createElement("article");
    row.className = `message-row ${isSelf ? "self" : "other"}`;

    const bubble = document.createElement("div");
    bubble.className = "message";
    bubble.textContent = item.content || "";

    const meta = document.createElement("div");
    meta.className = "meta";
    meta.textContent = formatMessageMeta(item, isSelf);
    if (isSelf && item.status === "failed" && item.client_msg_id) {
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
    els.messages.appendChild(row);
  }

  els.messages.scrollTop = els.messages.scrollHeight;
}

function setStatus(text) {
  els.statusText.textContent = text;
}

async function apiRequest(path, options = {}) {
  const response = await fetch(path, {
    method: options.method || "GET",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
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
    const message = extractErrorMessage(data?.error, extractErrorMessage(data?.message, `请求失败 (${response.status})`));
    throw new Error(message);
  }

  return data;
}

function safeUUID() {
  if (window.crypto && window.crypto.randomUUID) {
    return window.crypto.randomUUID();
  }
  return `${Date.now()}_${Math.random().toString(36).slice(2, 10)}`;
}

function formatTime(value) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }

  const now = new Date();
  const time = date.toLocaleTimeString("zh-CN", {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
  });

  const isSameYear = date.getFullYear() === now.getFullYear();
  const isSameDay =
    isSameYear && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();

  if (isSameDay) {
    return time;
  }
  if (isSameYear) {
    const monthDay = date.toLocaleDateString("zh-CN", {
      month: "numeric",
      day: "numeric",
    });
    return `${monthDay} ${time}`;
  }

  const fullDate = date.toLocaleDateString("zh-CN", {
    year: "numeric",
    month: "numeric",
    day: "numeric",
  });
  return `${fullDate} ${time}`;
}

function formatMessageMeta(message, isSelf) {
  const timeText = formatTime(message?.created_at);
  if (!isSelf) {
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

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible" && state.viewMode === "chat") {
    touchVisitorToken(true);
    void reportReadProgress();
    void runBackfillSync(false);
    schedulePollingSync(0);
  }
});

window.addEventListener("beforeunload", () => {
  touchVisitorToken(true);
  resetPendingMap();
  closeWebSocket();
  stopPolling();
});
