import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import ScorePanel, { DIM_HINTS, clampScore, computeTotal } from "../ScorePanel";
import type { Dim } from "../../api/types";

// 现行口径：五维各 20 分
const DIMS: Dim[] = [
  { key: "white", label: "白平衡（匀）", weight: 0.2, max: 20, step: 5 },
  { key: "black", label: "黑平衡（稳）", weight: 0.2, max: 20, step: 5 },
  { key: "variation", label: "参差", weight: 0.2, max: 20, step: 5 },
  { key: "structure", label: "结构", weight: 0.2, max: 20, step: 5 },
  { key: "control", label: "控笔", weight: 0.2, max: 20, step: 5 },
];

describe("computeTotal（五维百分制）", () => {
  it("每维 20 分时总分就是直接相加", () => {
    expect(
      computeTotal(DIMS, { white: 18, black: 18, variation: 16, structure: 17, control: 16 }),
    ).toBe(85);
  });

  it("满分 100 / 全 12 分 = 60 / 全 0 = 0", () => {
    expect(
      computeTotal(DIMS, { white: 20, black: 20, variation: 20, structure: 20, control: 20 }),
    ).toBe(100);
    expect(
      computeTotal(DIMS, { white: 12, black: 12, variation: 12, structure: 12, control: 12 }),
    ).toBe(60);
    expect(
      computeTotal(DIMS, { white: 0, black: 0, variation: 0, structure: 0, control: 0 }),
    ).toBe(0);
  });

  it("缺任一维度返回 null（不允许提前显示总分）", () => {
    expect(computeTotal(DIMS, { white: 20, black: 20, variation: 20, structure: 20 })).toBeNull();
  });

  it("后台改权重后按权重归一化", () => {
    const weighted: Dim[] = [
      { key: "white", label: "白平衡", weight: 0.4, max: 20 },
      { key: "black", label: "黑平衡", weight: 0.3, max: 20 },
      { key: "variation", label: "参差", weight: 0.2, max: 20 },
      { key: "structure", label: "结构", weight: 0.1, max: 20 },
    ];
    expect(computeTotal(weighted, { white: 20, black: 20, variation: 20, structure: 0 })).toBe(90);
  });
});

describe("ScorePanel（滑杆 + 数字框）", () => {
  it("每个维度一行，并带 0~20 的上限标注", () => {
    const { container } = render(
      <ScorePanel dims={DIMS} value={{}} onPick={() => undefined} hotkeyDim={null} />,
    );
    expect(container.querySelectorAll(".score-row")).toHaveLength(5);
    expect(container.querySelectorAll(".score-row input")).toHaveLength(5);
    expect(screen.getByText("白平衡（匀）")).toBeInTheDocument();
    expect(screen.getByText("控笔")).toBeInTheDocument();
  });

  it("未打分时数字框是空占位，不是 0", () => {
    render(<ScorePanel dims={DIMS} value={{}} onPick={() => undefined} hotkeyDim={null} />);
    const inputs = screen.getAllByRole("spinbutton") as HTMLInputElement[];
    expect(inputs[0].value).toBe("");
    expect(inputs[0].placeholder).toBe("—");
  });

  it("在数字框里键入 15 会上报该维度；键入 17 会被对齐成 15", () => {
    const onPick = vi.fn();
    render(<ScorePanel dims={DIMS} value={{}} onPick={onPick} hotkeyDim={null} />);
    const input = screen.getAllByRole("spinbutton")[0];
    fireEvent.change(input, { target: { value: "15" } });
    expect(onPick.mock.calls.at(-1)).toEqual(["white", 15]);
    fireEvent.change(input, { target: { value: "17" } });
    expect(onPick.mock.calls.at(-1)).toEqual(["white", 15]);
  });

  it("非档位值会被对齐到最近的 5 分档", () => {
    expect(clampScore(17, 20)).toBe(15);
    expect(clampScore(18, 20)).toBe(20);
    expect(clampScore(3, 20)).toBe(5);
    expect(clampScore(2, 20)).toBe(0);
    expect(clampScore(26, 20)).toBe(20);
    expect(clampScore(5, 20)).toBe(5);
    expect(clampScore(10, 20)).toBe(10);
  });

  it("已填的分数显示在框里", () => {
    render(<ScorePanel dims={DIMS} value={{ control: 17 }} onPick={() => undefined} hotkeyDim={null} />);
    const inputs = screen.getAllByRole("spinbutton") as HTMLInputElement[];
    expect(inputs[4].value).toBe("17");
  });

  it("五个维度都带解读文案（供问号提示）", () => {
    for (const d of DIMS) {
      expect(DIM_HINTS[d.key]).toBeTruthy();
    }
    expect(DIM_HINTS.black).toContain("一分为二");
  });
});
