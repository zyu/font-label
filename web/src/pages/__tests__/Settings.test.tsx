import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { message } from "antd";
import { api, ApiError } from "../../api/client";
import SettingsPage from "../admin/Settings";

const { reload } = vi.hoisted(() => ({ reload: vi.fn() }));
vi.mock("../../auth", () => ({ useAuth: () => ({ reload }) }));

const settings = { comment_templates: ["原有评语"], exception_reasons: [{ key: "blur", label: "图片模糊" }] };
const editors = [
  { title: "常用评语（JSON 数组）", key: "comment_templates", valid: '["撇捺左右空白大致匀。"]' },
  { title: "异常原因（JSON 数组，key + label）", key: "exception_reasons", valid: '[{"key":"blur","label":"严重模糊"}]' },
];

beforeEach(() => {
  reload.mockResolvedValue(undefined);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  Object.defineProperty(window, "matchMedia", { writable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn() })) });
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation((element) => getComputedStyle(element));
  vi.spyOn(api, "get").mockResolvedValue({ settings });
  vi.spyOn(api, "put").mockResolvedValue({});
  vi.spyOn(message, "success").mockImplementation(() => undefined as never);
  vi.spyOn(message, "error").mockImplementation(() => undefined as never);
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.clearAllMocks(); vi.unstubAllGlobals(); });

function mount(title: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(["rubric"], { dims: [], grade_bands: [], dim_comment_min: 10, dim_comment_max: 50 });
  qc.setQueryData(["settings"], { settings });
  render(<QueryClientProvider client={qc}><SettingsPage /></QueryClientProvider>);
  return within(screen.getByText(title).closest(".ant-card")! as HTMLElement);
}

describe.each(editors)("$title JSON 保存", ({ title, key, valid }) => {
  it.each(["[abc", "", '["abc",]'])("非法 JSON %s 显示提示且不保存", (raw) => {
    const editor = mount(title);
    const input = editor.getByRole("textbox");
    fireEvent.change(input, { target: { value: raw } });
    fireEvent.click(editor.getByText("保存", { exact: true }));
    expect(message.error).toHaveBeenCalledWith("JSON 格式错误");
    expect(api.put).not.toHaveBeenCalled();
    expect(message.success).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();
    expect(input).toHaveValue(raw);
  });

  it("合法 JSON 解析后正常保存并提示成功", async () => {
    const editor = mount(title);
    fireEvent.change(editor.getByRole("textbox"), { target: { value: valid } });
    fireEvent.click(editor.getByText("保存", { exact: true }));
    await waitFor(() => expect(api.put).toHaveBeenCalledWith(`/settings/${key}`, JSON.parse(valid)));
    await waitFor(() => expect(message.success).toHaveBeenCalledWith("已保存"));
    expect(message.error).not.toHaveBeenCalled();
    expect(reload).toHaveBeenCalled();
  });

  it("合法 JSON 的接口错误继续显示保存错误，不误报格式错误", async () => {
    vi.mocked(api.put).mockRejectedValue(new ApiError(500, "INTERNAL", "保存失败，请稍后重试"));
    const editor = mount(title);
    fireEvent.change(editor.getByRole("textbox"), { target: { value: valid } });
    fireEvent.click(editor.getByText("保存", { exact: true }));
    await waitFor(() => expect(message.error).toHaveBeenCalledWith("保存失败，请稍后重试"));
    expect(message.error).not.toHaveBeenCalledWith("JSON 格式错误");
    expect(message.success).not.toHaveBeenCalled();
  });
});
