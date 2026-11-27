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

it.each(["质检不合格", "组间等级差异"])("从登记表处理%s并按仲裁编号提交结论", async (cause) => {
  const gradeDispute = cause === "组间等级差异";
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
        annotation: { kind: gradeDispute ? "score" : "exception" }, review: "approve", reviewer: "审核员", version: 1,
        quality_result: gradeDispute ? "" : "fail", inspector: gradeDispute ? "" : "质检员", arbiter: "仲裁员", ruling: "",
        grade_differences: gradeDispute ? [{
          dimension: "white", label: "白平衡", own_grade: "S", peer_grade: "B", gap: 2,
          own_comment: "本组评语", peer_comment: "对组评语", peer_group: "乙组", peer_annotator: "王老师",
        }] : undefined,
      }],
    };
  });
  const post = vi.spyOn(api, "post").mockResolvedValue({ ok: true });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><Quality /></QueryClientProvider>);
  expect(screen.queryByText("抽检规则")).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole("tab", { name: "争议仲裁登记" }));
  fireEvent.click(await screen.findByRole("button", { name: /P000020/ }));
  if (gradeDispute) {
    expect(await screen.findByText("乙组 · 王老师")).toBeVisible();
    expect(screen.getByText("相差 2 档")).toBeVisible();
    expect(screen.getByText("对组评语")).toBeVisible();
  }
  fireEvent.change(screen.getByRole("textbox", { name: "处理备注" }), { target: { value: "按原图退回" } });
  fireEvent.click(screen.getByRole("button", { name: /^退\s*回$/ }));
  await waitFor(() => expect(post).toHaveBeenCalledWith("/quality/disputes/47", { result: "return", note: "按原图退回" }));
});
