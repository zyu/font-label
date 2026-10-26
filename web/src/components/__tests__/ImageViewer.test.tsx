import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import ImageViewer from "../ImageViewer";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

function loadImage(width: number, height: number) {
  const img = screen.getByRole("img", { name: "作业" });
  const wrap = img.parentElement!;
  Object.defineProperties(wrap, {
    clientWidth: { configurable: true, value: 800 },
    clientHeight: { configurable: true, value: 600 },
  });
  vi.spyOn(wrap, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, width: 800, height: 600 } as DOMRect);
  Object.defineProperties(img, {
    naturalWidth: { configurable: true, value: width },
    naturalHeight: { configurable: true, value: height },
  });
  fireEvent.load(img);
  return { img, wrap };
}

describe("图片缩放", () => {
  it("小图默认按原始尺寸显示", async () => {
    render(<ImageViewer src="/small.png" alt="作业" resetKey={1} />);
    const { img } = loadImage(200, 100);
    await waitFor(() => expect(img.style.opacity).toBe("1"));
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 60)); });
    expect(img.style.transform).toContain("scale(1)");
  });

  it("大图可缩到完整显示，不受 20% 下限截断", async () => {
    render(<ImageViewer src="/large.png" alt="作业" resetKey={1} />);
    const { img } = loadImage(8000, 6000);
    await waitFor(() => expect(img.style.transform).toContain("scale(0.1)"));
  });

  it("操作工具栏时不会开始拖动画布", async () => {
    render(<ImageViewer src="/image.png" alt="作业" resetKey={1} />);
    const { img, wrap } = loadImage(800, 600);
    await waitFor(() => expect(img.style.opacity).toBe("1"));
    const button = wrap.querySelector(".viewer-toolbar button")!;
    fireEvent.pointerDown(button, { clientX: 700, clientY: 500, pointerId: 1 });
    fireEvent.pointerMove(wrap, { clientX: 710, clientY: 510, pointerId: 1 });
    expect(img.style.transform).toContain("translate(0px, 0px)");
  });

  it("相同组件更换图片会重新加载并重置位置", async () => {
    const view = render(<ImageViewer src="/one.png" alt="作业" />);
    const { img } = loadImage(800, 600);
    await waitFor(() => expect(img.style.opacity).toBe("1"));
    view.rerender(<ImageViewer src="/two.png" alt="作业" />);
    expect(screen.getByRole("img", { name: "作业" }).style.opacity).toBe("0");
  });
});

describe("缩小后的定位", () => {
  it("从放大拖动状态缩小到适应窗口时回到中心，工具栏不会残留拖动", async () => {
    const pointerEvent = window.PointerEvent;
    window.PointerEvent = MouseEvent as typeof PointerEvent;
    try {
      render(<ImageViewer src="/pan.png" alt="作业" />);
      const { img, wrap } = loadImage(1600, 1200);
      await waitFor(() => expect(img.style.transform).toContain("scale(0.5)"));
      fireEvent.click(screen.getByRole("button", { name: "放大" }));
      fireEvent.pointerDown(img, { button: 0, clientX: 400, clientY: 300 });
      fireEvent.pointerMove(wrap, { clientX: 450, clientY: 330 });
      fireEvent.pointerUp(wrap);
      expect(img.style.transform).toContain("translate(50px, 30px)");
      const shrink = screen.getByRole("button", { name: "缩小" });
      fireEvent.pointerDown(shrink, { button: 0, clientX: 700, clientY: 500 });
      fireEvent.click(shrink);
      fireEvent.pointerMove(wrap, { clientX: 710, clientY: 510 });
      expect(img.style.transform).toContain("translate(0px, 0px) scale(0.5)");
    } finally {
      window.PointerEvent = pointerEvent;
    }
  });
});
