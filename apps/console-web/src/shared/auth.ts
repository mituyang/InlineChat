const STORAGE_KEY = "inlinechat.staff.token";
const LEGACY_TOKEN_KEYS = ["inlinechat.agent.token", "inlinechat.admin.token"];
const THEME_STORAGE_KEY = "inlinechat.ui.theme";

export function readStaffToken(): string {
  const shared = window.localStorage.getItem(STORAGE_KEY);
  if (shared) {
    return shared;
  }

  for (const key of LEGACY_TOKEN_KEYS) {
    const legacy = window.localStorage.getItem(key);
    if (!legacy) {
      continue;
    }
    window.localStorage.setItem(STORAGE_KEY, legacy);
    window.localStorage.removeItem(key);
    return legacy;
  }

  return "";
}

export function clearStaffToken(): void {
  window.localStorage.removeItem(STORAGE_KEY);
  for (const key of LEGACY_TOKEN_KEYS) {
    window.localStorage.removeItem(key);
  }
}

export function readTheme(): "light" | "dark" {
  const stored = window.localStorage.getItem(THEME_STORAGE_KEY);
  if (stored === "light" || stored === "dark") {
    return stored;
  }
  const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
  return prefersDark ? "dark" : "light";
}

export function writeTheme(theme: "light" | "dark"): void {
  window.localStorage.setItem(THEME_STORAGE_KEY, theme);
}
