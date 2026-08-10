// Shared display formatting — one implementation for every view.

// formatBytes renders a byte count in the most readable binary unit.
// Unknown/zero sizes render as "" so call sites can fall back ("—").
export function formatBytes(bytes: number): string {
  if (!bytes || bytes <= 0 || !Number.isFinite(bytes)) return "";
  if (bytes >= 1 << 30) return `${(bytes / (1 << 30)).toFixed(1)} GiB`;
  if (bytes >= 1 << 20) return `${(bytes / (1 << 20)).toFixed(1)} MiB`;
  return `${Math.max(1, Math.round(bytes / 1024))} KiB`;
}

// formatDuration renders a millisecond duration as "m:ss" (or "h:mm:ss" past
// an hour). Unknown/zero durations render as "".
export function formatDuration(ms: number): string {
  if (!ms || ms <= 0 || !Number.isFinite(ms)) return "";
  const totalSeconds = Math.round(ms / 1000);
  const h = Math.floor(totalSeconds / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = totalSeconds % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

// relativeTime renders how long ago an ISO timestamp was ("3h ago",
// "2d ago"); future or unparseable input renders as "".
export function relativeTime(iso?: string): string {
  if (!iso) return "";
  const ms = Date.now() - new Date(iso).getTime();
  if (isNaN(ms) || ms < 0) return "";
  const min = ms / 60_000;
  if (min < 1) return "just now";
  if (min < 60) return `${Math.floor(min)}m ago`;
  const h = min / 60;
  if (h < 24) return `${Math.floor(h)}h ago`;
  const d = h / 24;
  if (d < 30) return `${Math.floor(d)}d ago`;
  if (d < 365) return `${Math.floor(d / 30)}mo ago`;
  return `${Math.floor(d / 365)}y ago`;
}
