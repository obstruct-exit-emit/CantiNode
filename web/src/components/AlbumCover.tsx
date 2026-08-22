import { useState } from "react";
import { api, musicAlbumCoverUrl } from "../api";
import { useUi } from "../ui";

// AlbumCover renders an owned album's cover art — className is "poster"
// (the library/artist page's own poster-grid tiles) or "detail-art" (the
// album page's own large header image); both already have a paired
// ".fallback" modifier in App.css.
//
// Three states: no mbid yet (unmatched — nothing to fetch, plain letter
// tile, no retry action since there's no release to look art up for);
// mbid but the image failed to load (a real fetch attempt came back
// empty, most commonly neither TheAudioDB nor Cover Art Archive having
// this specific release — see internal/coverart) — letter tile plus a
// small retry control layered on top, since this is the one state where
// trying again might actually find something; and the ordinary case,
// just the image. Retry is placed inside this shared component (not left
// to each page) so the album page and every artist page's own albums grid
// get it for free and can't drift out of sync.
export default function AlbumCover({
  albumId,
  mbid,
  title,
  className,
  alt = "",
}: {
  albumId: number;
  mbid: string;
  title: string;
  className: "poster" | "detail-art";
  alt?: string;
}) {
  const { toast } = useUi();
  const [failed, setFailed] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [cacheBust, setCacheBust] = useState(0);

  if (!mbid) {
    return <div className={`${className} fallback`}>{title.charAt(0)}</div>;
  }

  if (!failed) {
    const src = musicAlbumCoverUrl(albumId) + (cacheBust ? `&t=${cacheBust}` : "");
    return <img className={className} src={src} alt={alt} loading="lazy" onError={() => setFailed(true)} />;
  }

  const retry = async (e: React.SyntheticEvent) => {
    // AlbumCover often sits inside another clickable element (a
    // poster-card button that navigates to the album on click) — this
    // must consume the event so retrying doesn't also trigger that.
    e.preventDefault();
    e.stopPropagation();
    if (retrying) return;
    setRetrying(true);
    try {
      const { found } = await api.retryMusicAlbumCover(albumId);
      if (found) {
        setCacheBust(Date.now());
        setFailed(false);
      } else {
        toast(`Still no cover art found for "${title}".`, "info");
      }
    } catch (err) {
      toast(String(err instanceof Error ? err.message : err), "bad");
    } finally {
      setRetrying(false);
    }
  };

  return (
    <div className={`${className} fallback cover-missing`}>
      <span>{title.charAt(0)}</span>
      {/* A real <button> here would get silently hoisted out of an
          enclosing poster-card button by the browser's own HTML parsing
          rules (interactive content can't nest) — role="button" gets the
          same semantics without that. */}
      <span
        className="cover-retry"
        role="button"
        tabIndex={0}
        aria-label="Retry cover art"
        title="Look for cover art again"
        onClick={retry}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") retry(e);
        }}
      >
        {retrying ? "…" : "⟳"}
      </span>
    </div>
  );
}
