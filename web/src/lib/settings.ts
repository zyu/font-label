// Settings may contain legacy invalid JSON shapes even when API types say arrays.
export function stringSetting(value: unknown): string[] {
  return Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string" && item.trim().length > 0)
    : [];
}

export function exceptionReasons(value: unknown): { key: string; label: string }[] {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>();
  return value.filter((item): item is { key: string; label: string } => {
    if (!item || typeof item !== "object" || Array.isArray(item)
      || typeof item.key !== "string" || !item.key.trim() || item.key.trim() !== item.key
      || typeof item.label !== "string" || !item.label.trim() || seen.has(item.key)) return false;
    seen.add(item.key);
    return true;
  });
}

const historicalExceptionLabels: Record<string, string> = {
  blur: "严重失焦，笔画糊成一团",
  tilt: "图片倾斜",
  exposure: "曝光异常",
  privacy: "含未打码信息",
  occluded: "文字被遮挡",
  empty: "无有效书写内容或重复图片",
};

export function exceptionReasonLabel(key: string, reasons: unknown): string {
  return exceptionReasons(reasons).find((reason) => reason.key === key)?.label
    ?? (Object.hasOwn(historicalExceptionLabels, key) ? historicalExceptionLabels[key] : key);
}
