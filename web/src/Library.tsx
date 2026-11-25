import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Cell } from "./Cell";
import { useViewport } from "./useViewport";
import { EmptyState } from "./State";
import { Say, Sk } from "./Skeleton";
import "./skeleton.css";

type Filter = "all" | "movies" | "series" | "downloading" | "missing";

const isOn = (t: TitleSummary) => t.badge?.state === "downloading" || t.badge?.state === "caching" || t.badge?.state === "searching" || t.badge?.state === "stalled";
const isMissing = (t: TitleSummary) => t.badge?.state === "missing" || t.badge?.state === "partial_season";

const FILTERS: { key: Filter; label: string; none: string; test: (t: TitleSummary) => boolean }[] = [
  { key: "all", label: "All", none: "Nothing here yet.", test: () => true },
  { key: "movies", label: "Movies", none: "No movies yet.", test: (t) => t.kind === "movie" },
  { key: "series", label: "Series", none: "No series yet.", test: (t) => t.kind === "series" },
  { key: "downloading", label: "Downloading", none: "Nothing is downloading right now.", test: (t) => t.inLibrary && isOn(t) },
  { key: "missing", label: "Missing", none: "Nothing is missing.", test: (t) => t.inLibrary && isMissing(t) },
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
  notice?: ReactNode;
  active?: boolean;
}

export function Library({ api, items, onSelect, notice, active = true }: Props) {
  const [filter, setFilter] = useState<Filter>("all");
  const strip = useRef<HTMLDivElement>(null);
  const [bar, setBar] = useState<{ x: number; w: number } | null>(null);
  const [slide, setSlide] = useState(false);
  const { width } = useViewport();
  const cols = colsOf(width);
  const counts = useMemo(() => Object.fromEntries(FILTERS.map((f) => [f.key, (items ?? []).filter(f.test).length])) as Record<Filter, number>, [items]);
  const shown = useMemo(() => {
    const match = FILTERS.find((f) => f.key === filter)!.test;
    const list = (items ?? []).filter(match);
    return [...list.filter((t) => t.inLibrary), ...list.filter((t) => !t.inLibrary)];
  }, [items, filter]);
  const loaded = items !== null && items.length > 0;

  useLayoutEffect(() => {
    const row = strip.current;
    const on = row?.querySelector<HTMLElement>('[aria-pressed="true"]');
    if (row && on) setBar({ x: on.offsetLeft, w: on.offsetWidth });
  }, [filter, loaded, counts]);

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
      const on = row.querySelector<HTMLElement>('[aria-pressed="true"]');
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
        <div ref={strip} className="chips" role="group" aria-label="Filter the library" style={bar ? ({ "--bar-x": `${bar.x}px`, "--bar-w": `${bar.w}px` } as CSSProperties) : undefined} data-bar={bar ? "" : undefined} data-slide={slide || undefined}>
          {FILTERS.map((f) => (
            <button key={f.key} type="button" className="chip" aria-pressed={filter === f.key} onClick={() => setFilter(f.key)}>
              {f.label}
              <span>{counts[f.key]}</span>
            </button>
          ))}
          <i className="chips-bar" aria-hidden="true" />
        </div>
      )}
      {loaded ? (
        <>
          {shown.length === 0 ? (
            <EmptyState fit="area">{FILTERS.find((f) => f.key === filter)!.none}</EmptyState>
          ) : (
            <ul className="tiles" style={{ ["--cols" as string]: cols }}>
              {shown.map((t, i) => (
                <li key={`${filter}-${t.id}`} style={slot(i)}>
                  <Cell api={api} title={t} onSelect={onSelect} />
                </li>
              ))}
            </ul>
          )}
        </>
      ) : !active ? null : items === null ? (
        <>
          <Say>Loading the library</Say>
          <div className="sk-chips" aria-hidden="true">
            {[44, 64, 56, 84, 60].map((w, i) => (
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
