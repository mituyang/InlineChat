export function formatTime(value: string | number | undefined): string {
  if (!value) {
    return "--";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "--";
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

export function normalizeAgentID(value: number | string | undefined): string {
  return String(value ?? "")
    .replace(/\s+/g, "")
    .trim();
}

export function isValidAgentID(value: number | string | undefined): boolean {
  return /^(?!0000)\d{4}$/.test(normalizeAgentID(value));
}

export function formatAgentID(value: number | string | undefined): string {
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

export function truncateText(value: string, max = 48): string {
  const chars = Array.from(String(value ?? ""));
  if (chars.length <= max) {
    return String(value ?? "");
  }
  return `${chars.slice(0, max).join("")}...`;
}

export function countBy<T>(items: T[], predicate: (item: T) => boolean): number {
  return items.reduce((count, item) => count + (predicate(item) ? 1 : 0), 0);
}

export function countMessageChars(value: string | undefined): number {
  return Array.from(String(value ?? "")).length;
}

export function formatDurationSince(value: string | number | undefined): string {
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

  const minutes = Math.floor(diffMs / 60_000);
  if (minutes < 1) {
    return "1 分钟内";
  }
  if (minutes < 60) {
    return `${minutes} 分钟`;
  }

  const hours = Math.floor(minutes / 60);
  if (hours < 24) {
    return `${hours} 小时`;
  }

  const days = Math.floor(hours / 24);
  if (days < 30) {
    return `${days} 天`;
  }

  const months = Math.floor(days / 30);
  if (months < 12) {
    return `${months} 个月`;
  }

  const years = Math.floor(months / 12);
  return `${years} 年`;
}

export function formatMessageTime(value: string | number | undefined): string {
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
  const isSameDay = isSameYear && date.getMonth() === now.getMonth() && date.getDate() === now.getDate();
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

export function formatMessageStatus(status: string | undefined): string {
  const normalized = String(status ?? "").trim().toLowerCase();
  if (normalized === "sending") {
    return "发送中";
  }
  if (normalized === "failed") {
    return "发送失败，点击重发";
  }
  if (normalized === "read") {
    return "已读";
  }
  return "";
}

export function formatMessageMeta(
  message: { sender_type?: string; created_at?: string; status?: string },
  mine: boolean,
): string {
  const timeText = formatMessageTime(message.created_at);
  if (!mine) {
    const label = message.sender_type === "ai" ? "AI顾问" : "";
    if (label && timeText) {
      return `${label} ${timeText}`;
    }
    return label || timeText;
  }
  const statusText = formatMessageStatus(message.status);
  if (timeText && statusText) {
    return `${timeText} ${statusText}`;
  }
  return timeText || statusText;
}
