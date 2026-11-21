import { useMemo, useState } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Cell } from "./Cell";
import { useViewport } from "./useViewport";

type Filter = "all" | "movies" | "series" | "downloading" | "missing";

const isOn = (t: TitleSummary) => t.badge?.state === "downloading" || t.badge?.state === "searching" || t.badge?.state === "stalled";
const isMissing = (t: TitleSummary) => t.badge?.state === "missing" || t.badge?.state === "partial_season";

const FILTERS: { key: Filter; label: string; test: (t: TitleSummary) => boolean }[] = [
  { key: "all", label: "All", test: () => true },
  { key: "movies", label: "Movies", test: (t) => t.kind === "movie" },
  { key: "series", label: "Series", test: (t) => t.kind === "series" },
  { key: "downloading", label: "Downloading", test: (t) => t.inLibrary && isOn(t) },
  { key: "missing", label: "Missing", test: (t) => t.inLibrary && isMissing(t) },
];

// Rows below the first screenful share one delay, so a long library never leaves its lower rows blank for seconds.
const STAGGER_ROWS = 4;

export function colsOf(width: number) {
  const w = Math.min(width, 1760);
  const target = Math.min(280, Math.max(150, w / 6));
  return Math.max(2, Math.round((w - 32 + 16) / (target + 16)));
}

interface Props {
  api: Api;
  items: TitleSummary[] | null;
  onSelect?: (title: TitleSummary) => void;
}

export function Library({ api, items, onSelect }: Props) {
  const [filter, setFilter] = useState<Filter>("all");
  const { width } = useViewport();
  const cols = colsOf(width);
  const counts = useMemo(() => Object.fromEntries(FILTERS.map((f) => [f.key, (items ?? []).filter(f.test).length])) as Record<Filter, number>, [items]);
  const shown = useMemo(() => {
    const match = FILTERS.find((f) => f.key === filter)!.test;
    const list = (items ?? []).filter(match);
    return [...list.filter((t) => t.inLibrary), ...list.filter((t) => !t.inLibrary)];
  }, [items, filter]);
  const loaded = items !== null && items.length > 0;

  const slot = (i: number) => ({ ["--d" as string]: (i % cols) * 45 + Math.min(Math.floor(i / cols), STAGGER_ROWS) * 70 });

  return (
    <div className="library" aria-label="Library" aria-busy={items === null}>
      {loaded && (
        <div className="chips" role="group" aria-label="Filter the library">
          {FILTERS.map((f) => (
            <button key={f.key} type="button" className="chip" aria-pressed={filter === f.key} onClick={() => setFilter(f.key)}>
              {f.label}
              <span>{counts[f.key]}</span>
            </button>
          ))}
        </div>
      )}
      {loaded ? (
        <>
          {shown.length === 0 && <p className="hint">Nothing here.</p>}
          <ul className="tiles" style={{ ["--cols" as string]: cols }}>
            {shown.map((t, i) => (
              <li key={`${filter}-${t.id}`} style={slot(i)}>
                <Cell api={api} title={t} onSelect={onSelect} />
              </li>
            ))}
          </ul>
        </>
      ) : (
        <ul className="tiles" aria-hidden="true" style={{ ["--cols" as string]: cols }}>
          {Array.from({ length: cols * 2 }, (_, i) => (
            <li key={i} className="placeholder" />
          ))}
        </ul>
      )}
    </div>
  );
}
