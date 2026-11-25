import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Cell } from "./Cell";
import { useViewport } from "./useViewport";
import { EmptyState } from "./State";
import { Say, Sk } from "./Skeleton";
import "./skeleton.css";

type Kind = "all" | "movies" | "series";
type Section = "library" | "trending" | "downloading" | "missing";
type Browsable = "library" | "trending";

const isOn = (t: TitleSummary) => t.badge?.state === "downloading" || t.badge?.state === "caching" || t.badge?.state === "searching" || t.badge?.state === "stalled";
const isMissing = (t: TitleSummary) => t.badge?.state === "missing" || t.badge?.state === "partial_season";

const KINDS: { key: Kind; label: string; test: (t: TitleSummary) => boolean }[] = [
  { key: "all", label: "All", test: () => true },
  { key: "movies", label: "Movies", test: (t) => t.kind === "movie" },
  { key: "series", label: "Series", test: (t) => t.kind === "series" },
];

const SECTIONS: { key: Section; label: string }[] = [
  { key: "library", label: "Library" },
  { key: "trending", label: "Trending" },
  { key: "downloading", label: "Downloading" },
  { key: "missing", label: "Missing" },
];

const NONE: Record<Section, Record<Kind, string>> = {
  library: { all: "Nothing here yet.", movies: "No movies yet.", series: "No series yet." },
  trending: { all: "No trending titles right now.", movies: "No trending movies right now.", series: "No trending series right now." },
  downloading: { all: "Nothing is downloading right now.", movies: "", series: "" },
  missing: { all: "Nothing is missing.", movies: "", series: "" },
};

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
  // Ids the last wall fetch listed as trending; a title that was in the library then is never one of them.
  trending?: ReadonlySet<string>;
  onSelect?: (title: TitleSummary) => void;
  notice?: ReactNode;
  active?: boolean;
}

const NO_IDS: ReadonlySet<string> = new Set();

export function Library({ api, items, trending = NO_IDS, onSelect, notice, active = true }: Props) {
  const [section, setSection] = useState<Section>("library");
  const [kinds, setKinds] = useState<Record<Browsable, Kind>>({ library: "all", trending: "all" });
  const strip = useRef<HTMLDivElement>(null);
  const [bar, setBar] = useState<{ x: number; w: number } | null>(null);
  const [slide, setSlide] = useState(false);
  const { width } = useViewport();
  const cols = colsOf(width);
  const owned = useMemo(() => (items ?? []).filter((t) => t.inLibrary), [items]);
  const trends = useMemo(() => (items ?? []).filter((t) => !t.inLibrary && trending.has(t.id)), [items, trending]);
  const downloading = useMemo(() => owned.filter(isOn), [owned]);
  const missing = useMemo(() => owned.filter(isMissing), [owned]);
  const pools: Record<Section, TitleSummary[]> = { library: owned, trending: trends, downloading, missing };
  const pool = pools[section];
  const browsable = section === "library" || section === "trending";
  const kind = browsable ? kinds[section] : "all";
  const kindCounts = useMemo(() => Object.fromEntries(KINDS.map((k) => [k.key, pool.filter(k.test).length])) as Record<Kind, number>, [pool]);
  const shown = useMemo(() => pool.filter(KINDS.find((k) => k.key === kind)!.test), [pool, kind]);
  const none = NONE[section][kind];
  const loaded = items !== null && items.length > 0;

  useLayoutEffect(() => {
    const row = strip.current;
    const on = row?.querySelector<HTMLElement>(".chip[data-bar-on]");
    if (row && on) setBar({ x: on.offsetLeft, w: on.offsetWidth });
  }, [section, loaded, owned, trends]);

  // The first placement is not an animation.
  useEffect(() => {
    if (!bar || slide) return;
    const id = requestAnimationFrame(() => setSlide(true));
    return () => cancelAnimationFrame(id);
  }, [bar, slide]);

  useEffect(() => {
    const row = strip.current;
    if (!row || typeof ResizeObserver === "undefined") return;
    const measure = () => {
      const on = row.querySelector<HTMLElement>(".chip[data-bar-on]");
      if (on) setBar({ x: on.offsetLeft, w: on.offsetWidth });
    };
    const ro = new ResizeObserver(measure);
    ro.observe(row);
    row.querySelectorAll("button").forEach((b) => ro.observe(b));
    return () => ro.disconnect();
  }, [loaded]);

  const slot = (i: number) => ({ ["--d" as string]: (i % cols) * 45 + Math.min(Math.floor(i / cols), STAGGER_ROWS) * 70 });

  return (
    <div className="library" aria-label="Library" aria-busy={items === null}>
      {loaded && (
        <>
          <div ref={strip} className="chips" role="group" aria-label="Filter the library" style={bar ? ({ "--bar-x": `${bar.x}px`, "--bar-w": `${bar.w}px` } as CSSProperties) : undefined} data-bar={bar ? "" : undefined} data-slide={slide || undefined}>
            {SECTIONS.map((c) => (
              <button key={c.key} type="button" className="chip" aria-pressed={section === c.key} data-bar-on={section === c.key || undefined} onClick={() => setSection(c.key)}>
                {c.label}
                <span>{pools[c.key].length}</span>
              </button>
            ))}
            <i className="chips-bar" aria-hidden="true" />
          </div>
          {browsable && (
            <div className="kinds" role="group" aria-label="Movies and series">
              {KINDS.map((k) => (
                <button key={k.key} type="button" aria-pressed={kind === k.key} onClick={() => setKinds((prev) => ({ ...prev, [section]: k.key }))}>
                  {k.label}
                  <span>{kindCounts[k.key]}</span>
                </button>
              ))}
            </div>
          )}
        </>
      )}
      {loaded ? (
        shown.length === 0 ? (
          <EmptyState fit="area">{none}</EmptyState>
        ) : (
          <ul className="tiles" aria-label={section === "trending" ? "Trending titles" : "Library titles"} style={{ ["--cols" as string]: cols }}>
            {shown.map((t, i) => (
              <li key={`${section}-${kind}-${t.id}`} style={slot(i)}>
                <Cell api={api} title={t} onSelect={onSelect} />
              </li>
            ))}
          </ul>
        )
      ) : !active ? null : items === null ? (
        <>
          <Say>Loading the library</Say>
          <div className="sk-chips" aria-hidden="true">
            {[44, 60, 84, 56].map((w, i) => (
              <Sk key={i} shape="line" width={w} />
            ))}
          </div>
          <ul className="tiles" aria-hidden="true" style={{ ["--cols" as string]: cols }}>
            {Array.from({ length: cols * 2 }, (_, i) => (
              <li key={i} className="sk sk-poster" />
            ))}
          </ul>
        </>
      ) : (
        notice
      )}
    </div>
  );
}
