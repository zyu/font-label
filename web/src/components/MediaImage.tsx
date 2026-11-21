import { useCallback, useEffect, useState, type ComponentProps } from "react";

// The API renews signatures on reads. The path still names the same asset.
export function mediaAssetKey(src: string) {
  return /^\/api\/v1\/media\//.test(src) ? src.split("?")[0] : src;
}

export function useMediaSource(latest: string) {
  const [src, setSrc] = useState(latest);
  const refresh = useCallback(() => setSrc(latest), [latest]);
  return { src, refresh };
}

type MediaImageProps = Omit<ComponentProps<"img">, "src"> & { src: string };

function StableImage({ src: latest, onError, ...props }: MediaImageProps) {
  const { src, refresh } = useMediaSource(latest);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (failed && src !== latest) {
      refresh();
      setFailed(false);
    }
  }, [failed,latest,src,refresh]);
  return <img {...props} src={src} onError={(event) => {
    if (src !== latest) refresh();
    else {
      setFailed(true);
      onError?.(event);
    }
  }} />;
}

export default function MediaImage(props: MediaImageProps) {
  return <StableImage key={mediaAssetKey(props.src)} {...props} />;
}
