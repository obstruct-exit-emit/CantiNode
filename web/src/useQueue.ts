import { useCallback, useEffect, useRef, useState } from "react";
import { api, type QueueItem } from "./api";

// useQueue polls the download queue while the component is mounted and maps
// items to their books. The server serves a shared 15-second snapshot, so a
// page-level poll is one cheap request no matter how many rows use it.
// refresh() jumps the poll — called right after a grab so the new download's
// badge appears immediately instead of on the next tick.
export function useQueue(pollMs = 12_000) {
  const [items, setItems] = useState<QueueItem[]>([]);
  const [tick, setTick] = useState(0);
  const stopped = useRef(false);

  useEffect(() => {
    stopped.current = false;
    const check = () =>
      api
        .queue()
        .then((q) => {
          if (!stopped.current) setItems(q.items);
        })
        .catch(() => {}); // transient queue errors: keep the last state
    check();
    const timer = setInterval(check, pollMs);
    return () => {
      stopped.current = true;
      clearInterval(timer);
    };
  }, [pollMs, tick]);

  const refresh = useCallback(() => setTick((t) => t + 1), []);

  // activeFor returns the live download for a book+format, if any — failed
  // items don't count (the book is back to wanted).
  const activeFor = useCallback(
    (bookId: number, mediaType: string) =>
      items.find(
        (it) => it.bookId === bookId && it.mediaType === mediaType && it.status !== "failed",
      ) ?? null,
    [items],
  );

  return { items, refresh, activeFor };
}

// DownloadState renders as "downloading · 42%" on badges.
export function downloadPct(it: QueueItem): string {
  return `${Math.max(0, Math.min(100, Math.round(it.progress * 100)))}%`;
}
