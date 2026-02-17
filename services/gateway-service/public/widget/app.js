const params = new URLSearchParams(window.location.search);

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
      const existing = await apiRequest(`/api/chat/v1/conversations/${storedConversationID}`);
      if (
        String(existing.site_id) === state.siteID &&
        String(existing.visitor_token) === state.visitorToken &&
        String(existing.status) === "open"
      ) {
        conversationID = String(existing.id);
      }
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
    if (state.ws && state.ws.readyState === WebSocket.OPEN) {
      state.ws.send(
        JSON.stringify({
          type: "message.send",
          payload: {
            content,
            client_msg_id: clientMsgID,
            visitor_token: state.visitorToken,
          },
        })
      );
    } else {
      await apiRequest(`/api/chat/v1/conversations/${state.conversationID}/messages`, {
        method: "POST",
        body: {
          sender_type: "visitor",
          content,
          client_msg_id: clientMsgID,
          visitor_token: state.visitorToken,
        },
      });
      await refreshMessages();
    }

    els.contentInput.value = "";
    setStatus("消息已发送");
  } catch (error) {
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
      if (data.type === "message.new" && data.payload && data.payload.message) {
        mergeMessages([data.payload.message]);
      }
      if (data.type === "error") {
        setStatus(data.error || "实时消息异常");
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
  const byID = new Map();

  for (const msg of state.messages) {
    if (msg && msg.id) {
      byID.set(String(msg.id), msg);
    }
  }

  for (const msg of items) {
    if (msg && msg.id) {
      byID.set(String(msg.id), msg);
    }
  }

  state.messages = Array.from(byID.values()).sort((a, b) => Number(a.id) - Number(b.id));
  renderMessages(state.messages);
}

function renderMessages(items) {
  if (!Array.isArray(items) || items.length === 0) {
    els.messages.innerHTML = '<div class="empty">你好，我是在线客服助手，请输入消息。</div>';
    return;
  }

  els.messages.innerHTML = "";

  for (const item of items) {
    const block = document.createElement("article");
    block.className = `message ${item.sender_type === "visitor" ? "self" : "other"}`;

    const content = document.createElement("div");
    content.textContent = item.content || "";

    const meta = document.createElement("div");
    meta.className = "meta";
    const sender = item.sender_type === "visitor" ? "我" : item.sender_type === "agent" ? "客服" : "系统";
    meta.textContent = `${sender} · ${formatTime(item.created_at)}`;

    block.appendChild(content);
    block.appendChild(meta);
    els.messages.appendChild(block);
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
    return value;
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

window.addEventListener("beforeunload", () => {
  closeWebSocket();
  if (state.pollTimer) {
    clearInterval(state.pollTimer);
  }
});
