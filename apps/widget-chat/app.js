const params = new URLSearchParams(window.location.search);
const ACK_TIMEOUT_MS = 5000;

const state = {
  siteID: (params.get("site_id") || "").trim(),
  title: (params.get("title") || "在线客服").trim(),
  parentOrigin: (params.get("parent_origin") || "*").trim(),
  visitorToken: "",
  conversationID: "",
  messages: [],
  ws: null,
  wsConversationID: "",
  wsConnected: false,
  wsReconnectTimer: null,
  wsReconnectAttempt: 0,
  pollTimer: null,
  lastReadReported: 0,
  lastReadInFlight: 0,
  pendingMap: {},
};

const els = {
  titleText: document.getElementById("titleText"),
  statusText: document.getElementById("statusText"),
  closeBtn: document.getElementById("closeBtn"),
  messages: document.getElementById("messages"),
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
    throw new Error("缺少 site_id 参数，无法创建会话");
  }

  state.visitorToken = loadVisitorToken();
  await prepareConversation();
  await refreshMessages();
  connectWebSocket();
  startPolling();
  setStatus("已连接客服");
}

function bindEvents() {
  els.closeBtn.addEventListener("click", () => {
    window.parent.postMessage({ type: "inlinechat.widget.close" }, state.parentOrigin || "*");
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
}

function visitorTokenKey() {
  return `inlinechat.widget.visitor_token.${state.siteID}`;
}

function conversationKey() {
  return `inlinechat.widget.conversation.${state.siteID}.${state.visitorToken}`;
}

function loadVisitorToken() {
  const key = visitorTokenKey();
  const saved = localStorage.getItem(key);
  if (saved && saved.trim()) {
    return saved.trim();
  }

  const generated = `vt_${safeUUID()}`;
  localStorage.setItem(key, generated);
  return generated;
}

async function prepareConversation() {
  const storedConversationID = localStorage.getItem(conversationKey()) || "";
  let conversationID = "";

  if (storedConversationID) {
    try {
      await apiRequest(`/api/chat/v1/conversations/${storedConversationID}/messages?limit=1`);
      conversationID = String(storedConversationID);
    } catch {
      conversationID = "";
    }
  }

  if (!conversationID) {
    const created = await apiRequest("/api/chat/v1/conversations", {
      method: "POST",
      body: {
        site_id: state.siteID,
        visitor_token: state.visitorToken,
      },
    });
    conversationID = String(created.id);
  }

  state.conversationID = conversationID;
  state.lastReadReported = 0;
  state.lastReadInFlight = 0;
  resetPendingMap();
  state.messages = [];
  renderMessages([]);
  localStorage.setItem(conversationKey(), conversationID);
}

async function sendMessage() {
  const content = els.contentInput.value.trim();
  if (!content) {
    return;
  }
  if (!state.conversationID) {
    setStatus("会话未初始化，请稍后");
    return;
  }

  const clientMsgID = `cw_${safeUUID()}`;
  els.sendBtn.disabled = true;

  try {
    mergeMessages([createLocalOutgoingMessage(content, clientMsgID, "visitor")]);
    sendMessageViaWS({
      sender_type: "visitor",
      content,
      client_msg_id: clientMsgID,
      visitor_token: state.visitorToken,
    });
    els.contentInput.value = "";
    setStatus("消息发送中...");
  } catch (error) {
    markMessageFailedByClientMsgID(clientMsgID);
    setStatus(error.message || "发送失败");
  } finally {
    els.sendBtn.disabled = false;
    els.contentInput.focus();
  }
}

async function refreshMessages() {
  if (!state.conversationID) {
    return;
  }
  const data = await apiRequest(`/api/chat/v1/conversations/${state.conversationID}/messages?limit=200`);
  const items = Array.isArray(data.items) ? data.items : [];
  mergeMessages(items);
}

function connectWebSocket() {
  if (!state.conversationID) {
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
  const wsURL = `${protocol}://${window.location.host}/ws/${conversationID}`;
  const ws = new WebSocket(wsURL);

  ws.addEventListener("open", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = true;
    state.wsReconnectAttempt = 0;
    setStatus("实时连接已建立");
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
        case "error":
          setStatus(data.error || "实时消息异常");
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
    if (conversationID !== state.conversationID) {
      return;
    }
    scheduleWsReconnect(conversationID);
    setStatus("实时连接已断开，正在自动重连");
  });

  ws.addEventListener("error", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = false;
    setStatus("实时连接异常，正在自动重连");
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

function startPolling() {
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
  }

  state.pollTimer = setInterval(() => {
    if (!state.conversationID) {
      return;
    }
    if (state.wsConnected && state.wsConversationID === state.conversationID) {
      return;
    }
    refreshMessages().catch(() => {
      setStatus("消息同步失败");
    });
  }, 3000);
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
  void reportReadProgress();
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
    setStatus("消息发送超时，请重发");
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
  const current = String(els.statusText?.textContent || "").trim();
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
  setStatus(payload.error || "发送失败");
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
    sendMessageViaWS({
      sender_type: "visitor",
      content: message.content || "",
      client_msg_id: key,
      visitor_token: state.visitorToken,
    });
    setStatus("消息重发中...");
  } catch (error) {
    markMessageFailedByClientMsgID(key);
    setStatus(error.message || "重发失败");
  }
}

async function reportReadProgress() {
  if (!state.conversationID || !state.visitorToken || !Array.isArray(state.messages) || state.messages.length === 0) {
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
    els.messages.innerHTML = '<div class="empty">你好，我是在线客服助手，请输入消息。</div>';
    return;
  }

  els.messages.innerHTML = "";

  for (const item of items) {
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
    const message = data.error || data?.error?.message || `请求失败 (${response.status})`;
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

window.addEventListener("beforeunload", () => {
  resetPendingMap();
  closeWebSocket();
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
  }
});
