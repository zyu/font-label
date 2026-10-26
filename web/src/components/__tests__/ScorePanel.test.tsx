import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
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

function ControlledScores() {
  const [scores, setScores] = useState<Record<string, number>>({});
  return <ScorePanel dims={DIMS} value={scores} hotkeyDim={null}
    onPick={(key, score) => setScores((current) => ({ ...current, [key]: score }))} />;
}

beforeEach(() => vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} }));
afterEach(() => vi.unstubAllGlobals());

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

  it.each(["10", "15", "20"])("逐字键入 %s 不会提前吸附第一位", (target) => {
    render(<ControlledScores />);
    const input = screen.getAllByRole("spinbutton")[0] as HTMLInputElement;
    fireEvent.focus(input);
    for (const char of target) {
      fireEvent.change(input, { target: { value: input.value + char } });
    }
    expect(input.value).toBe(target);
    fireEvent.blur(input);
    expect(input.value).toBe(target);
  });

  it("17 在编辑时保留原值，失焦后才吸附成 15", () => {
    render(<ControlledScores />);
    const input = screen.getAllByRole("spinbutton")[0] as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "17" } });
    expect(input.value).toBe("17");
    fireEvent.blur(input);
    expect(input.value).toBe("15");
  });

  it("18 在按回车后吸附成 20", () => {
    render(<ControlledScores />);
    const input = screen.getAllByRole("spinbutton")[0] as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "18" } });
    expect(input.value).toBe("18");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(input.value).toBe("20");
  });

  it("未编辑过的分数框失焦仍为空", () => {
    render(<ControlledScores />);
    const input = screen.getAllByRole("spinbutton")[0] as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.blur(input);
    expect(input.value).toBe("");
  });

  it("粘贴整数和方向键仍能按 5 分档调整", () => {
    render(<ControlledScores />);
    const input = screen.getAllByRole("spinbutton")[0] as HTMLInputElement;
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "15" } });
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(input.value).toBe("10");
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(input.value).toBe("15");
  });

  it("滑杆仍即时按 5 分档更新数字框", () => {
    render(<ControlledScores />);
    fireEvent.keyDown(screen.getAllByRole("slider")[0], { key: "ArrowRight", keyCode: 39, which: 39 });
    expect((screen.getAllByRole("spinbutton")[0] as HTMLInputElement).value).toBe("5");
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
