const STORAGE_KEYS = {
  siteId: "inlinechat.customer.site_id",
  visitorToken: "inlinechat.customer.visitor_token",
  conversationMap: "inlinechat.customer.conversation_map",
};
const ACK_TIMEOUT_MS = 5000;

const state = {
  siteId: "",
  visitorToken: "",
  widgetSession: "",
  conversationId: "",
  conversationStatus: "",
  ws: null,
  wsConversationId: "",
  wsConnected: false,
  wsReconnectTimer: null,
  wsReconnectAttempt: 0,
  messages: [],
  pollTimer: null,
  pollInFlight: false,
  pollAttempt: 0,
  lastReadReported: 0,
  lastReadInFlight: 0,
  pendingMap: {},
  sendPending: false,
};

const els = {
  siteIdInput: document.getElementById("siteIdInput"),
  visitorTokenInput: document.getElementById("visitorTokenInput"),
  conversationIdInput: document.getElementById("conversationIdInput"),
  startBtn: document.getElementById("startBtn"),
  newBtn: document.getElementById("newBtn"),
  wsBadge: document.getElementById("wsBadge"),
  messages: document.getElementById("messages"),
  sendForm: document.getElementById("sendForm"),
  contentInput: document.getElementById("contentInput"),
  sendBtn: document.getElementById("sendBtn"),
  sessionNotice: document.getElementById("sessionNotice"),
  sessionNoticeText: document.getElementById("sessionNoticeText"),
  sessionNoticeNewBtn: document.getElementById("sessionNoticeNewBtn"),
  statusLine: document.getElementById("statusLine"),
};

init();

function init() {
  const savedSiteId = localStorage.getItem(STORAGE_KEYS.siteId);
  state.siteId = savedSiteId && savedSiteId.trim() ? savedSiteId.trim() : "site_demo";
  state.visitorToken = loadVisitorToken();

  els.siteIdInput.value = state.siteId;
  els.visitorTokenInput.value = state.visitorToken;
  renderMessages([]);

  els.startBtn.addEventListener("click", () => startSession(false));
  els.newBtn.addEventListener("click", () => startSession(true));
  els.sessionNoticeNewBtn.addEventListener("click", () => startSession(true));

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

  updateSessionUI();
}

function loadVisitorToken() {
  const existing = localStorage.getItem(STORAGE_KEYS.visitorToken);
  if (existing && existing.trim()) {
    return existing.trim();
  }

  const generated = `vt_${safeUUID()}`;
  localStorage.setItem(STORAGE_KEYS.visitorToken, generated);
  return generated;
}

function withVisitorToken(path) {
  const rawPath = String(path || "").trim();
  if (!rawPath) {
    return rawPath;
  }
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

function widgetSessionHeaders() {
  if (!state.widgetSession) {
    return {};
  }
  return {
    "X-InlineChat-Widget-Session": state.widgetSession,
  };
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

function getConversationMap() {
  const raw = localStorage.getItem(STORAGE_KEYS.conversationMap);
  if (!raw) {
    return {};
  }
  try {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object") {
      return parsed;
    }
    return {};
  } catch {
    return {};
  }
}

function saveConversation(siteId, conversationId) {
  const map = getConversationMap();
  map[siteId] = String(conversationId);
  localStorage.setItem(STORAGE_KEYS.conversationMap, JSON.stringify(map));
}

function removeConversation(siteId) {
  const map = getConversationMap();
  delete map[siteId];
  localStorage.setItem(STORAGE_KEYS.conversationMap, JSON.stringify(map));
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
  const hasConversation = Boolean(state.conversationId);
  const isClosed = state.conversationStatus === "closed";
  const canSend = hasConversation && !isClosed && !state.sendPending;

  if (!hasConversation) {
    els.contentInput.placeholder = "请先进入会话，然后发送消息";
  } else if (isClosed) {
    els.contentInput.placeholder = "会话已关闭，请点击“新建聊天”开始新的对话";
  } else {
    els.contentInput.placeholder = "输入消息，回车发送（Shift+回车换行）";
  }

  els.contentInput.disabled = !canSend;
  els.sendBtn.disabled = !canSend;

  if (els.sessionNotice) {
    if (!hasConversation) {
      els.sessionNotice.hidden = false;
      if (els.sessionNoticeText) {
        els.sessionNoticeText.textContent = "尚未进入会话，点击“新建聊天”开始对话。";
      }
    } else if (isClosed) {
      els.sessionNotice.hidden = false;
      if (els.sessionNoticeText) {
        els.sessionNoticeText.textContent = `会话 #${state.conversationId} 已关闭，无法继续发送消息。`;
      }
    } else {
      els.sessionNotice.hidden = true;
    }
  }
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
    if (announceClosed && prev !== "closed") {
      setStatus("会话已关闭，请点击“新建聊天”开始新的对话。");
    }
  }

  updateSessionUI();
}

