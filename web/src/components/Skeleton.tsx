// Skeleton loading states: shimmering placeholders shaped like the content
// they stand in for, instead of bare "Loading…" text.

// PosterGridSkeleton stands in for a library's poster grid.
export function PosterGridSkeleton({ count = 12 }: { count?: number }) {
  return (
    <section className="card" aria-busy="true">
      <div className="skel skel-line" style={{ width: "35%" }} />
      <div className="poster-grid">
        {Array.from({ length: count }, (_, i) => (
          <div key={i} className="skel-poster-card">
            <div className="skel skel-poster" />
            <div className="skel skel-line" />
            <div className="skel skel-line short" />
          </div>
        ))}
      </div>
    </section>
  );
}

// DetailSkeleton stands in for a detail page's header (art + text).
export function DetailSkeleton() {
  return (
    <section className="card detail-head" aria-busy="true">
      <div className="skel skel-art" />
      <div className="detail-info">
        <div className="skel skel-line" style={{ width: "45%", height: "1.4rem" }} />
        <div className="skel skel-line" style={{ width: "30%" }} />
        <div className="skel skel-line" style={{ width: "90%" }} />
        <div className="skel skel-line" style={{ width: "85%" }} />
        <div className="skel skel-line" style={{ width: "60%" }} />
      </div>
    </section>
  );
}

// RowsSkeleton stands in for a list card (queue, calendar, home rows).
export function RowsSkeleton({ rows = 4 }: { rows?: number }) {
  return (
    <section className="card" aria-busy="true">
      <div className="skel skel-line" style={{ width: "30%" }} />
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skel skel-line row-skel" />
      ))}
    </section>
  );
}
