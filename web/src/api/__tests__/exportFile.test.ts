import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../client";

const contents = '[{"id":1,"dim_comments":{"white":"撇捺左右空白大致匀。"}}]';

beforeEach(() => vi.stubGlobal("localStorage", { getItem: () => null }));
afterEach(() => vi.unstubAllGlobals());

function respond(body: string, status: number, headers: Record<string, string>) {
  const fetch = vi.fn().mockResolvedValue(new Response(body, { status, headers }));
  vi.stubGlobal("fetch", fetch);
  return fetch;
}

function blobText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

describe("导出响应识别", () => {
  it.each(["approved", "reviewed", "all"])("%s 状态的 JSON 附件作为文件下载", async (status) => {
    const fetch = respond(contents, 200, {
      "Content-Type": "application/json; charset=utf-8",
      "Content-Disposition": 'attachment; filename="font-label.json"',
    });
    const body = { format: "json", filters: { status } };
    const result = await api.exportFile(body);
    expect(result.blob).toBeDefined();
    expect(await blobText(result.blob!)).toBe(contents);
    expect(result.jobId).toBeUndefined();
    expect(fetch).toHaveBeenCalledWith("/api/v1/exports", expect.objectContaining({ method: "POST", body: JSON.stringify(body) }));
  });

  it("空数组 JSON 附件也会下载", async () => {
    respond("[]", 200, { "Content-Type": "application/json", "Content-Disposition": "attachment" });
    const result = await api.exportFile({ format: "json" });
    expect(await blobText(result.blob!)).toBe("[]");
  });

  it("附件标记忽略大小写，文件内容不会被当作任务描述", async () => {
    const content = '{"data":{"async":true,"job_id":7}}';
    respond(content, 200, { "Content-Type": "application/json", "Content-Disposition": 'Attachment; filename="result.json"' });
    const result = await api.exportFile({ format: "json" });
    expect(await blobText(result.blob!)).toBe(content);
    expect(result.jobId).toBeUndefined();
  });

  it.each([200, 202])("继续支持 HTTP %s 的异步导出任务", async (status) => {
    respond(JSON.stringify({ data: { async: true, job_id: 42 } }), status, { "Content-Type": "application/json" });
    expect(await api.exportFile({ format: "json" })).toEqual({ jobId: 42 });
  });

  it("HTTP 202 可直接标识任务，无须额外 async 字段", async () => {
    respond('{"data":{"job_id":42}}', 202, { "Content-Type": "application/json" });
    expect(await api.exportFile({ format: "json" })).toEqual({ jobId: 42 });
  });

  it.each([undefined, 0, -1, "42", 1.5])("异步任务标识 %s 非法时明确报错", async (jobId) => {
    respond(JSON.stringify({ data: { async: true, job_id: jobId } }), 200, { "Content-Type": "application/json" });
    await expect(api.exportFile({ format: "json" })).rejects.toThrow("导出响应格式异常，请重试");
  });

  it("202 返回无效 JSON 时明确报错", async () => {
    respond("invalid", 202, { "Content-Type": "application/json" });
    await expect(api.exportFile({ format: "json" })).rejects.toThrow("导出响应格式异常，请重试");
  });

  it.each([
    ["xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"],
    ["csv", "text/csv; charset=utf-8"],
    ["jsonl", "application/x-ndjson; charset=utf-8"],
  ])("%s 下载保持正常", async (format, contentType) => {
    respond("file contents", 200, { "Content-Type": contentType, "Content-Disposition": `attachment; filename="result.${format}"` });
    const result = await api.exportFile({ format });
    expect(await blobText(result.blob!)).toBe("file contents");
  });

  it("接口失败时保留服务端错误提示", async () => {
    respond('{"error":{"code":"VALIDATION_FAILED","message":"导出格式不合法"}}', 422, { "Content-Type": "application/json" });
    await expect(api.exportFile({ format: "unknown" })).rejects.toThrow("导出格式不合法");
  });
});