async function syncConversationStatus() {
  if (!state.conversationId) {
    return;
  }
  const conversation = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${state.conversationId}`));
  if (String(conversation.id || "") !== String(state.conversationId || "")) {
    return;
  }
  applyConversationStatus(conversation.status, true);
}

async function startSession(forceNew) {
  const siteId = els.siteIdInput.value.trim();
  if (!siteId) {
    setStatus("请先输入 Site ID", true);
    return;
  }

  state.siteId = siteId;
  localStorage.setItem(STORAGE_KEYS.siteId, siteId);
  state.visitorToken = loadVisitorToken();
  state.widgetSession = "";
  els.visitorTokenInput.value = state.visitorToken;
  closeWebSocket();
  stopPolling();
  resetPendingMap();
  state.conversationId = "";
  state.conversationStatus = "";
  state.messages = [];
  state.sendPending = false;
  els.conversationIdInput.value = "";
  renderMessages([]);
  updateSessionUI();

  try {
    setStatus("正在准备会话...");
    let conversationId = "";
    let conversationMeta = null;

    if (!forceNew) {
      const map = getConversationMap();
      const existing = map[siteId];
      if (existing) {
        const conversation = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${existing}`));
        if (conversation.site_id === siteId) {
          conversationId = String(conversation.id);
          conversationMeta = conversation;
        } else {
          removeConversation(siteId);
        }
      }
    }

    if (!conversationId) {
      state.widgetSession = await fetchWidgetSession(siteId);
      const created = await apiRequest("/api/chat/v1/conversations", {
        method: "POST",
        headers: widgetSessionHeaders(),
        body: {
          site_id: siteId,
          visitor_token: state.visitorToken,
        },
      });
      conversationId = String(created.id);
      conversationMeta = created;
      saveConversation(siteId, conversationId);
    }

    state.conversationId = conversationId;
    state.conversationStatus = normalizeConversationStatus(conversationMeta?.status) || "open";
    state.lastReadReported = 0;
    state.lastReadInFlight = 0;
    state.sendPending = false;
    state.messages = [];
    renderMessages([]);
    els.conversationIdInput.value = conversationId;
    updateSessionUI();

    await refreshMessages();
    if (state.conversationStatus === "open") {
      connectWebSocket();
    } else {
      setWsBadge(false);
    }
    startPolling();
    if (state.conversationStatus === "closed") {
      setStatus(`会话 #${conversationId} 已关闭，请点击“新建聊天”开始新的对话。`);
    } else {
      setStatus(`已进入会话 #${conversationId}`);
    }
  } catch (error) {
    setStatus(error.message || "进入会话失败", true);
  }
}

function extractWidgetSession(html) {
  const match = String(html || "").match(/window\.__INLINECHAT_WIDGET_SESSION__=("([^"\\]|\\.)*")/);
  if (!match || !match[1]) {
    return "";
  }
  try {
    return String(JSON.parse(match[1]) || "").trim();
  } catch {
    return "";
  }
}

