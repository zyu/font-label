import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { api } from "../../api/client";
import ExportsPage from "../admin/Exports";

beforeEach(() => {
  vi.spyOn(api, "get").mockResolvedValue({ items: [] });
  vi.stubGlobal("localStorage", { getItem: () => null });
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation((element) => getComputedStyle(element));
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  Object.defineProperty(window, "matchMedia", { writable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn() })) });
  vi.spyOn(message, "success").mockImplementation(() => undefined as never);
  vi.spyOn(message, "error").mockImplementation(() => undefined as never);
  vi.spyOn(message, "info").mockImplementation(() => undefined as never);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); vi.unstubAllGlobals(); });

function mount(ossEnabled = false) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  for (const key of ["folders", "users"]) qc.setQueryData([key], { items: [] });
  qc.setQueryData(["export-jobs", 1], { items: [], oss_enabled: ossEnabled });
  render(<QueryClientProvider client={qc}><ExportsPage /></QueryClientProvider>);
}

it("取数仅提供 OSS，下载格式和旧链接不可见", () => {
  mount();
  expect(screen.queryByText("Excel (.xlsx)")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^导\s*出$/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "下载" })).not.toBeInTheDocument();
});

it("按当前筛选创建 OSS 上传任务并显示完成的对象地址", async () => {
  const objectURL = "https://fixture.oss-cn-chengdu.aliyuncs.com/font-label/exports/fixture.zip";
  vi.mocked(api.get).mockResolvedValue({
    oss_enabled: true,
    items: [{
      id: 33, format: "json", destination: "oss", status: "done", row_count: 1,
      created_at: "2026-11-20T09:00:00Z", object_url: objectURL,
      object_key: "font-label/exports/fixture.zip",
    }],
  });
  const post = vi.spyOn(api, "post").mockResolvedValue({ job_id: 33 });
  mount(true);
  fireEvent.click(screen.getByText("已审核（含打回）", { exact: true }));
  fireEvent.click(screen.getByRole("button", { name: /上传结果和图片到 OSS/ }));
  await waitFor(() => expect(post).toHaveBeenCalledWith("/exports/oss", {
    filters: { folders: [], annotators: [], status: "reviewed", date_field: "created", from: "", to: "" },
  }));
  expect(await screen.findByRole("link", { name: "对象地址" })).toHaveAttribute("href", objectURL);
});
