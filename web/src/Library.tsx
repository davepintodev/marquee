import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Cell } from "./Cell";
import { useViewport } from "./useViewport";
import { EmptyState } from "./State";
import { Say, Sk } from "./Skeleton";
import "./skeleton.css";

type Kind = "all" | "movies" | "series";
type Status = "downloading" | "missing";
type Section = "library" | "trending";

const isOn = (t: TitleSummary) => t.badge?.state === "downloading" || t.badge?.state === "caching" || t.badge?.state === "searching" || t.badge?.state === "stalled";
const isMissing = (t: TitleSummary) => t.badge?.state === "missing" || t.badge?.state === "partial_season";

const KINDS: { key: Kind; label: string; test: (t: TitleSummary) => boolean }[] = [
  { key: "all", label: "Library", test: () => true },
  { key: "movies", label: "Movies", test: (t) => t.kind === "movie" },
  { key: "series", label: "Series", test: (t) => t.kind === "series" },
];

const STATUSES: { key: Status; label: string; none: string; test: (t: TitleSummary) => boolean }[] = [
  { key: "downloading", label: "Downloading", none: "Nothing is downloading right now.", test: isOn },
  { key: "missing", label: "Missing", none: "Nothing is missing.", test: isMissing },
];

const NONE: Record<Section, Record<Kind, string>> = {
  library: { all: "Nothing here yet.", movies: "No movies yet.", series: "No series yet." },
  trending: { all: "No trending titles right now.", movies: "No trending movies right now.", series: "No trending series right now." },
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
  const [kind, setKind] = useState<Kind>("all");
  const [status, setStatus] = useState<Status | null>(null);
  const strip = useRef<HTMLDivElement>(null);
  const [bar, setBar] = useState<{ x: number; w: number } | null>(null);
  const [slide, setSlide] = useState(false);
  const { width } = useViewport();
  const cols = colsOf(width);
  const owned = useMemo(() => (items ?? []).filter((t) => t.inLibrary), [items]);
  const trends = useMemo(() => (items ?? []).filter((t) => !t.inLibrary && trending.has(t.id)), [items, trending]);
  const inTrending = section === "trending";
  const pool = inTrending ? trends : owned;
  const kindCounts = useMemo(() => Object.fromEntries(KINDS.map((k) => [k.key, pool.filter(k.test).length])) as Record<Kind, number>, [pool]);
  const statusCounts = useMemo(() => Object.fromEntries(STATUSES.map((s) => [s.key, owned.filter(s.test).length])) as Record<Status, number>, [owned]);
  const activeStatus = inTrending ? undefined : STATUSES.find((s) => s.key === status);
  const activeKind = KINDS.find((k) => k.key === kind)!;
  const shown = useMemo(() => pool.filter((activeStatus ?? activeKind).test), [pool, activeStatus, activeKind]);
  const none = activeStatus?.none ?? NONE[section][kind];
  const loaded = items !== null && items.length > 0;
  const barKey = inTrending ? (kind === "all" ? "" : kind) : (status ?? kind);

  useLayoutEffect(() => {
    const row = strip.current;
    const on = row?.querySelector<HTMLElement>(".chip[data-bar-on]");
    if (row && on) setBar({ x: on.offsetLeft, w: on.offsetWidth });
  }, [barKey, loaded, kindCounts, statusCounts]);

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
  // In Trending the Library chip is the way back, so it is pressed only in the Library section.
  const kindOn = (k: Kind) => (inTrending ? kind === k && k !== "all" : status === null && kind === k);
  const barOn = (b: string) => b === barKey;
  const pickKind = (k: Kind) => {
    setKind(k);
    setStatus(null);
    if (k === "all") setSection("library");
  };

  return (
    <div className="library" aria-label="Library" aria-busy={items === null}>
      {loaded && (
        <div ref={strip} className="chips" role="group" aria-label="Filter the library" style={bar ? ({ "--bar-x": `${bar.x}px`, "--bar-w": `${bar.w}px` } as CSSProperties) : undefined} data-bar={bar && barKey !== "" ? "" : undefined} data-slide={slide || undefined}>
          {KINDS.map((k) => (
            <button key={k.key} type="button" className="chip" aria-pressed={kindOn(k.key)} data-bar-on={barOn(k.key) || undefined} onClick={() => pickKind(k.key)}>
              {k.label}
              {!(inTrending && k.key === "all") && <span>{kindCounts[k.key]}</span>}
            </button>
          ))}
          {STATUSES.map((s) => (
            <button key={s.key} type="button" className="chip" aria-pressed={status === s.key && !inTrending} data-bar-on={barOn(s.key) || undefined} disabled={inTrending} onClick={() => setStatus(s.key)}>
              {s.label}
              {!inTrending && <span>{statusCounts[s.key]}</span>}
            </button>
          ))}
          <button type="button" className="chip chip-section" aria-pressed={inTrending} onClick={() => setSection(inTrending ? "library" : "trending")}>
            Trending
            <span>{trends.length}</span>
          </button>
          <i className="chips-bar" aria-hidden="true" />
        </div>
      )}
      {loaded ? (
        shown.length === 0 ? (
          <EmptyState fit="area">{none}</EmptyState>
        ) : (
          <ul className="tiles" aria-label={inTrending ? "Trending titles" : "Library titles"} style={{ ["--cols" as string]: cols }}>
            {shown.map((t, i) => (
              <li key={`${section}-${barKey}-${t.id}`} style={slot(i)}>
                <Cell api={api} title={t} onSelect={onSelect} />
              </li>
            ))}
          </ul>
        )
      ) : !active ? null : items === null ? (
        <>
          <Say>Loading the library</Say>
          <div className="sk-chips" aria-hidden="true">
            {[44, 64, 56, 84, 60, 64].map((w, i) => (
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