async function fetchWidgetSession(siteId) {
  const parentOrigin = window.location.origin;
  const resp = await fetch(
    `/app/widget/?site_id=${encodeURIComponent(siteId)}&parent_origin=${encodeURIComponent(parentOrigin)}`,
    {
      headers: {
        Accept: "text/html,application/xhtml+xml",
      },
    }
  );
  const html = await resp.text();
  if (!resp.ok) {
    throw new Error(html.trim() || `获取 widget session 失败 (${resp.status})`);
  }
  const session = extractWidgetSession(html);
  if (!session) {
    throw new Error("获取 widget session 失败");
  }
  return session;
}

async function sendMessage() {
  const content = els.contentInput.value.trim();
  if (!content) {
    return;
  }
  if (!state.conversationId) {
    setStatus("请先进入会话", true);
    return;
  }
  if (state.conversationStatus === "closed") {
    updateSessionUI();
    setStatus("会话已关闭，无法继续发送，请点击“新建聊天”。");
    return;
  }
  if (state.sendPending) {
    return;
  }

  const clientMsgId = `c_${safeUUID()}`;
  state.sendPending = true;
  updateSessionUI();

  try {
    const payload = {
      sender_type: "visitor",
      content,
      client_msg_id: clientMsgId,
      visitor_token: state.visitorToken,
    };

    mergeMessages([createLocalOutgoingMessage(content, clientMsgId, "visitor")], { forceScrollBottom: true });
    sendMessageViaWS(payload);
    els.contentInput.value = "";
    setStatus("消息发送中...");
  } catch (error) {
    markMessageFailedByClientMsgID(clientMsgId);
    setStatus(error.message || "发送失败", true);
  } finally {
    state.sendPending = false;
    updateSessionUI();
    els.contentInput.focus();
  }
}

function startPolling(delayMs = 1200) {
  stopPolling();
  schedulePollingSync(delayMs);
}

