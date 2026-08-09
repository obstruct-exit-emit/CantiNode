import { useState } from "react";
import { proxiedImage } from "../api";
import { useUi } from "../ui";

// AddResultsGrid renders provider search results as a poster grid — cover
// art, title, subtitle, optional blurb, and a per-card Add button with its
// own progress → added state. Shared by every library's add flow so search
// results look as good as the library itself.
export interface AddResult {
  key: string;
  title: string;
  subtitle?: string;
  blurb?: string;
  imageUrl?: string;
  addLabel: string;
  add: () => Promise<unknown>;
}

export default function AddResultsGrid({
  results,
  onAdded,
}: {
  results: AddResult[];
  onAdded: () => void;
}) {
  const { toast } = useUi();
  const [state, setState] = useState<Record<string, "busy" | "added">>({});

  if (results.length === 0) return null;

  const add = (r: AddResult) => {
    setState((s) => ({ ...s, [r.key]: "busy" }));
    r.add()
      .then(() => {
        setState((s) => ({ ...s, [r.key]: "added" }));
        toast(`Added "${r.title}" to this library`, "ok");
        onAdded();
      })
      .catch((err: unknown) => {
        setState((s) => {
          const next = { ...s };
          delete next[r.key];
          return next;
        });
        toast(String(err instanceof Error ? err.message : err), "bad");
      });
  };

  return (
    <div className="add-grid">
      {results.map((r) => {
        const st = state[r.key];
        return (
          <div key={r.key} className="add-card">
            {r.imageUrl ? (
              <img className="poster" src={proxiedImage(r.imageUrl)} alt="" loading="lazy" />
            ) : (
              <div className="poster fallback">{r.title.charAt(0)}</div>
            )}
            <div className="add-card-body">
              <span className="poster-title" title={r.title}>
                {r.title}
              </span>
              {r.subtitle && (
                <span className="poster-sub" title={r.subtitle}>
                  {r.subtitle}
                </span>
              )}
              {r.blurb && <p className="add-blurb">{r.blurb}</p>}
            </div>
            <button disabled={!!st} onClick={() => add(r)}>
              {st === "added" ? "✓ Added" : st === "busy" ? "Adding…" : r.addLabel}
            </button>
          </div>
        );
      })}
    </div>
  );
}
