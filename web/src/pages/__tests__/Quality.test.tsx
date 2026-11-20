import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { api } from "../../api/client";
import Quality from "../Quality";

vi.mock("../../auth", () => ({
  useAuth: () => ({ user: { id: 8, role: "quality" }, me: { rubric: { dims: [] } } }),
}));
vi.mock("../../components/ImageViewer", () => ({ default: () => <span>原图</span> }));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("从登记表按仲裁编号提交结论", async () => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const computed = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation((element) => computed(element));
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {},
  })));
  vi.spyOn(api, "get").mockImplementation(async (path) => {
    if (!path.startsWith("/quality/disputes")) return { items: [], total: 0 };
    return {
      total: 1,
      items: [{
        id: 12, dispute_id: 47, image_id: 22, sample_id: "P000020", image: "sample.png", group: "甲组",
        annotation: { kind: "exception" }, review: "approve", reviewer: "审核员", version: 1,
        quality_result: "fail", inspector: "质检员", arbiter: "仲裁员", ruling: "",
      }],
    };
  });
  const post = vi.spyOn(api, "post").mockResolvedValue({ ok: true });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><Quality /></QueryClientProvider>);
  expect(screen.queryByText("抽检规则")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("tab", { name: "争议仲裁登记" }));
  fireEvent.click(await screen.findByRole("button", { name: /P000020/ }));
  fireEvent.change(screen.getByRole("textbox", { name: "处理备注" }), { target: { value: "按原图退回" } });
  fireEvent.click(screen.getByRole("button", { name: /^退\s*回$/ }));
  await waitFor(() => expect(post).toHaveBeenCalledWith("/quality/disputes/47", { result: "return", note: "按原图退回" }));
});
