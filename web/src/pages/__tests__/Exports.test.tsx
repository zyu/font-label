import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { downloadBlob } from "../../api/client";
import ExportsPage from "../admin/Exports";

vi.mock("../../api/client", async (original) => ({
  ...(await original<typeof import("../../api/client")>()),
  downloadBlob: vi.fn(),
}));

beforeEach(() => {
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

function mount() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  for (const key of ["folders", "users", "export-jobs"]) qc.setQueryData([key], { items: [] });
  render(<QueryClientProvider client={qc}><ExportsPage /></QueryClientProvider>);
}

function blobText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

describe("JSON 导出下载交互", () => {
  it.each([
    ["仅已通过", "approved"],
    ["已审核（含打回）", "reviewed"],
    ["全部", "all"],
  ])("选择%s时点击导出会下载 .json 并提示成功", async (label, status) => {
    const contents = '[{"id":1}]';
    const fetch = vi.fn().mockResolvedValue(new Response(contents, { status: 200, headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Content-Disposition": 'attachment; filename="result.json"',
    } }));
    vi.stubGlobal("fetch", fetch);
    mount();
    fireEvent.click(screen.getByText("JSON", { exact: true }));
    fireEvent.click(screen.getByText(label, { exact: true }));
    fireEvent.click(screen.getByRole("button", { name: /导\s*出/ }));
    await waitFor(() => expect(downloadBlob).toHaveBeenCalledWith(expect.anything(), expect.stringMatching(/\.json$/)));
    const blob = vi.mocked(downloadBlob).mock.calls[0][0];
    expect(await blobText(blob)).toBe(contents);
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({ format: "json", filters: { status } });
    expect(message.success).toHaveBeenCalledWith("已开始下载");
    expect(message.info).not.toHaveBeenCalled();
  });

  it("异步响应缺少任务编号时页面显示失败提示", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response('{"data":{"async":true}}', { status: 200, headers: { "Content-Type": "application/json" } })));
    mount();
    fireEvent.click(screen.getByRole("button", { name: /导\s*出/ }));
    await waitFor(() => expect(message.error).toHaveBeenCalledWith("导出响应格式异常，请重试"));
    expect(downloadBlob).not.toHaveBeenCalled();
  });
});
