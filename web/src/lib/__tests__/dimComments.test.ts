import { describe, expect, it } from "vitest";
import { charCount, dimCommentStates, dimCommentsOk, firstBlockingDimComment } from "../dimComments";
import type { Dim } from "../../api/types";

const DIMS: Dim[] = [
  { key: "white", label: "白平衡（匀）", weight: 0.2, max: 20, step: 5 },
  { key: "black", label: "黑平衡（稳）", weight: 0.2, max: 20, step: 5 },
  { key: "variation", label: "参差", weight: 0.2, max: 20, step: 5 },
  { key: "structure", label: "结构", weight: 0.2, max: 20, step: 5 },
  { key: "control", label: "控笔", weight: 0.2, max: 20, step: 5 },
];

const text = (n: number) => "字".repeat(n);

describe("charCount", () => {
  it("按字符计数，忽略首尾空白", () => {
    expect(charCount("  十个字十个字十个字  ")).toBe(9);
    expect(charCount("")).toBe(0);
    expect(charCount("   ")).toBe(0);
  });
});

describe("dimCommentStates", () => {
  it("五维都有、且每维 >= 10 字才算合格", () => {
    const values = {
      white: text(10),
      black: text(10),
      variation: text(12),
      structure: text(20),
      control: text(50),
    };
    const states = dimCommentStates(DIMS, values, 10, 50);
    expect(states).toHaveLength(5);
    expect(states.every((s) => s.ok)).toBe(true);
    expect(dimCommentsOk(DIMS, values, 10, 50)).toBe(true);
  });

  it("某维只有 9 字时不合格，并给出还差 1 字", () => {
    const values = { white: text(9), black: text(10), variation: text(10), structure: text(10), control: text(10) };
    const white = dimCommentStates(DIMS, values, 10, 50)[0];
    expect(white.ok).toBe(false);
    expect(white.hint).toContain("还差 1 字");
    expect(dimCommentsOk(DIMS, values, 10, 50)).toBe(false);
    expect(firstBlockingDimComment(DIMS, values, 10, 50)?.key).toBe("white");
  });

  it("越界（超过上限）也不合格", () => {
    const values = { white: text(51), black: text(10), variation: text(10), structure: text(10), control: text(10) };
    const white = dimCommentStates(DIMS, values, 10, 50)[0];
    expect(white.ok).toBe(false);
    expect(white.hint).toContain("超出 1 字");
  });

  it("未填写的维度提示未填写", () => {
    const states = dimCommentStates(DIMS, {}, 10, 50);
    expect(states[0].ok).toBe(false);
    expect(states[0].hint).toContain("未填写");
    expect(firstBlockingDimComment(DIMS, {}, 10, 50)?.key).toBe("white");
  });

  it("点评里的多余空白不计数", () => {
    const values = { white: `  ${text(9)}  `, black: text(10), variation: text(10), structure: text(10), control: text(10) };
    expect(dimCommentStates(DIMS, values, 10, 50)[0].ok).toBe(false);
  });
});
