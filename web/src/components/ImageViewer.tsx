import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { Button, Empty, Space, Tooltip } from "antd";
import { ZoomInOutlined, ZoomOutOutlined, ExpandOutlined, ReloadOutlined, FullscreenOutlined } from "@ant-design/icons";

interface Props {
  src: string;
  alt?: string;
  resetKey?: string | number;
}

export default function ImageViewer(props: Props) {
  return <ImageCanvas key={`${props.resetKey ?? ""}:${props.src}`} {...props} />;
}

function ImageCanvas({ src, alt }: Props) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const imgRef = useRef<HTMLImageElement>(null);
  const [view, setView] = useState({ scale: 1, x: 0, y: 0 });
  const [failed, setFailed] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [fitScale, setFitScale] = useState(1);
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);

  const fit = useCallback(() => {
    const wrap = wrapRef.current;
    const img = imgRef.current;
    if (!wrap || !img?.naturalWidth || !wrap.clientWidth || !wrap.clientHeight) return;
    // Small images stay at native size; large images may need less than 20%.
    const scale = Math.min(1, wrap.clientWidth / img.naturalWidth, wrap.clientHeight / img.naturalHeight);
    setFitScale(scale);
    setView({ scale, x: 0, y: 0 });
  }, []);

  useLayoutEffect(() => {
    if (imgRef.current?.complete && imgRef.current.naturalWidth) {
      setLoaded(true);
      fit();
    }
    const wrap = wrapRef.current;
    if (!wrap || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(fit);
    observer.observe(wrap);
    return () => observer.disconnect();
  }, [fit]);

  const zoomAt = useCallback((next: (scale: number) => number, cx?: number, cy?: number) => {
    const wrap = wrapRef.current;
    const img = imgRef.current;
    if (!wrap || !img?.naturalWidth) return;
    const rect = wrap.getBoundingClientRect();
    const mx = (cx ?? rect.left + rect.width / 2) - rect.left - rect.width / 2;
    const my = (cy ?? rect.top + rect.height / 2) - rect.top - rect.height / 2;
    setView((prev) => {
      const scale = Math.min(8, Math.max(Math.min(fitScale, 0.05), next(prev.scale)));
      // Keep the cursor's image point fixed, then center any axis that now fits.
      const boundX = Math.max(0, (img.naturalWidth * scale - wrap.clientWidth) / 2);
      const boundY = Math.max(0, (img.naturalHeight * scale - wrap.clientHeight) / 2);
      return {
        scale,
        x: Math.max(-boundX, Math.min(boundX, mx - (mx - prev.x) * scale / prev.scale)),
        y: Math.max(-boundY, Math.min(boundY, my - (my - prev.y) * scale / prev.scale)),
      };
    });
  }, [fitScale]);

  useEffect(() => {
    const wrap = wrapRef.current;
    if (!wrap) return;
    function onWheel(e: WheelEvent) {
      if ((e.target as HTMLElement).closest(".viewer-toolbar")) return;
      e.preventDefault();
      zoomAt((scale) => scale * (e.deltaY < 0 ? 1.12 : 1 / 1.12), e.clientX, e.clientY);
    }
    wrap.addEventListener("wheel", onWheel, { passive: false });
    return () => wrap.removeEventListener("wheel", onWheel);
  }, [zoomAt]);

  return (
    <div
      className="viewer"
      ref={wrapRef}
      onPointerDown={(e) => {
        if (e.button !== 0 || (e.target as HTMLElement).closest(".viewer-toolbar")) return;
        e.preventDefault();
        e.currentTarget.setPointerCapture?.(e.pointerId);
        drag.current = { x: e.clientX, y: e.clientY, px: view.x, py: view.y };
      }}
      onPointerMove={(e) => {
        if (!drag.current) return;
        const x = drag.current.px + e.clientX - drag.current.x;
        const y = drag.current.py + e.clientY - drag.current.y;
        setView((prev) => ({ ...prev, x, y }));
      }}
      onPointerUp={() => { drag.current = null; }}
      onPointerCancel={() => { drag.current = null; }}
      onLostPointerCapture={() => { drag.current = null; }}
      onDoubleClick={(e) => zoomAt((scale) => scale > fitScale * 1.05 ? fitScale : Math.min(2, fitScale * 2), e.clientX, e.clientY)}
    >
      {failed ? (
        <div className="viewer-empty">
          <Empty description={<span style={{ color: "#8b95a8" }}>图片加载失败</span>} image={Empty.PRESENTED_IMAGE_SIMPLE}>
            <Space>
              <Button icon={<ReloadOutlined />} onClick={() => setFailed(false)}>重新加载</Button>
              <Button type="primary" href={src} target="_blank">打开原图</Button>
            </Space>
          </Empty>
        </div>
      ) : (
        <img
          ref={imgRef}
          src={src}
          alt={alt || "作业图片"}
          draggable={false}
          onLoad={() => { setLoaded(true); fit(); }}
          onError={() => setFailed(true)}
          style={{
            left: "50%",
            top: "50%",
            transform: `translate(-50%, -50%) translate(${view.x}px, ${view.y}px) scale(${view.scale})`,
            opacity: loaded ? 1 : 0,
          }}
        />
      )}
      <div className="viewer-toolbar" onPointerDown={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
        <Tooltip title="缩小"><Button aria-label="缩小" size="small" icon={<ZoomOutOutlined />} onClick={() => zoomAt((scale) => scale / 1.2)} /></Tooltip>
        <Tooltip title="放大"><Button aria-label="放大" size="small" icon={<ZoomInOutlined />} onClick={() => zoomAt((scale) => scale * 1.2)} /></Tooltip>
        <Tooltip title="适应窗口"><Button aria-label="适应窗口" size="small" icon={<ExpandOutlined />} onClick={fit} /></Tooltip>
        <Tooltip title="全屏"><Button aria-label="全屏" size="small" icon={<FullscreenOutlined />} onClick={() => wrapRef.current?.requestFullscreen?.()} /></Tooltip>
      </div>
    </div>
  );
}
