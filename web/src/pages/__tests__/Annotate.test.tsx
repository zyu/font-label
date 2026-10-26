import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { message, Modal } from "antd";
import type { ImageDetail, MeResponse } from "../../api/types";
import { ApiError, api } from "../../api/client";
import AnnotatePage from "../Annotate";

const dims = ["white", "black", "variation", "structure", "control"].map((key) => ({ key, label: key, weight: 0.2, max: 20, step: 5 }));
const comments = Object.fromEntries(dims.map((d) => [d.key, `${d.key}：撇捺左右空白大致匀，重心稳定。`]));
let showScorePanel = false;
const scores = Object.fromEntries(dims.map((d) => [d.key, 20]));
const me = { rubric: { dims, grade_bands: [{ min: 90, label: "优秀" }], dim_comment_min: 10, dim_comment_max: 50 }, limits: { auto_next: false }, comment_templates: ["撇捺左右空白大致匀，重心稳定。"], exception_reasons: [{ key: "blur", label: "图片模糊" }] } as MeResponse;
vi.mock("../../auth", () => ({ useAuth: () => ({ me }) }));
vi.mock("../../components/ExampleDrawer", () => ({ default: () => null }));
vi.mock("../../components/ImageViewer", () => ({ default: ({ src }: { src: string }) => <img src={src} alt="标注原图" /> }));
vi.mock("../../components/ScorePanel", async (original) => {
  const module = await original<typeof import("../../components/ScorePanel")>();
  const ActualScorePanel = module.default;
  return { ...module, default: (props: Parameters<typeof ActualScorePanel>[0]) => showScorePanel ? <ActualScorePanel {...props} /> : null };
});

function detail(id: number, filled = true): ImageDetail {
  return { image: { id, scores: filled ? scores : {}, dim_comments: filled ? comments : {}, stored_name: `test${id}.png`, original_url: `/original/${id}`, preview_url: `/preview/${id}`, status: "assigned" }, draft: null, reviews: [] } as unknown as ImageDetail;
}

function mount(filled = true, imageDetail = detail(1, filled)) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  qc.setQueryData(["image", 1], imageDetail);
  qc.setQueryData(["image", 2], detail(2, filled));
  qc.setQueryData(["annotate-queue"], [detail(1).image, detail(2).image]);
  render(<QueryClientProvider client={qc}><MemoryRouter initialEntries={[{ pathname: "/workbench/1", state: { returnTo: "/workbench?tab=approved" } }]}><Routes>
    <Route path="/workbench/:imageId" element={<AnnotatePage />} />
    <Route path="/workbench" element={<div>工作台</div>} />
  </Routes></MemoryRouter></QueryClientProvider>);
  return qc;
}

