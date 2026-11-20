import type { Dim } from "../api/types";

export const LEVELS = ["S", "A", "B", "C", "D"] as const;
export type Level = typeof LEVELS[number];

export const levelGuide: Record<Level, { title: string; rule: string }> = {
  S: { title: "优秀 · 可作班级范例", rule: "规范端正美观，最多 1 处轻微问题。" },
  A: { title: "良好 · 有小瑕疵", rule: "2～3 处轻微问题，或 1 处明显问题。" },
  B: { title: "合格 · 规范达标", rule: "2 处明显问题，或轻微问题较多，无严重问题。" },
  C: { title: "待提高 · 需重点练习", rule: "3 处及以上明显问题，或 1 处严重问题。" },
  D: { title: "较差 · 基本没写好", rule: "问题遍布全字，或 2 处及以上严重问题。" },
};

const defaultComments: Record<string, readonly string[]> = {
  white: [
    "笔画间距均匀，字内空白分布协调。",
    "字内留白基本均匀，靠右笔画间距略紧。",
    "相邻横画间距有两处偏窄，留白不够均匀。",
    "上下留白明显失衡，多处笔画挤在一起。",
    "字内空白分布失衡，多处间距过大或过窄。",
  ],
  black: [
    "字的重心稳定，左右笔画的分量均衡。",
    "左右分量基本均衡，收笔一侧略显偏重。",
    "重心略偏一侧，上下两部分分量差异明显。",
    "重心明显偏离中轴，两侧笔画分量不均。",
    "多处笔画分量失衡，整体重心明显偏移。",
  ],
  variation: [
    "主笔舒展，长短变化清楚，撇捺搭配自然。",
    "主次长短基本分明，末笔稍短了一些。",
    "撇捺长短接近，主笔不突出，横画长度雷同。",
    "多处笔画长短相近，主次关系难以辨认。",
    "笔画普遍缺少长短变化，主笔没有体现。",
  ],
  structure: [
    "各部件位置得当，整体占格端正协调。",
    "部件比例大致协调，右侧略显拥挤。",
    "左右部件有两处对齐偏差，右部空间偏小。",
    "左旁过大、右部被挤，上下错位，整体失衡。",
    "各部件比例明显失调，多处位置偏离。",
  ],
  control: [
    "起收笔清楚，行笔稳定，轻重变化自然。",
    "笔画粗细基本一致，长横中段略轻。",
    "两处收笔略显拖沓，部分笔画轻重不够清楚。",
    "多处转折生硬，起收笔和轻重变化不稳定。",
    "起收笔普遍不到位，多处笔画抖断，横竖失控。",
  ],
};

const legacyComments = [
  { dimension: "white", grade: "S", text: "撑满到位，四周留白均匀。" },
  { dimension: "white", grade: "A", text: "布匀略欠，横向笔画间距不均。" },
  { dimension: "black", grade: "B", text: "重心偏左，注意中轴对齐。" },
  { dimension: "control", grade: "S", text: "撇捺出锋干净，姿态舒展。" },
  { dimension: "variation", grade: "B", text: "主笔不够放开，注意长短变化。" },
  { dimension: "control", grade: "A", text: "整体稳，留意右侧收笔不要出格。" },
];

export function scoreForLevel(dim: Dim, level: Level): number {
  const max = dim.max > 0 ? dim.max : 10;
  const step = dim.step && dim.step > 1 ? dim.step : 1;
  return Math.round((4 - LEVELS.indexOf(level)) * Math.floor(max / step) / 4) * step;
}

export function levelForScore(dim: Dim, score?: number): Level | undefined {
  if (score == null || !Number.isFinite(score)) return undefined;
  if (score === 0) return "D";
  return LEVELS.reduce((best, level) =>
    Math.abs(score - scoreForLevel(dim, level)) < Math.abs(score - scoreForLevel(dim, best)) ? level : best,
  );
}

export function commentChoices(config: unknown, dimension: string, grade: Level): string[] {
  const options: string[] = [];
  if (Array.isArray(config)) {
    for (const item of config) {
      const template = typeof item === "string"
        ? legacyComments.find((entry) => entry.text === item.trim())
        : item;
      if (template?.dimension === dimension && template.grade === grade && typeof template.text === "string" && template.text.trim()) {
        options.push(template.text.trim());
      }
    }
  }
  const standard = defaultComments[dimension]?.[LEVELS.indexOf(grade)];
  if (standard) options.push(standard);
  return [...new Set(options)];
}
