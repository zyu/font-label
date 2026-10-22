import { useCallback, useEffect, useRef, useState } from "react";
import { Button, Empty, Space, Tooltip } from "antd";
import {
  ZoomInOutlined,
  ZoomOutOutlined,
  ExpandOutlined,
  ReloadOutlined,
  FullscreenOutlined,
} from "@ant-design/icons";

interface Props {
  src: string;
  alt?: string;
  resetKey?: string | number;
}

/**
 * Zoomable / pannable image canvas.
 * Wheel zooms around the cursor, drag pans, double click toggles 1x/2x,
 * "适应窗口" fits the image inside the container.
 */
export default function ImageViewer({ src, alt, resetKey }: Props) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const imgRef = useRef<HTMLImageElement>(null);
  const [scale, setScale] = useState(1);
  const [pos, setPos] = useState({ x: 0, y: 0 });
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [fitScale, setFitScale] = useState(1);
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);

  const clamp = (v: number) => Math.min(8, Math.max(0.2, v));

  const fit = useCallback(() => {
    const wrap = wrapRef.current;
    const img = imgRef.current;
    if (!wrap || !img || !img.naturalWidth) return;
    const s = Math.min(wrap.clientWidth / img.naturalWidth, wrap.clientHeight / img.naturalHeight);
    const fs = clamp(s);
    setFitScale(fs);
    setScale(fs);
    setPos({ x: 0, y: 0 });
  }, []);

  useEffect(() => {
    setFailed(false);
    setLoaded(false);
    setScale(1);
    setPos({ x: 0, y: 0 });
    setFitScale(1);
  }, [resetKey]);

  useEffect(() => {
    if (loaded) {
      const t = setTimeout(fit, 30);
      return () => clearTimeout(t);
    }
  }, [loaded, fit]);

  function zoomAt(next: number, cx?: number, cy?: number) {
    const wrap = wrapRef.current;
    const s2 = clamp(next);
    if (!wrap) {
      setScale(s2);
      return;
    }
    const rect = wrap.getBoundingClientRect();
    const mx = (cx ?? rect.left + rect.width / 2) - rect.left - rect.width / 2;
    const my = (cy ?? rect.top + rect.height / 2) - rect.top - rect.height / 2;
    setPos((p) => ({
      x: mx - ((mx - p.x) * s2) / scale,
      y: my - ((my - p.y) * s2) / scale,
    }));
    setScale(s2);
  }

  function onWheel(e: React.WheelEvent) {
    e.preventDefault();
    const factor = e.deltaY < 0 ? 1.12 : 1 / 1.12;
    zoomAt(scale * factor, e.clientX, e.clientY);
  }

  function onPointerDown(e: React.PointerEvent) {
    (e.target as HTMLElement).setPointerCapture?.(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY, px: pos.x, py: pos.y };
  }
  function onPointerMove(e: React.PointerEvent) {
    if (!drag.current) return;
    setPos({ x: drag.current.px + (e.clientX - drag.current.x), y: drag.current.py + (e.clientY - drag.current.y) });
  }
  function onPointerUp() {
    drag.current = null;
  }

  return (
    <div
      className="viewer"
      ref={wrapRef}
      onWheel={onWheel}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={(e) => zoomAt(scale > 1.05 ? fitScale : Math.min(2, fitScale * 2), e.clientX, e.clientY)}
    >
      {failed && (
        <div className="viewer-empty">
          <Empty
            description={<span style={{ color: "#8b95a8" }}>图片加载失败</span>}
            image={Empty.PRESENTED_IMAGE_SIMPLE}
          >
            <Space>
              <Button icon={<ReloadOutlined />} onClick={() => setFailed(false)}>
                重新加载
              </Button>
              <Button type="primary" href={src} target="_blank">
                打开原图
              </Button>
            </Space>
          </Empty>
        </div>
      )}
      {!failed && (
        <img
          ref={imgRef}
          src={src}
          alt={alt || "作业图片"}
          onLoad={() => setLoaded(true)}
          onError={() => setFailed(true)}
          style={{
            left: "50%",
            top: "50%",
            transform: `translate(-50%, -50%) translate(${pos.x}px, ${pos.y}px) scale(${scale})`,
            opacity: loaded ? 1 : 0,
            transition: drag.current ? "none" : "transform .08s ease-out, opacity .15s",
          }}
        />
      )}
      <div className="viewer-toolbar">
        <Tooltip title="缩小 (-)">
          <Button size="small" icon={<ZoomOutOutlined />} onClick={() => zoomAt(scale / 1.2)} />
        </Tooltip>
        <Tooltip title="放大 (+)">
          <Button size="small" icon={<ZoomInOutlined />} onClick={() => zoomAt(scale * 1.2)} />
        </Tooltip>
        <Tooltip title="适应窗口 (0)">
          <Button size="small" icon={<ExpandOutlined />} onClick={fit} />
        </Tooltip>
        <Tooltip title="全屏">
          <Button
            size="small"
            icon={<FullscreenOutlined />}
            onClick={() => wrapRef.current?.requestFullscreen?.()}
          />
        </Tooltip>
      </div>
    </div>
  );
}
