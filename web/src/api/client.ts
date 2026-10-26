const BASE = "/api/v1";

type ExportFileResult = { blob: Blob; jobId?: never } | { jobId: number; blob?: never };

export class ApiError extends Error {
  code: string;
  status: number;
  fields?: Record<string, string>;
  constructor(status: number, code: string, message: string, fields?: Record<string, string>) {
    super(message);
    this.status = status;
    this.code = code;
    this.fields = fields;
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
    if (res.status === 401) {
      setToken("");
      onUnauthorized?.();
    }
    const err = payload?.error;
    throw new ApiError(res.status, err?.code || "HTTP_" + res.status, err?.message || "请求失败", err?.fields);
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
  /** Download an export; returns either a Blob or a job descriptor. */
  exportFile: async (body: unknown): Promise<ExportFileResult> => {
    const headers = new Headers({ "Content-Type": "application/json" });
    const token = getToken();
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const res = await fetch(BASE + "/exports", { method: "POST", headers, body: JSON.stringify(body) });
    if (!res.ok) {
      const payload = await res.json().catch(() => null);
      throw new ApiError(res.status, payload?.error?.code || "HTTP", payload?.error?.message || "导出失败");
    }
    const attachment = /^\s*attachment(?:\s*;|\s*$)/i.test(res.headers.get("Content-Disposition") || "");
    const ct = (res.headers.get("Content-Type") || "").toLowerCase();
    // JSON exports and task descriptors share a MIME type. An attachment is a
    // file; existing servers return HTTP 200 with async=true for queued jobs.
    if (!attachment && (res.status === 202 || ct.includes("application/json"))) {
      const payload = await res.json().catch(() => null);
      const jobId = payload?.data?.job_id;
      if ((res.status === 202 || payload?.data?.async === true) && Number.isSafeInteger(jobId) && jobId > 0) {
        return { jobId };
      }
      throw new ApiError(res.status, "INVALID_EXPORT_RESPONSE", "导出响应格式异常，请重试");
    }
    return { blob: await res.blob() };
  },
};

export function downloadBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

export async function downloadExportByID(id: number, name: string) {
  const token = getToken();
  const res = await fetch(`${BASE}/exports/${id}/download`, { headers: { Authorization: `Bearer ${token}` } });
  if (!res.ok) throw new Error("下载失败");
  downloadBlob(await res.blob(), name);
}
