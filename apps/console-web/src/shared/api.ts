import { clearStaffToken, readStaffToken } from "./auth";

export class APIError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "APIError";
    this.status = status;
  }
}

type RequestOptions = {
  method?: string;
  auth?: boolean;
  body?: unknown;
  signal?: AbortSignal;
};

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
  };

  if (options.auth) {
    const token = readStaffToken();
    if (!token) {
      throw new APIError("未登录", 401);
    }
    headers.Authorization = `Bearer ${token}`;
  }

  const response = await fetch(path, {
    method: options.method ?? "GET",
    headers,
    body: options.body == null ? undefined : JSON.stringify(options.body),
    signal: options.signal,
  });

  const text = await response.text();
  let payload: unknown = {};
  if (text) {
    try {
      payload = JSON.parse(text) as unknown;
    } catch {
      payload = {};
    }
  }

  if (!response.ok) {
    if (response.status === 401) {
      clearStaffToken();
    }
    throw new APIError(extractErrorMessage(payload, `请求失败 (${response.status})`), response.status);
  }

  return payload as T;
}

function extractErrorMessage(payload: unknown, fallback: string): string {
  if (typeof payload === "string") {
    return payload.trim() || fallback;
  }
  if (!payload || typeof payload !== "object") {
    return fallback;
  }

  const record = payload as Record<string, unknown>;
  const nestedCandidates = [record.error, record.message, record.detail, record.reason];
  for (const candidate of nestedCandidates) {
    if (typeof candidate === "string" && candidate.trim()) {
      return candidate.trim();
    }
    if (candidate && typeof candidate === "object") {
      const nested = extractErrorMessage(candidate, "");
      if (nested) {
        return nested;
      }
    }
  }
  return fallback;
}
