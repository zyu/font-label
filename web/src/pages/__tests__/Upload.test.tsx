import { cleanup, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import UploadPage from "../admin/Upload";

let maxFileMB: number | undefined;
vi.mock("../../auth", () => ({ useAuth: () => ({ me: { limits: { max_file_mb: maxFileMB } } }) }));

beforeEach(() => {
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  Object.defineProperty(window, "matchMedia", { writable: true, value: vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn() })) });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("上传大小提示与实际配置一致", () => {
  it.each([10, 0, 5, undefined])("配置 %sMB", (value) => {
    maxFileMB = value;
    const qc = new QueryClient({ defaultOptions: { queries: { staleTime: Infinity } } });
    qc.setQueryData(["folders"], { items: [] });
    render(<QueryClientProvider client={qc}><UploadPage /></QueryClientProvider>);
    expect(screen.getByText(`支持 jpg / png / webp / gif，单张 ≤ ${value ?? 10}MB`)).toBeInTheDocument();
  });
});
