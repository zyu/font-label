import { cleanup, render, screen, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import StatsPage from "../Stats";
import FoldersPage from "../admin/Folders";

let role = "admin";
vi.mock("../../auth", () => ({ useAuth: () => ({ user: { role } }) }));
vi.mock("recharts", () => ({ ResponsiveContainer: () => null, LineChart: () => null, CartesianGrid: () => null, Legend: () => null, Line: () => null, Tooltip: () => null, XAxis: () => null, YAxis: () => null }));

beforeEach(() => {
 role = "admin";
 vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
 const computed = window.getComputedStyle.bind(window);
 vi.spyOn(window, "getComputedStyle").mockImplementation((element) => computed(element));
 Object.defineProperty(window, "matchMedia", { writable: true, value: vi.fn(() => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} })) });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

function mount(Page: typeof StatsPage) {
 const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
 const folders = [
  { id:1, name:"QA_R2_上传_1812", total:8, unassigned:0, assigned:0, submitted:0, approved:7, rejected:0, excluded:1 },
  { id:2, name:"未完成批次", total:6, unassigned:1, assigned:1, submitted:1, approved:1, rejected:1, excluded:1 },
 ];
 qc.setQueryData(["stats-me"], { holding:2, rejected:0, pending:1, approved:7, submitted_today:0, granted:21, avg_total:80, reject_events:7 });
 qc.setQueryData(["folders", "all"], { items:folders });
 qc.setQueryData(["stats-folders"], { items:folders });
 qc.setQueryData(["stats-overview"], {});
 qc.setQueryData(["stats-annotators"], { items:[] });
 qc.setQueryData(["stats-rejections"], { reasons:[] });
 qc.setQueryData(["stats-series"], { points:[] });
 render(<QueryClientProvider client={qc}><Page /></QueryClientProvider>);
}

describe("统计口径与文件夹状态展示", () => {
 it("个人累计驳回卡片与文字均为7次，并单列当前待重做0张", () => {
  role = "annotator";
  mount(StatsPage);
  const label = screen.getByText("累计驳回（次）");
  expect(label.closest(".stat-card")).toHaveTextContent("7");
  expect(screen.getByText(/累计领取/).textContent).toMatch(/累计领取\s*21\s*张；累计被驳回\s*7\s*次/);
  expect(screen.getByText(/当前待重做/).textContent).toMatch(/当前待重做\s*0\s*张/);
 });

 it.each([["文件夹列表", FoldersPage], ["统计文件夹进度", StatsPage]] as const)("%s展示无效图和标注中，进度只计审核完成图片", (_, Page) => {
  mount(Page);
  const row = screen.getByRole("row", { name:/QA_R2_上传_1812/ });
  const table = row.closest("table")!;
  const headers = within(table).getAllByRole("columnheader").map((header) => header.textContent);
  const excludedIndex = headers.indexOf("无效图");
  const assignedIndex = headers.indexOf("标注中");
  expect(excludedIndex).toBeGreaterThan(-1);
  expect(assignedIndex).toBeGreaterThan(-1);
  expect(within(row).getAllByRole("cell")[excludedIndex]).toHaveTextContent("1");
  expect(within(row).getByText("8/8")).toBeInTheDocument();
  const unfinished = screen.getByRole("row", { name:/未完成批次/ });
  expect(within(unfinished).getByText("2/6")).toBeInTheDocument();
  expect(within(unfinished).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "33");
 });
});