beforeEach(() => {
  showScorePanel = false;
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, "getComputedStyle").mockImplementation((element) => getComputedStyle(element));
  Object.defineProperty(window, "matchMedia", { writable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn() })) });
  vi.spyOn(api, "get").mockImplementation(async (path) => path === "/images/1" ? detail(1) : detail(2));
  vi.spyOn(api, "post").mockResolvedValue({});
  vi.spyOn(api, "put").mockResolvedValue({});
  vi.spyOn(message, "warning").mockImplementation(() => undefined as never);
  vi.spyOn(message, "error").mockImplementation(() => undefined as never);
  vi.spyOn(message, "success").mockImplementation(() => undefined as never);
});
afterEach(() => { Modal.destroyAll(); cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("标注交互回归", () => {
  it.each(["submitted", "approved", "excluded"] as const)("%s 图片只读，忽略旧草稿，点击和快捷键均不能提交", async (status) => {
    showScorePanel = true;
    const saved = detail(1);
    saved.image.status = status;
    saved.draft = { kind: "score", scores: { ...scores, white: 0 }, dim_comments: { ...comments, white: "不应显示的旧草稿" } };
    mount(true, saved);
    expect(screen.getAllByRole("spinbutton")[0]).toBeDisabled();
    expect(document.querySelector('[data-test="dim-comment-white"]')).toHaveValue(comments.white);
    expect(document.querySelector('[data-test="dim-comment-white"]')).toHaveAttribute("readonly");
    expect(screen.getByRole("button", { name: /提交并下一张/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /异常/ })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /提交并下一张/ }));
    fireEvent.keyDown(window, { key: "Enter", ctrlKey: true });
    fireEvent.keyDown(window, { key: "e" });
    await act(async () => {});
    expect(screen.queryByRole("button", { name: "提交异常" })).not.toBeInTheDocument();
    expect(api.post).not.toHaveBeenCalled();
    expect(api.put).not.toHaveBeenCalled();
  });

  it.each(["submitted", "excluded"])("%s 异常图展示具体原因与备注", (status) => {
    const saved = detail(1);
    Object.assign(saved.image, { status, kind: "exception", exception_reasons: ["blur", "retired_reason"], comment: "右下角无法辨认" });
    mount(true, saved);
    expect(screen.getByText("图片模糊")).toBeInTheDocument();
    expect(screen.getByText("retired_reason")).toBeInTheDocument();
    expect(screen.getByText(/右下角无法辨认/)).toBeInTheDocument();
  });

  it("返回列表前保存修改的草稿", async () => {
    mount();
    fireEvent.change(document.querySelector('[data-test="dim-comment-white"]')!, { target: { value: "返回列表也需要保存的最新点评。" } });
    fireEvent.click(screen.getByRole("button", { name: "返回列表" }));
    fireEvent.click(await screen.findByRole("button", { name: "保存草稿并切换" }));
    await screen.findByText("工作台");
    expect(api.put).toHaveBeenCalledWith("/images/1/draft", expect.objectContaining({ dim_comments: { ...comments, white: "返回列表也需要保存的最新点评。" } }));
  });

  it("点击提交会提示未填写原因，与快捷键保持一致", () => {
    mount(false);
    fireEvent.click(screen.getByRole("button", { name: /提交并下一张/ }));
    expect(message.warning).toHaveBeenCalledWith(expect.stringContaining("打分"));
    expect(api.post).not.toHaveBeenCalled();
  });

  it("评分提交在没有 randomUUID 的 HTTP 环境仍可提交全部维度评语", async () => {
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    mount();
    fireEvent.click(screen.getByRole("button", { name: /提交并下一张/ }));
    await waitFor(() => expect(api.post).toHaveBeenCalledWith("/images/1/submit", expect.objectContaining({ kind: "score", dim_comments: comments, client_submit_id: expect.stringMatching(/^[\da-f-]{36}$/) })));
  });

  it("异常上报在没有 randomUUID 时能发出请求，并展示接口错误", async () => {
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    vi.mocked(api.post).mockRejectedValue(new ApiError(409, "CONFLICT", "任务已被回收"));
    mount();
    fireEvent.click(screen.getByRole("button", { name: /异常/ }));
    fireEvent.click(await screen.findByLabelText("图片模糊"));
    fireEvent.click(screen.getByRole("button", { name: "提交异常" }));
    await waitFor(() => expect(api.post).toHaveBeenCalledWith("/images/1/submit", expect.objectContaining({ kind: "exception", exception_reasons: ["blur"] })));
    await waitFor(() => expect(message.error).toHaveBeenCalledWith("任务已被回收"));
  });

  it("草稿切到下一张再返回时立即恢复各维度评语", async () => {
    mount();
    fireEvent.change(document.querySelector('[data-test="dim-comment-white"]')!, { target: { value: "撇捺左右空白大致匀，这是刚保存的新评语。" } });
    fireEvent.click(screen.getByRole("button", { name: "下一张" }));
    fireEvent.click(await screen.findByRole("button", { name: "保存草稿并切换" }));
    await screen.findByText("test2.png");
    fireEvent.click(screen.getByRole("button", { name: "上一张" }));
    await screen.findByText("test1.png");
    await waitFor(() => expect(document.querySelector('[data-test="dim-comment-white"]')).toHaveValue("撇捺左右空白大致匀，这是刚保存的新评语。"));
  });

  it("草稿保存失败会提示并留在当前图片", async () => {
    vi.mocked(api.put).mockRejectedValue(new ApiError(500, "INTERNAL", "草稿保存失败"));
    mount();
    fireEvent.change(document.querySelector('[data-test="dim-comment-white"]')!, { target: { value: "最新评语，保存失败时不能丢失。" } });
    fireEvent.click(screen.getByRole("button", { name: "下一张" }));
    fireEvent.click(await screen.findByRole("button", { name: "保存草稿并切换" }));
    await waitFor(() => expect(message.error).toHaveBeenCalledWith(expect.stringContaining("草稿")));
    expect(screen.getByText("test1.png")).toBeInTheDocument();
  });

  it("常用评语只填入指定维度，并可直接提交", async () => {
    mount();
    fireEvent.change(document.querySelector('[data-test="dim-comment-control"]')!, { target: { value: "" } });
    fireEvent.mouseEnter(screen.getByRole("button", { name: "control常用评语" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "撇捺左右空白大致匀，重心稳定。" }));
    expect(document.querySelector('[data-test="dim-comment-control"]')).toHaveValue("撇捺左右空白大致匀，重心稳定。");
    expect(document.querySelector('[data-test="dim-comment-white"]')).toHaveValue(comments.white);
    fireEvent.click(screen.getByRole("button", { name: /提交并下一张/ }));
    await waitFor(() => expect(api.post).toHaveBeenCalledWith("/images/1/submit", expect.objectContaining({ dim_comments: { ...comments, control: "撇捺左右空白大致匀，重心稳定。" } })));
  });

  it("评语过短时点击和快捷键都显示相同原因", () => {
    mount();
    fireEvent.change(document.querySelector('[data-test="dim-comment-white"]')!, { target: { value: "略匀" } });
    fireEvent.click(screen.getByRole("button", { name: /提交并下一张/ }));
    fireEvent.keyDown(window, { key: "Enter", ctrlKey: true });
    expect(message.warning).toHaveBeenCalledTimes(2);
    expect(vi.mocked(message.warning).mock.calls[0]).toEqual(vi.mocked(message.warning).mock.calls[1]);
    expect(api.post).not.toHaveBeenCalled();
  });

  it("自动保存尚未完成时，切图保存按顺序落库并保留最新评语", async () => {
    let resolveSave!: () => void;
    vi.mocked(api.put).mockImplementationOnce(() => new Promise<void>((resolve) => { resolveSave = resolve; }));
    mount();
    fireEvent.change(document.querySelector('[data-test="dim-comment-white"]')!, { target: { value: "较早的评语，等待自动保存完成。" } });
    await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1), { timeout: 2000 });
    fireEvent.change(document.querySelector('[data-test="dim-comment-white"]')!, { target: { value: "最新的评语，切图后仍要保留完整。" } });
    fireEvent.click(screen.getByRole("button", { name: "下一张" }));
    fireEvent.click(await screen.findByRole("button", { name: "保存草稿并切换" }));
    expect(api.put).toHaveBeenCalledTimes(1);
    await act(async () => resolveSave());
    await screen.findByText("test2.png");
    expect(api.put).toHaveBeenLastCalledWith("/images/1/draft", expect.objectContaining({ dim_comments: { ...comments, white: "最新的评语，切图后仍要保留完整。" } }));
    fireEvent.click(screen.getByRole("button", { name: "上一张" }));
    await screen.findByText("test1.png");
    expect(document.querySelector('[data-test="dim-comment-white"]')).toHaveValue("最新的评语，切图后仍要保留完整。");
  });

  it("D01：HTTP 环境下异常上报成功后有提示并返回工作台", async () => {
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    mount();
    fireEvent.click(screen.getByRole("button", { name: /异常/ }));
    fireEvent.click(await screen.findByLabelText("图片模糊"));
    fireEvent.click(screen.getByRole("button", { name: "提交异常" }));
    await screen.findByText("工作台");
    expect(api.post).toHaveBeenCalledWith("/images/1/submit", expect.objectContaining({ kind: "exception", client_submit_id: expect.stringMatching(/^[\da-f-]{36}$/) }));
    expect(message.success).toHaveBeenCalledWith("已提交，等待审核");
  });

  it("D01：提交标识生成失败也能显示错误，不会静默无反应", async () => {
    vi.stubGlobal("crypto", { getRandomValues: () => { throw new Error("测试：浏览器随机数接口不可用"); } });
    mount();
    fireEvent.click(screen.getByRole("button", { name: /提交并下一张/ }));
    await waitFor(() => expect(message.error).toHaveBeenCalledWith("测试：浏览器随机数接口不可用"));
    expect(api.post).not.toHaveBeenCalled();
  });

  it.each([["17", 15], ["26", 20]])("数字框输入 %s 后直接快捷键提交会带上最终档位 %s", async (typed, expected) => {
    showScorePanel = true;
    vi.stubGlobal("crypto", { getRandomValues: crypto.getRandomValues.bind(crypto) });
    mount();
    const input = screen.getAllByRole("spinbutton")[0];
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "10" } });
    fireEvent.change(input, { target: { value: typed } });
    fireEvent.keyDown(input, { key: "Enter", ctrlKey: true });
    await waitFor(() => expect(api.post).toHaveBeenCalledWith("/images/1/submit", expect.objectContaining({ scores: { ...scores, white: expected } })));
  });

  it("标注使用原图保证放大细节", () => {
    mount();
    expect(screen.getByAltText("标注原图")).toHaveAttribute("src", "/original/1");
  });
});
