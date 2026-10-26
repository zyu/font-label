import type { Dim } from "../api/types";

/** 单条维度点评的校验结果，界面直接用这些字段渲染色字与提示 */
export interface DimCommentState {
  key: string;
  label: string;
  text: string;
  /** 已输入字数（按 Unicode 字符数，和输入框里的「n 字」一致） */
  len: number;
  ok: boolean;
  /** 给用户看的一句话：还差几个字 / 超了几个字 / 已符合 */
  hint: string;
}

/** 去掉首尾空白后按字符（非字节）计数 */
export function charCount(text: string): number {
  return [...text.trim()].length;
}

/** 每个维度一条点评：状态 + 提示文案 */
export function dimCommentStates(
  dims: Dim[],
  values: Record<string, string>,
  min: number,
  max: number,
): DimCommentState[] {
  return dims.map((d) => {
    const text = values[d.key] ?? "";
    const len = charCount(text);
    const ok = len >= min && len <= max;
    let hint: string;
    if (len === 0) {
      hint = `未填写，至少 ${min} 字`;
    } else if (len < min) {
      hint = `还差 ${min - len} 字（${len}/${min}）`;
    } else if (len > max) {
      hint = `超出 ${len - max} 字（上限 ${max}）`;
    } else {
      hint = `${len} 字，符合要求`;
    }
    return { key: d.key, label: d.label, text, len, ok, hint };
  });
}

/** 五维点评是否全部合格 */
export function dimCommentsOk(
  dims: Dim[],
  values: Record<string, string>,
  min: number,
  max: number,
): boolean {
  return dimCommentStates(dims, values, min, max).every((s) => s.ok);
}

/** 第一条不合格的维度点评，用来做提交按钮的阻断原因 */
export function firstBlockingDimComment(
  dims: Dim[],
  values: Record<string, string>,
  min: number,
  max: number,
): DimCommentState | null {
  return dimCommentStates(dims, values, min, max).find((s) => !s.ok) ?? null;
}
