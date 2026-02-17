const STORAGE_KEYS = {
  siteId: "inlinechat.customer.site_id",
  visitorToken: "inlinechat.customer.visitor_token",
  conversationMap: "inlinechat.customer.conversation_map",
};

const state = {
  siteId: "",
  visitorToken: "",
  conversationId: "",
  ws: null,
  wsConversationId: "",
  wsConnected: false,
  wsReconnectTimer: null,
  wsReconnectAttempt: 0,
  messages: [],
  pollTimer: null,
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

function loadVisitorToken() {
  const existing = localStorage.getItem(STORAGE_KEYS.visitorToken);
  if (existing && existing.trim()) {
    return existing.trim();
  }

  const generated = `vt_${safeUUID()}`;
  localStorage.setItem(STORAGE_KEYS.visitorToken, generated);
  return generated;
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

async function startSession(forceNew) {
  const siteId = els.siteIdInput.value.trim();
  if (!siteId) {
    setStatus("请先输入 Site ID", true);
    return;
  }

  state.siteId = siteId;
  localStorage.setItem(STORAGE_KEYS.siteId, siteId);
  state.visitorToken = loadVisitorToken();
  els.visitorTokenInput.value = state.visitorToken;

  try {
    setStatus("正在准备会话...");
    let conversationId = "";

    if (!forceNew) {
      const map = getConversationMap();
      const existing = map[siteId];
      if (existing) {
        const conversation = await apiRequest(`/api/chat/v1/conversations/${existing}`);
        if (conversation.site_id === siteId && conversation.visitor_token === state.visitorToken && conversation.status === "open") {
          conversationId = String(conversation.id);
        } else {
          removeConversation(siteId);
        }
      }
    }

    if (!conversationId) {
      const created = await apiRequest("/api/chat/v1/conversations", {
        method: "POST",
        body: {
          site_id: siteId,
          visitor_token: state.visitorToken,
        },
      });
      conversationId = String(created.id);
      saveConversation(siteId, conversationId);
    }

    state.conversationId = conversationId;
    els.conversationIdInput.value = conversationId;

    await refreshMessages();
    connectWebSocket();
    startPolling();
    setStatus(`已进入会话 #${conversationId}`);
  } catch (error) {
    setStatus(error.message || "进入会话失败", true);
  }
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

  const clientMsgId = `c_${safeUUID()}`;
  els.sendBtn.disabled = true;

  try {
    const payload = {
      content,
      client_msg_id: clientMsgId,
      visitor_token: state.visitorToken,
    };

    if (state.ws && state.ws.readyState === WebSocket.OPEN) {
      state.ws.send(
        JSON.stringify({
          type: "message.send",
          payload,
        })
      );
    } else {
      await apiRequest(`/api/chat/v1/conversations/${state.conversationId}/messages`, {
        method: "POST",
        body: {
          sender_type: "visitor",
          content,
          client_msg_id: clientMsgId,
          visitor_token: state.visitorToken,
        },
      });
      await refreshMessages();
    }

    els.contentInput.value = "";
    setStatus("消息已发送");
  } catch (error) {
    setStatus(error.message || "发送失败", true);
  } finally {
    els.sendBtn.disabled = false;
    els.contentInput.focus();
  }
}

function startPolling() {
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
  }
  state.pollTimer = setInterval(() => {
    if (!state.conversationId) {
      return;
    }
    if (state.wsConnected && state.wsConversationId === state.conversationId) {
      return;
    }
    refreshMessages().catch((error) => {
      setStatus(error.message || "消息刷新失败", true);
    });
  }, 3000);
}

function stopPolling() {
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
    state.pollTimer = null;
  }
}

function connectWebSocket() {
  if (!state.conversationId) {
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

  const wsUrl = `${window.location.protocol === "https:" ? "wss" : "ws"}://${window.location.host}/ws/${conversationId}`;
  const ws = new WebSocket(wsUrl);

  ws.addEventListener("open", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = true;
    state.wsReconnectAttempt = 0;
    setWsBadge(true);
    setStatus(`WebSocket 已连接，会话 #${conversationId}`);
  });

  ws.addEventListener("message", (event) => {
    try {
      const data = JSON.parse(event.data);
      if (data.type === "message.new" && data.payload && data.payload.message) {
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
    setWsBadge(false);
    if (conversationId !== state.conversationId) {
      return;
    }
    scheduleWsReconnect(conversationId);
    setStatus("WebSocket 已断开，正在自动重连", true);
  });

  ws.addEventListener("error", () => {
    if (state.ws !== ws) {
      return;
    }
    state.wsConnected = false;
    setWsBadge(false);
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

async function refreshMessages() {
  if (!state.conversationId) {
    return;
  }

  const resp = await apiRequest(`/api/chat/v1/conversations/${state.conversationId}/messages?limit=200`);
  const items = Array.isArray(resp.items) ? resp.items : [];
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
    if (!msg || !msg.id) {
      continue;
    }
    dict.set(String(msg.id), msg);
  }

  const merged = Array.from(dict.values()).sort((a, b) => Number(a.id) - Number(b.id));
  state.messages = merged;
  renderMessages(merged);
}

function renderMessages(items) {
  if (!Array.isArray(items) || items.length === 0) {
    els.messages.innerHTML = '<div class="empty">暂无消息，发送第一条开始对话</div>';
    return;
  }

  els.messages.innerHTML = "";
  for (const item of items) {
    const box = document.createElement("article");
    box.className = `message ${item.sender_type === "visitor" ? "self" : "other"}`;

    const content = document.createElement("div");
    content.textContent = item.content || "";

    const meta = document.createElement("div");
    meta.className = "meta";
    const sender = item.sender_type === "visitor" ? "我" : item.sender_type === "agent" ? "客服" : "系统";
    meta.textContent = `${sender} · ${formatTime(item.created_at)}`;

    box.appendChild(content);
    box.appendChild(meta);
    els.messages.appendChild(box);
  }

  els.messages.scrollTop = els.messages.scrollHeight;
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
    return value;
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

window.addEventListener("beforeunload", () => {
  closeWebSocket();
  stopPolling();
});