function shouldRunPollingSync() {
  if (!state.conversationId) {
    return false;
  }
  if (state.conversationStatus === "closed") {
    return false;
  }
  return !(state.wsConnected && state.wsConversationId === state.conversationId);
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
      if (!state.conversationId) {
        state.pollAttempt = 0;
        return;
      }
      if (state.conversationStatus === "closed") {
        await refreshMessages();
        state.pollAttempt = 0;
        return;
      }
      if (state.wsConnected && state.wsConversationId === state.conversationId) {
        state.pollAttempt = 0;
        return;
      }
      await refreshMessages();
    } catch (error) {
      success = false;
      setStatus(error.message || "消息刷新失败", true);
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

function connectWebSocket() {
  if (!state.conversationId) {
    return;
  }
  if (state.conversationStatus === "closed") {
    setWsBadge(false);
    return;
  }

  clearWsReconnectTimer();
  if (state.ws) {
    state.ws.close();
    state.ws = null;
  }

  const conversationId = state.conversationId;
  state.wsConversationId = conversationId;
  state.wsConnected = false;

  const wsUrl = `${window.location.protocol === "https:" ? "wss" : "ws"}://${window.location.host}/ws/${conversationId}?visitor_token=${encodeURIComponent(state.visitorToken)}`;
  const ws = new WebSocket(wsUrl);

  ws.addEventListener("open", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = true;
    state.wsReconnectAttempt = 0;
    stopPolling();
    setWsBadge(true);
    setStatus(`WebSocket 已连接，会话 #${conversationId}`);
  });

  ws.addEventListener("message", (event) => {
    try {
      const data = JSON.parse(event.data);
      switch (data.type) {
        case "message.new":
          if (data.payload && data.payload.message) {
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
        case "conversation.closed":
          applyConversationStatus("closed", true);
          break;
        case "conversation.status":
          if (data.payload && data.payload.status) {
            applyConversationStatus(data.payload.status, true);
          }
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
    setWsBadge(false);
    if (conversationId !== state.conversationId) {
      return;
    }
    if (state.conversationStatus === "closed") {
      return;
    }
    startPolling(0);
    scheduleWsReconnect(conversationId);
    setStatus("WebSocket 已断开，正在自动重连", true);
  });

  ws.addEventListener("error", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = false;
    setWsBadge(false);
    startPolling(0);
    setStatus("WebSocket 连接异常，正在自动重连", true);
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
  setWsBadge(false);
}

function clearWsReconnectTimer() {
  if (state.wsReconnectTimer) {
    clearTimeout(state.wsReconnectTimer);
    state.wsReconnectTimer = null;
  }
}

function scheduleWsReconnect(conversationId) {
  if (!conversationId || conversationId !== state.conversationId) {
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
    if (!state.conversationId || state.conversationId !== conversationId) {
      return;
    }
    connectWebSocket();
  }, delay);
}

function isNearBottom(container, threshold = 72) {
  if (!container) {
    return true;
  }
  const gap = container.scrollHeight - container.scrollTop - container.clientHeight;
  return gap <= threshold;
}

function captureScrollAnchor(container) {
  if (!container) {
    return null;
  }
  const nodes = Array.from(container.querySelectorAll("[data-message-key]"));
  if (nodes.length === 0) {
    return null;
  }

  const containerTop = container.getBoundingClientRect().top;
  for (const node of nodes) {
    const key = String(node.getAttribute("data-message-key") || "").trim();
    if (!key) {
      continue;
    }
    const rect = node.getBoundingClientRect();
    if (rect.bottom > containerTop + 1) {
      return {
        key,
        offset: rect.top - containerTop,
      };
    }
  }

  const fallback = nodes[0];
  const fallbackKey = String(fallback.getAttribute("data-message-key") || "").trim();
  if (!fallbackKey) {
    return null;
  }
  return {
    key: fallbackKey,
    offset: fallback.getBoundingClientRect().top - containerTop,
  };
}

function restoreScrollAnchor(container, anchor, fallbackScrollTop) {
  if (!container) {
    return;
  }

  if (anchor && anchor.key) {
    const nodes = container.querySelectorAll("[data-message-key]");
    for (const node of nodes) {
      if (String(node.getAttribute("data-message-key") || "").trim() !== anchor.key) {
        continue;
      }
      const containerTop = container.getBoundingClientRect().top;
      const rect = node.getBoundingClientRect();
      container.scrollTop += rect.top - containerTop - Number(anchor.offset || 0);
      return;
    }
  }

  const maxScrollTop = Math.max(container.scrollHeight - container.clientHeight, 0);
  container.scrollTop = Math.min(fallbackScrollTop, maxScrollTop);
}

async function refreshMessages(options = {}) {
  if (!state.conversationId) {
    return;
  }

  const resp = await apiRequest(withVisitorToken(`/api/chat/v1/conversations/${state.conversationId}/messages?limit=200`));
  const items = Array.isArray(resp.items) ? resp.items : [];
  mergeMessages(items, options);
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

  const merged = Array.from(byKey.values()).sort(compareMessageOrder);
  state.messages = merged;
  renderMessages(state.messages, options);
  void reportReadProgress();
}

function createLocalOutgoingMessage(content, clientMsgID, senderType) {
  const now = new Date().toISOString();
  return {
    id: 0,
    conversation_id: Number(state.conversationId || 0),
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
  const status = normalizeMessageStatus(message.status);
  return {
    ...message,
    id: id > 0 ? id : 0,
    client_msg_id: clientMsgID,
    sender_type: String(message.sender_type || "").trim().toLowerCase(),
    status,
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
  setStatus(extractErrorMessage(payload.error, "发送失败"), true);
}

function handleMessageStatusEvent(payload) {
  const conversationID = Number(payload.conversation_id || 0);
  if (conversationID > 0 && String(conversationID) !== String(state.conversationId || "")) {
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
    sendMessageViaWS({
      sender_type: "visitor",
      content: message.content || "",
      client_msg_id: key,
      visitor_token: state.visitorToken,
    });
    setStatus("消息重发中...");
  } catch (error) {
    markMessageFailedByClientMsgID(key);
    setStatus(error.message || "重发失败", true);
  }
}

function shouldReportReadProgress() {
  if (typeof document !== "undefined" && document.visibilityState && document.visibilityState !== "visible") {
    return false;
  }
  return true;
}

async function reportReadProgress() {
  if (!shouldReportReadProgress() || !state.conversationId || !state.visitorToken || !Array.isArray(state.messages) || state.messages.length === 0) {
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
    const resp = await apiRequest(`/api/chat/v1/conversations/${state.conversationId}/read`, {
      method: "POST",
      body: {
        last_read_message_id: maxMessageID,
        visitor_token: state.visitorToken,
      },
    });
    state.lastReadReported = Math.max(Number(state.lastReadReported || 0), maxMessageID);
  } catch {
    // read 上报失败时不前移游标，等待下次继续上报。
  } finally {
    if (Number(state.lastReadInFlight || 0) === maxMessageID) {
      state.lastReadInFlight = 0;
    }
  }
}

function renderMessages(items, options = {}) {
  const shouldStickBottom = Boolean(options.forceScrollBottom) || isNearBottom(els.messages);
  const previousScrollTop = els.messages.scrollTop;
  const scrollAnchor = shouldStickBottom ? null : captureScrollAnchor(els.messages);
  if (!Array.isArray(items) || items.length === 0) {
    els.messages.innerHTML = '<div class="empty">暂无消息，发送第一条开始对话</div>';
    return;
  }

  els.messages.innerHTML = "";
  for (const item of items) {
    const isSystem = item.sender_type === "system";
    if (isSystem) {
      const box = document.createElement("article");
      box.className = "message system";

      const content = document.createElement("div");
      content.textContent = item.content || "";

      box.appendChild(content);
      els.messages.appendChild(box);
      continue;
    }

    const box = document.createElement("article");
    box.className = `message ${item.sender_type === "visitor" ? "self" : "other"}`;
    box.setAttribute("data-message-key", getMessageKey(item));

    const content = document.createElement("div");
    content.textContent = item.content || "";

    const meta = document.createElement("div");
    meta.className = "meta";
    const isSelf = item.sender_type === "visitor";
    const sender = isSelf ? "我" : item.sender_type === "agent" ? "客服" : "系统";
    const statusText = formatMessageStatus(item.status);
    meta.textContent = statusText
      ? `${sender} · ${formatTime(item.created_at)} · ${statusText}`
      : `${sender} · ${formatTime(item.created_at)}`;
    if (isSelf && item.status === "failed" && item.client_msg_id) {
      meta.style.cursor = "pointer";
      meta.title = "点击重发";
      meta.addEventListener("click", () => {
        void resendMessage(item.client_msg_id);
      });
    }

    box.appendChild(content);
    box.appendChild(meta);
    els.messages.appendChild(box);
  }

  if (shouldStickBottom) {
    els.messages.scrollTop = els.messages.scrollHeight;
  } else {
    restoreScrollAnchor(els.messages, scrollAnchor, previousScrollTop);
  }
}

function setWsBadge(online) {
  if (online) {
    els.wsBadge.textContent = "WebSocket 已连接";
    els.wsBadge.classList.remove("offline");
    els.wsBadge.classList.add("online");
  } else {
    els.wsBadge.textContent = "WebSocket 未连接";
    els.wsBadge.classList.remove("online");
    els.wsBadge.classList.add("offline");
  }
}

function setStatus(text, isError = false) {
  els.statusLine.textContent = text;
  els.statusLine.classList.toggle("error", Boolean(isError));
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
    return value;
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

function formatMessageStatus(status) {
  if (status === "sending") {
    return "发送中";
  }
  if (status === "failed") {
    return "发送失败";
  }
  if (status === "sent") {
    return "已发送";
  }
  if (status === "read") {
    return "已读";
  }
  return "";
}

document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") {
    void reportReadProgress();
    schedulePollingSync(0);
  }
});

window.addEventListener("beforeunload", () => {
  resetPendingMap();
  closeWebSocket();
  stopPolling();
});
