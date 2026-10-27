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
