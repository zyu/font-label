import { InputNumber, Slider, Tooltip } from "antd";
import { QuestionCircleOutlined } from "@ant-design/icons";
import type { Dim } from "../api/types";

/** 每个维度的判定口径（标注页 label 上的问号即取自这里） */
export const DIM_HINTS: Record<string, string> = {
  white: "白平衡（匀）：字/笔画与笔画之间的空白要均匀",
  black: "黑平衡（稳）：笔画长度代表重量；每个字从中间一分为二，两半的笔画总长度/重量大致相当",
  variation: "参差：上下左右四面的笔画要有长有短，不能一样齐",
  structure:
    "结构：分左右、上下、半包围、全包围、独体字，按结构逻辑看——哪边笔画多，哪边占位大；独体字参照“满”",
  control: "控笔：笔画书写要有轻重变化（起收笔的粗细、顿挫）",
};

interface Props {
  dims: Dim[];
  value: Record<string, number>;
  /** 一次只上报一个维度，父组件用函数式更新，快速连改不会丢 */
  onPick: (dimKey: string, score: number) => void;
  hotkeyDim: string | null;
}

/** 打分粒度：0/5/10/15/20 */
export const SCORE_STEP = 5;

/** 把输入夹到 0~max 并对齐到 step（数字框可能收到越界值、空值或 17 这种非档位值） */
export function clampScore(
  v: number | string | null | undefined,
  max: number,
  step: number = SCORE_STEP,
): number | null {
  if (v === null || v === undefined || v === "") return null;
  const n = Number(v);
  if (Number.isNaN(n)) return null;
  const st = step > 1 ? step : 1;
  const snapped = Math.round(n / st) * st;
  return Math.max(0, Math.min(max, snapped));
}

/**
 * 每维 0~max（现行 20 分）。用「滑杆 + 数字框」而不是 0~20 的按钮墙：
 * 21 个按钮一排既点不准，也放不下五个维度。
 */
export default function ScorePanel({ dims, value, onPick, hotkeyDim }: Props) {
  const stepOf = (d: Dim) => (d.step && d.step > 1 ? d.step : SCORE_STEP);
  return (
    <>
    <div className="score-grid">
      {dims.map((d) => {
        const cur = value[d.key];
        const hint = DIM_HINTS[d.key] ?? "";
        const max = d.max || 20;
        const step = stepOf(d);
        return (
          <div className="score-row" key={d.key} data-dim={d.key}>
            <div
              className="score-label"
              style={{ color: hotkeyDim === d.key ? "#2b5cff" : undefined, width: 108 }}
            >
              <Tooltip title={hint || d.label}>
                <span>
                  {d.label}
                  {hint && (
                    <QuestionCircleOutlined style={{ fontSize: 11, marginLeft: 4, color: "#98a2b3" }} />
                  )}
                </span>
              </Tooltip>
            </div>
            <Slider
              min={0}
              max={max}
              step={step}
              value={cur ?? 0}
              onChange={(v) => onPick(d.key, v)}
              tooltip={{ formatter: (v) => `${v} 分` }}
              style={{ flex: 1, margin: "0 10px" }}
            />
            <InputNumber
              size="small"
              min={0}
              max={max}
              step={step}
              value={cur}
              placeholder="—"
              onChange={(v) => {
                const next = clampScore(v, max, step);
                if (next === null) return;
                onPick(d.key, next);
              }}
              style={{ width: 58 }}
            />
            <span style={{ width: 30, fontSize: 11, color: "#98a2b3", textAlign: "right" }}>/{max}</span>
          </div>
        );
      })}
    </div>
    <div className="score-step-hint">
      打分档位：{(() => {
        const d = dims[0];
        if (!d) return "";
        const max = d.max || 20;
        const st = d.step && d.step > 1 ? d.step : SCORE_STEP;
        const marks: number[] = [];
        for (let m = 0; m <= max; m += st) marks.push(m);
        return marks.join(" / ");
      })()}
      （滑杆和数字框都只停在这些档位上）
    </div>
    </>
  );
}

/** 把一组历史分数对齐到当前档位（用于回填草稿 / 被打回的上一版） */
export function snapToStep(dims: Dim[], raw: Record<string, number>): Record<string, number> {
  const out: Record<string, number> = {};
  for (const d of dims) {
    const v = raw?.[d.key];
    if (v === undefined) continue;
    const st = d.step && d.step > 1 ? d.step : SCORE_STEP;
    const snapped = clampScore(v, d.max || 20, st);
    if (snapped !== null) out[d.key] = snapped;
  }
  return out;
}

export function computeTotal(dims: Dim[], scores: Record<string, number>): number | null {
  // 每个维度都打了分才算总分，避免"缺一维也显示总分"的误读
  if (dims.some((d) => scores[d.key] === undefined)) return null;
  let sum = 0;
  let weight = 0;
  for (const d of dims) {
    const max = d.max || 20;
    sum += (scores[d.key] / max) * d.weight;
    weight += d.weight;
  }
  if (weight === 0) return null;
  return Math.round((sum / weight) * 1000) / 10;
}
