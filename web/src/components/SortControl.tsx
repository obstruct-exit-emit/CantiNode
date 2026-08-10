import type { Book, HomeItem, MusicAlbum, MusicReleaseGroup } from "../api";

// groupBySeries splits an already series-sorted list into consecutive runs by
// series title, so a caller can render a heading before each run. Items with no
// series collect under the "" (standalone) title. Order is preserved.
export function groupBySeries<T>(
  items: T[],
  seriesTitleOf: (t: T) => string,
): { title: string; items: T[] }[] {
  const groups: { title: string; items: T[] }[] = [];
  for (const it of items) {
    const title = seriesTitleOf(it);
    const last = groups[groups.length - 1];
    if (last && last.title === title) last.items.push(it);
    else groups.push({ title, items: [it] });
  }
  return groups;
}

// RELEASE_CATEGORY_ORDER is the fixed display order for grouping a music
// release by type — albums always lead, everything else follows in this
// same order regardless of the active sort key/direction (only the items
// *within* a category move when sorting changes). "Live"/"Compilation"/
// "Soundtrack" come from MusicBrainz's secondary types and take priority
// over the primary type (a live album's primaryType is still "Album") since
// that's the distinction a listener actually cares about when browsing.
export const RELEASE_CATEGORY_ORDER = [
  "Album",
  "EP",
  "Single",
  "Live",
  "Compilation",
  "Soundtrack",
  "Broadcast",
  "Other",
] as const;
export type ReleaseCategory = (typeof RELEASE_CATEGORY_ORDER)[number];

export function releaseCategory(primaryType: string, secondaryTypes: string[] = []): ReleaseCategory {
  const secondary = new Set(secondaryTypes.map((t) => t.toLowerCase()));
  if (secondary.has("live")) return "Live";
  if (secondary.has("compilation")) return "Compilation";
  if (secondary.has("soundtrack")) return "Soundtrack";
  switch (primaryType) {
    case "Album":
    case "EP":
    case "Single":
    case "Broadcast":
      return primaryType;
    default:
      return "Other";
  }
}

// groupByReleaseCategory buckets an already-sorted list by releaseCategory,
// in RELEASE_CATEGORY_ORDER — a stable partition, so each bucket keeps the
// incoming (sorted) relative order of its items. Categories with no items
// are omitted entirely.
export function groupByReleaseCategory<T>(
  items: T[],
  categoryOf: (t: T) => ReleaseCategory,
): { category: ReleaseCategory; items: T[] }[] {
  const buckets = new Map<ReleaseCategory, T[]>();
  for (const it of items) {
    const cat = categoryOf(it);
    const bucket = buckets.get(cat);
    if (bucket) bucket.push(it);
    else buckets.set(cat, [it]);
  }
  return RELEASE_CATEGORY_ORDER.filter((c) => buckets.has(c)).map((c) => ({
    category: c,
    items: buckets.get(c)!,
  }));
}

export type SortDir = "asc" | "desc";

// defaultDirFor is each sort key's own natural reading direction — matches
// what the app already showed before ascending/descending existed as a
// choice: rating and date read highest/newest first, title and series read
// alphabetically first. Picking a key starts here; DirectionSelect flips it.
const DESCENDING_BY_DEFAULT = new Set(["date", "rating"]);
export function defaultDirFor(key: string): SortDir {
  return DESCENDING_BY_DEFAULT.has(key) ? "desc" : "asc";
}

// SortSelect is a compact sort-key dropdown for a card header — a plain select
// styled like the app's other dropdowns. Options are [key, label] pairs; the
// first is the section's natural/default order.
export function SortSelect({
  value,
  onChange,
  options,
}: {
  value: string;
  onChange: (v: string) => void;
  options: [key: string, label: string][];
}) {
  return (
    <select
      className="sort-select"
      aria-label="Sort by"
      title="Sort by"
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {options.map(([key, label]) => (
        <option key={key} value={key}>
          {label}
        </option>
      ))}
    </select>
  );
}

