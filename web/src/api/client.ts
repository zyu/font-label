const BASE = "/api/v1";

export class ApiError extends Error {
  code: string;
  status: number;
  fields?: Record<string, string>;
  details?: unknown;
  constructor(status: number, code: string, message: string, fields?: Record<string, string>, details?: unknown) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
    this.details = details;
  }
}

let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn;
}

export function getToken(): string {
  return localStorage.getItem("fl_token") || "";
}
export function setToken(token: string) {
  if (token) localStorage.setItem("fl_token", token);
  else localStorage.removeItem("fl_token");
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  if (init.body && !(init.body instanceof FormData)) headers.set("Content-Type", "application/json");
  const res = await fetch(BASE + path, { ...init, headers });
  const text = await res.text();
  let payload: any = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = null;
    }
  }
  if (!res.ok) {
    if (res.status === 401 && token === getToken()) {
      setToken("");
      onUnauthorized?.();
    }
    const err = payload?.error;
    throw new ApiError(res.status, err?.code || "HTTP_" + res.status, err?.message || "请求失败", err?.fields, err?.details);
  }
  return (payload?.data ?? payload) as T;
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PUT", body: body === undefined ? undefined : JSON.stringify(body) }),
  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: "PATCH", body: body === undefined ? undefined : JSON.stringify(body) }),
  del: <T>(path: string) => request<T>(path, { method: "DELETE" }),
  upload: async (form: FormData) => request<any>("/upload", { method: "POST", body: form }),
};
