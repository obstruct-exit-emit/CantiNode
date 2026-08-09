import { useEffect, useState } from "react";
import { api, type CalendarItem } from "../api";
import { libraryLabels } from "../App";
import { RowsSkeleton } from "../components/Skeleton";

const typeIcons: Record<string, string> = {
  ebook: "📖",
  audiobook: "🎧",
  manga: "🀄",
  comic: "💥",
  magazine: "📰",
};

// Agenda-style calendar: releases across all libraries grouped by date,
// today highlighted, every row clickable — prose books open their book page,
// volumes/issues their series page.
export default function CalendarView({
  onError,
  onOpenBook,
  onOpenSeries,
}: {
  onError: (message: string) => void;
  onOpenBook: (item: CalendarItem) => void;
  onOpenSeries: (item: CalendarItem) => void;
}) {
  const [items, setItems] = useState<CalendarItem[] | null>(null);

  useEffect(() => {
    api
      .calendar()
      .then((r) => setItems(r.items))
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)));
  }, [onError]);

  if (!items) return <RowsSkeleton rows={6} />;

  const today = new Date().toISOString().slice(0, 10);
  const byDate = new Map<string, CalendarItem[]>();
  for (const item of items) {
    const list = byDate.get(item.releaseDate) ?? [];
    list.push(item);
    byDate.set(item.releaseDate, list);
  }
  const dates = [...byDate.keys()].sort();
  const upcoming = dates.filter((d) => d >= today);
  const past = dates.filter((d) => d < today).reverse();

  const dayLabel = (iso: string) => {
    const d = new Date(iso + "T00:00:00");
    const diff = Math.round((d.getTime() - new Date(today + "T00:00:00").getTime()) / 86_400_000);
    const text = d.toLocaleDateString(undefined, {
      weekday: "short",
      month: "long",
      day: "numeric",
    });
    if (diff === 0) return { text, badge: "today" };
    if (diff === 1) return { text, badge: "tomorrow" };
    if (diff === -1) return { text, badge: "yesterday" };
    if (diff > 1 && diff <= 30) return { text, badge: `in ${diff}d` };
    if (diff < -1 && diff >= -30) return { text, badge: `${-diff}d ago` };
    return { text: `${text}, ${d.getFullYear()}`, badge: "" };
  };

  const open = (item: CalendarItem) => {
    // Prose books have an author page behind them; everything else lives on
    // its series page. Items missing both ids stay unclickable.
    if (item.mediaType === "ebook" || item.mediaType === "audiobook") {
      if (item.authorId) onOpenBook(item);
    } else if (item.seriesId) {
      onOpenSeries(item);
    }
  };

  const clickable = (item: CalendarItem) =>
    item.mediaType === "ebook" || item.mediaType === "audiobook"
      ? !!item.authorId
      : !!item.seriesId;

  const section = (title: string, list: string[]) =>
    list.length > 0 && (
      <section className="card">
        <h2>{title}</h2>
        {list.map((date) => {
          const { text, badge } = dayLabel(date);
          return (
            <div key={date} className="calendar-day">
              <h3 className={date === today ? "calendar-date today" : "calendar-date"}>
                {text}
                {badge && <span className="pill cal-when">{badge}</span>}
              </h3>
              <ul className="rows">
                {byDate.get(date)!.map((item) => (
                  <li key={`${item.mediaType}-${item.bookId}`}>
                    <div className="row">
                      {clickable(item) ? (
                        <button className="link cal-item" onClick={() => open(item)}>
                          {typeIcons[item.mediaType] ?? "📚"} {item.title}
                          {item.subtitle && <span className="muted"> · {item.subtitle}</span>}
                        </button>
                      ) : (
                        <span>
                          {typeIcons[item.mediaType] ?? "📚"} {item.title}
                          {item.subtitle && <span className="muted"> · {item.subtitle}</span>}
                        </span>
                      )}
                      <span className="row-actions">
                        <span className="muted">{libraryLabels[item.mediaType] ?? item.mediaType}</span>
                        <span className={item.owned ? "owned yes" : "owned no"}>
                          {item.owned ? "owned" : "wanted"}
                        </span>
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            </div>
          );
        })}
      </section>
    );

  return (
    <>
      {items.length === 0 && (
        <section className="card">
          <h2>Calendar</h2>
          <p className="muted">
            Nothing dated in the next 90 days (or the last 30). Release dates
            come from the metadata provider as books and volumes are added or
            refreshed.
          </p>
        </section>
      )}
      {section("Upcoming", upcoming)}
      {section("Recently released", past)}
    </>
  );
}