// DirectionButtons is the ascending/descending counterpart to SortSelect — a
// pair of toggle buttons (↓/↑, descending left/ascending right) applying to
// whichever sort key is currently chosen, matching the app's other
// button-pair filters (approved/all, usenet/torrent/direct) rather than a
// dropdown.
export function DirectionButtons({
  value,
  onChange,
}: {
  value: SortDir;
  onChange: (v: SortDir) => void;
}) {
  return (
    <span className="sort-dir-buttons">
      <button
        type="button"
        className={value === "desc" ? "toggle on" : "toggle"}
        title="Descending"
        aria-label="Sort descending"
        onClick={() => onChange("desc")}
      >
        ↓
      </button>
      <button
        type="button"
        className={value === "asc" ? "toggle on" : "toggle"}
        title="Ascending"
        aria-label="Sort ascending"
        onClick={() => onChange("asc")}
      >
        ↑
      </button>
    </span>
  );
}

// sortBooks returns a new array sorted by the given key and direction.
// "default" (or any unknown key) preserves the incoming order — reversible
// too, so "descending" on the default key shows it back-to-front.
export function sortBooks(books: Book[], key: string, dir: SortDir = defaultDirFor(key)): Book[] {
  const by = [...books];
  switch (key) {
    case "series": // by series name, then position; standalones last, by title
      by.sort((a, b) => {
        const sa = a.series?.[0];
        const sb = b.series?.[0];
        if (!sa && !sb) return (a.sortTitle || a.title).localeCompare(b.sortTitle || b.title);
        if (!sa) return 1;
        if (!sb) return -1;
        return sa.title.localeCompare(sb.title) || (sa.position || 0) - (sb.position || 0);
      });
      break;
    case "title":
      by.sort((a, b) => (a.sortTitle || a.title).localeCompare(b.sortTitle || b.title));
      break;
    case "date": // ascending = oldest first
      by.sort((a, b) => (a.releaseDate || "").localeCompare(b.releaseDate || ""));
      break;
    case "rating": // ascending = lowest first
      by.sort((a, b) => a.rating - b.rating);
      break;
    default:
      break;
  }
  return dir === "desc" ? by.reverse() : by;
}

// sortAlbums is the MusicAlbum equivalent of sortBooks, for an artist's
// owned-albums grid.
export function sortAlbums(albums: MusicAlbum[], key: string, dir: SortDir = defaultDirFor(key)): MusicAlbum[] {
  const by = [...albums];
  switch (key) {
    case "title":
      by.sort((a, b) => a.title.localeCompare(b.title));
      break;
    case "date": // ascending = oldest first
      by.sort((a, b) => (a.releaseDate || "").localeCompare(b.releaseDate || ""));
      break;
    default:
      break;
  }
  return dir === "desc" ? by.reverse() : by;
}

// sortReleaseGroups is the MusicReleaseGroup equivalent, for an artist's
// Missing-albums section (cached discography gaps).
export function sortReleaseGroups(
  groups: MusicReleaseGroup[],
  key: string,
  dir: SortDir = defaultDirFor(key),
): MusicReleaseGroup[] {
  const by = [...groups];
  switch (key) {
    case "title":
      by.sort((a, b) => a.title.localeCompare(b.title));
      break;
    case "date": // ascending = oldest first
      by.sort((a, b) => (a.firstReleaseDate || "").localeCompare(b.firstReleaseDate || ""));
      break;
    default:
      break;
  }
  return dir === "desc" ? by.reverse() : by;
}

// sortItems is the HomeItem (Wanted) equivalent of sortBooks.
export function sortItems(items: HomeItem[], key: string, dir: SortDir = defaultDirFor(key)): HomeItem[] {
  const by = [...items];
  switch (key) {
    case "series":
      by.sort((a, b) => {
        const ta = a.seriesTitle || "";
        const tb = b.seriesTitle || "";
        if (!ta && !tb) return a.title.localeCompare(b.title);
        if (!ta) return 1;
        if (!tb) return -1;
        return ta.localeCompare(tb) || (a.seriesPosition || 0) - (b.seriesPosition || 0);
      });
      break;
    case "title":
      by.sort((a, b) => a.title.localeCompare(b.title));
      break;
    case "date": // ascending = oldest first
      by.sort((a, b) => (a.releaseDate || "").localeCompare(b.releaseDate || ""));
      break;
    case "rating": // ascending = lowest first
      by.sort((a, b) => (a.rating || 0) - (b.rating || 0));
      break;
    default:
      break;
  }
  return dir === "desc" ? by.reverse() : by;
}
