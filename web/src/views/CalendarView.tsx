import { useCallback, useEffect, useState } from "react";
import { api, type CalendarEntry } from "../api";
import { RowsSkeleton } from "../components/Skeleton";

// dayLabel turns a release date into the agenda's group heading: "Today"/
// "Tomorrow"/"Yesterday" near the present, otherwise a weekday + date.
// MusicBrainz dates are calendar dates with no timezone, so everything here
// compares in UTC to match how the backend windows the query (Date.now()
// compared against a bare "2026-09-15" would drift a day around midnight
// in a non-UTC browser otherwise).
function dayLabel(dateStr: string): string {
  const d = new Date(dateStr + "T00:00:00Z");
  if (isNaN(d.getTime())) return dateStr;
  const now = new Date();
  const today = Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate());
  const diffDays = Math.round((d.getTime() - today) / 86_400_000);
  if (diffDays === 0) return "Today";
  if (diffDays === 1) return "Tomorrow";
  if (diffDays === -1) return "Yesterday";
  return d.toLocaleDateString(undefined, {
    timeZone: "UTC",
    weekday: "short",
    month: "short",
    day: "numeric",
    year: d.getUTCFullYear() === now.getUTCFullYear() ? undefined : "numeric",
  });
}

function isToday(dateStr: string): boolean {
  const d = new Date(dateStr + "T00:00:00Z");
  const now = new Date();
  return (
    d.getUTCFullYear() === now.getUTCFullYear() &&
    d.getUTCMonth() === now.getUTCMonth() &&
    d.getUTCDate() === now.getUTCDate()
  );
}

export default function CalendarView({
  onError,
  onOpenArtist,
}: {
  onError: (message: string) => void;
  onOpenArtist: (id: number) => void;
}) {
  const [entries, setEntries] = useState<CalendarEntry[]>([]);
  const [loading, setLoading] = useState(true);

  const reload = useCallback(() => {
    api
      .musicCalendar()
      .then(setEntries)
      .catch((err: unknown) => onError(String(err instanceof Error ? err.message : err)))
      .finally(() => setLoading(false));
  }, [onError]);

  useEffect(reload, [reload]);

  // Entries arrive already sorted by date — group into day buckets without
  // re-sorting, preserving that order.
  const days: { date: string; items: CalendarEntry[] }[] = [];
  for (const e of entries) {
    const last = days[days.length - 1];
    if (last && last.date === e.firstReleaseDate) {
      last.items.push(e);
    } else {
      days.push({ date: e.firstReleaseDate, items: [e] });
    }
  }

  return (
    <section className="card">
      <div className="card-head">
        <h2>Calendar</h2>
        <span className="row-actions">
          <button onClick={reload}>Refresh</button>
        </span>
      </div>
      {loading ? (
        <RowsSkeleton />
      ) : days.length === 0 ? (
        <p className="muted">
          Nothing coming up. This lists releases from your monitored artists —
          add or monitor an artist to start tracking their new albums.
        </p>
      ) : (
        days.map((day) => (
          <div className="calendar-day" key={day.date}>
            <div className={isToday(day.date) ? "calendar-date today" : "calendar-date"}>
              {dayLabel(day.date)}
            </div>
            <ul className="rows">
              {day.items.map((e) => (
                <li key={e.artistId + "/" + e.releaseGroupMbid}>
                  <div className="row">
                    <span>
                      <button className="link" onClick={() => onOpenArtist(e.artistId)}>
                        {e.artistName}
                      </button>
                      {" — "}
                      {e.title}
                    </span>
                    <span className="row-actions">
                      <span className="pill cal-when">{e.primaryType}</span>
                      {e.wantedAlbumId ? <span className="pill rb-retail">wanted</span> : null}
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          </div>
        ))
      )}
    </section>
  );
}
