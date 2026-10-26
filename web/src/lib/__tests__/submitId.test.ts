import { afterEach, describe, expect, it, vi } from "vitest";
import { createSubmitId } from "../submitId";

afterEach(() => vi.unstubAllGlobals());

describe("提交标识", () => {
  it("在内网 HTTP 没有 randomUUID 时也能生成有效且不重复的 UUID", () => {
    const getRandomValues = crypto.getRandomValues.bind(crypto);
    vi.stubGlobal("crypto", { getRandomValues });
    const ids = Array.from({ length: 100 }, createSubmitId);
    expect(new Set(ids).size).toBe(100);
    for (const id of ids) expect(id).toMatch(/^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/);
  });

  it("支持浏览器原生 randomUUID", () => {
    const randomUUID = vi.fn(() => "a798263a-1d7c-456b-b15e-121bff0d63a4");
    vi.stubGlobal("crypto", { randomUUID });
    expect(createSubmitId()).toBe(randomUUID());
  });
});
