import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ImageDTO, MeResponse } from "../../api/types";
import { api } from "../../api/client";
import WorkbenchPage from "../Workbench";
import AnnotatePage from "../Annotate";
import ReviewQueuePage from "../ReviewQueue";
import ReviewPage from "../ReviewPage";

const me = {
  rubric: { dims: [], grade_bands: [] }, limits: {},
  exception_reasons: [{ key: "blur", label: "图片模糊" }, { key: "crop", label: "图片不完整" }],
} as unknown as MeResponse;
vi.mock("../../auth", () => ({ useAuth: () => ({ me }) }));
vi.mock("../../components/ImageViewer", () => ({ default: () => <div>原图</div> }));
vi.mock("../../components/ExampleDrawer", () => ({ default: () => null }));

function CurrentURL() { const location = useLocation(); return <output data-testid="url">{location.pathname}{location.search}</output>; }

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const computed = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation((element) => computed(element));
  Object.defineProperty(window, "matchMedia", { writable: true, value: vi.fn(() => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} })) });
  vi.spyOn(api, "get").mockResolvedValue({ items: [], total: 0 });
  vi.spyOn(api, "post").mockResolvedValue({});
  vi.spyOn(api, "put").mockResolvedValue({});
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function mount(url: string, status = "excluded") {
  const image = { id: 1, folder_id: 9, folder: "测试文件夹", stored_name: "test.png", kind: "exception", status, total: null, exception_reasons: ["blur", "crop"], comment: "需要重拍", scores: {}, dim_comments: {} } as ImageDTO;
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  qc.setQueryData(["claim-folders"], { items: [] });
  qc.setQueryData(["folders"], { items: [{ id: 9, name: "测试文件夹", submitted: 1 }] });
  qc.setQueryData(["my-counts"], { active: 0, submitted: 1, rejected: 0 });
  for (const tab of ["approved", "submitted"]) qc.setQueryData(["my-tasks", tab], { items: [image], total: 1 });
  qc.setQueryData(["review-queue", "exception", 9], { items: [image], total: 1 });
  qc.setQueryData(["review-queue-nav"], { items: [image], total: 1 });
  qc.setQueryData(["annotate-queue"], []);
  qc.setQueryData(["image", 1], { image, draft: null, reviews: [] });
  render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[url]}><CurrentURL /><Routes>
    <Route path="/workbench" element={<WorkbenchPage />} />
    <Route path="/workbench/:imageId" element={<AnnotatePage />} />
    <Route path="/review" element={<ReviewQueuePage />} />
    <Route path="/review/:imageId" element={<ReviewPage />} />
  </Routes></MemoryRouter></QueryClientProvider>);
}

describe("列表和详情往返", () => {
  it.each(["approved", "submitted"])("标注 %s 分类显示异常原因并在返回后保留分类", async (tab) => {
    mount(`/workbench?tab=${tab}`, tab === "approved" ? "excluded" : "submitted");
    expect(screen.getByText("图片模糊")).toBeInTheDocument();
    expect(screen.getByText("图片不完整")).toBeInTheDocument();
    fireEvent.click(screen.getByText("test.png"));
    await screen.findByRole("button", { name: "返回列表" });
    expect(screen.getByText("图片模糊")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /提交并下一张/ })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "返回列表" }));
    expect(screen.getByTestId("url")).toHaveTextContent(`/workbench?tab=${tab}`);
    expect(screen.getByText("test.png")).toBeInTheDocument();
    expect(api.post).not.toHaveBeenCalled();
    expect(api.put).not.toHaveBeenCalled();
  });

  it("审核返回列表保留异常分类和文件夹", async () => {
    mount("/review?kind=exception&folder_id=9", "submitted");
    fireEvent.click(screen.getByText("test.png"));
    await screen.findByRole("button", { name: "返回列表" });
    fireEvent.click(screen.getByRole("button", { name: "返回列表" }));
    expect(screen.getByTestId("url")).toHaveTextContent("/review?kind=exception&folder_id=9");
    expect(screen.getByText("test.png")).toBeInTheDocument();
    expect(api.post).not.toHaveBeenCalled();
  });
});
