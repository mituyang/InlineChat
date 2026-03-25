import { readTheme, writeTheme } from "./auth";

export function applyTheme(theme: "light" | "dark"): void {
  document.documentElement.dataset.theme = theme;
  writeTheme(theme);
}

export function initTheme(): "light" | "dark" {
  const theme = readTheme();
  document.documentElement.dataset.theme = theme;
  return theme;
}
