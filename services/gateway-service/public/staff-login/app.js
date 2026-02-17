const STAFF_TOKEN_KEY = "inlinechat.staff.token";
const LEGACY_TOKEN_KEYS = ["inlinechat.agent.token", "inlinechat.admin.token"];
const ROUTES = {
  login: "/app/staff-login/",
  agent: "/app/agent/",
  admin: "/app/admin/",
};

const state = {
  token: "",
};

const els = {
  loginForm: document.getElementById("loginForm"),
  emailInput: document.getElementById("emailInput"),
  passwordInput: document.getElementById("passwordInput"),
  loginBtn: document.getElementById("loginBtn"),
  statusLine: document.getElementById("statusLine"),
};

init();

async function init() {
  bindEvents();

  const savedToken = readStaffToken();
  if (!savedToken) {
    return;
  }

  state.token = savedToken;
  setStatus("检测到登录态，正在验证并跳转...");
  try {
    const me = await fetchMe();
    redirectAfterLogin(me.role, me.email);
  } catch {
    clearToken();
    state.token = "";
    setStatus("登录态已失效，请重新登录", true);
  }
}

function bindEvents() {
  els.loginForm?.addEventListener("submit", async (event) => {
    event.preventDefault();
    await login();
  });
}

async function login() {
  const email = (els.emailInput.value || "").trim();
  const password = els.passwordInput.value || "";
  if (!email || !password) {
    setStatus("请输入邮箱和密码", true);
    return;
  }

  els.loginBtn.disabled = true;
  setStatus("登录中...");

  try {
    const auth = await requestJSON("/api/auth/v1/auth/login", {
      method: "POST",
      body: {
        email,
        password,
      },
    });

    if (!auth.token) {
      throw new Error("登录成功但未返回 token");
    }

    state.token = auth.token;
    saveToken(auth.token);

    const me = await fetchMe();
    redirectAfterLogin(me.role, me.email);
  } catch (error) {
    clearToken();
    state.token = "";
    setStatus(error.message || "登录失败", true);
  } finally {
    els.loginBtn.disabled = false;
  }
}

async function fetchMe() {
  return requestJSON("/api/auth/v1/auth/me", {
    auth: true,
  });
}

function redirectAfterLogin(role, email) {
  const next = normalizedNextRoute();
  const target = resolveTarget(role, next);

  if (!target) {
    clearToken();
    state.token = "";
    setStatus("当前账号角色无权限访问工作台", true);
    return;
  }

  setStatus(`登录成功：${email} (${role})，正在跳转...`);
  window.location.replace(target);
}

function resolveTarget(role, next) {
  const normalizedRole = String(role || "").trim();
  if (next && canVisit(next, normalizedRole)) {
    return next;
  }

  if (normalizedRole === "agent") {
    return ROUTES.agent;
  }
  if (normalizedRole === "admin" || normalizedRole === "super_admin") {
    return ROUTES.admin;
  }
  return "";
}

function canVisit(path, role) {
  if (path === ROUTES.agent) {
    return role === "agent" || role === "admin" || role === "super_admin";
  }
  if (path === ROUTES.admin) {
    return role === "admin" || role === "super_admin";
  }
  return false;
}

function normalizedNextRoute() {
  const rawNext = new URLSearchParams(window.location.search).get("next") || "";
  if (!rawNext) {
    return "";
  }

  try {
    const parsed = new URL(rawNext, window.location.origin);
    if (parsed.origin !== window.location.origin) {
      return "";
    }
    const path = `${parsed.pathname}${parsed.search}${parsed.hash}`;
    if (path.startsWith(ROUTES.agent)) {
      return ROUTES.agent;
    }
    if (path.startsWith(ROUTES.admin)) {
      return ROUTES.admin;
    }
  } catch {
    return "";
  }

  return "";
}

function readStaffToken() {
  const token = localStorage.getItem(STAFF_TOKEN_KEY);
  if (token) {
    return token;
  }

  for (const key of LEGACY_TOKEN_KEYS) {
    const legacy = localStorage.getItem(key);
    if (!legacy) {
      continue;
    }
    saveToken(legacy);
    return legacy;
  }

  return "";
}

function saveToken(token) {
  localStorage.setItem(STAFF_TOKEN_KEY, token);
  for (const key of LEGACY_TOKEN_KEYS) {
    localStorage.removeItem(key);
  }
}

function clearToken() {
  localStorage.removeItem(STAFF_TOKEN_KEY);
  for (const key of LEGACY_TOKEN_KEYS) {
    localStorage.removeItem(key);
  }
}

function setStatus(text, isError = false) {
  els.statusLine.textContent = text;
  els.statusLine.classList.toggle("error", Boolean(isError));
}

async function requestJSON(path, options = {}) {
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
    const message = data.error || data?.error?.message || `请求失败 (${response.status})`;
    throw new Error(message);
  }

  return data;
}
