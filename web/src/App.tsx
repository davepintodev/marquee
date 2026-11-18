import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Title, TitleSummary } from "./api/types";
import { Activity } from "./Activity";
import { Health } from "./Health";
import { Popup } from "./Popup";
import { Results } from "./Results";
import { goTo, useRoute } from "./route";
import { TitleDetail } from "./TitleDetail";
import { useViewport } from "./useViewport";
import { Wall } from "./Wall";

// Waiting this long after the last keystroke keeps one TMDB search per
// pause instead of one per letter.
const TYPING_PAUSE_MS = 200;

const TOAST_MS = 5000;

type Load<T> = { status: "loading" } | { status: "ok"; data: T } | { status: "error"; message: string };

function messageOf(e: unknown) {
  return e instanceof Error ? e.message : "Something went wrong.";
}

function isAbort(e: unknown) {
  return e instanceof DOMException && e.name === "AbortError";
}

export function App({ api }: { api: Api }) {
  const { width, height } = useViewport();
  const [query, setQuery] = useState("");
  const [wall, setWall] = useState<Load<TitleSummary[]>>({ status: "loading" });
  const [wallTry, setWallTry] = useState(0);
  const [answer, setAnswer] = useState<{ query: string; load: Load<TitleSummary[]> }>({ query: "", load: { status: "loading" } });
  const [selected, setSelected] = useState<TitleSummary | null>(null);
  // Titles changed from the popup: the wall and results lists were fetched before.
  const [changed, setChanged] = useState<Record<string, TitleSummary>>({});
  const [toast, setToast] = useState<{ n: number; text: string } | null>(null);
  const bar = useRef<HTMLInputElement>(null);
  const route = useRoute();
  const onPage = route.name !== "wall";
  // Failure here is quiet by design: the pill shows no dot when Health cannot be read.
  const [attention, setAttention] = useState(false);
  const toWall = useCallback(() => goTo({ name: "wall" }), []);

  const q = query.trim();
  const searching = q !== "";

  useEffect(() => {
    const ctl = new AbortController();
    api.wall(ctl.signal).then(
      (items) => !ctl.signal.aborted && setWall({ status: "ok", data: items.map((i) => i.title) }),
      (e) => !ctl.signal.aborted && !isAbort(e) && setWall({ status: "error", message: messageOf(e) }),
    );
    return () => ctl.abort();
  }, [api, wallTry]);

  useEffect(() => {
    if (onPage) return;
    const ctl = new AbortController();
    api.health(ctl.signal).then(
      (h) => !ctl.signal.aborted && setAttention(h.checks.some((c) => c.status === "error")),
      () => !ctl.signal.aborted && setAttention(false),
    );
    return () => ctl.abort();
  }, [api, onPage]);

  useEffect(() => {
    if (!q) return;
    const ctl = new AbortController();
    const timer = setTimeout(() => {
      api.search(q, ctl.signal).then(
        (found) => !ctl.signal.aborted && setAnswer({ query: q, load: { status: "ok", data: found } }),
        (e) => !ctl.signal.aborted && !isAbort(e) && setAnswer({ query: q, load: { status: "error", message: messageOf(e) } }),
      );
    }, TYPING_PAUSE_MS);
    return () => {
      clearTimeout(timer);
      ctl.abort();
    };
  }, [api, q]);

  useEffect(() => {
    if (!toast) return;
    const timer = setTimeout(() => setToast(null), TOAST_MS);
    return () => clearTimeout(timer);
  }, [toast]);

  const clear = useCallback(() => {
    setQuery("");
    bar.current?.focus();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (onPage) toWall();
      else if (selected) setSelected(null);
      else clear();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [clear, selected, onPage, toWall]);

  const current = useCallback((t: TitleSummary) => changed[t.id] ?? t, [changed]);
  const wallTitles = useMemo(() => (wall.status === "ok" ? wall.data.map(current) : null), [wall, current]);
  const results: Load<TitleSummary[]> =
    answer.query !== q ? { status: "loading" } : answer.load.status === "ok" ? { status: "ok", data: answer.load.data.map(current) } : answer.load;
  const open = selected && !onPage ? current(selected) : null;

  const keep = useCallback((t: TitleSummary) => setChanged((c) => ({ ...c, [t.id]: t })), []);
  const notify = useCallback((text: string) => setToast((prev) => ({ n: (prev?.n ?? 0) + 1, text })), []);

  const touch = typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;

  return (
    <div className="app" data-mode={onPage ? "page" : searching ? "search" : "wall"}>
      <Wall api={api} inert={searching || !!open || onPage} items={wallTitles} width={width} height={height} onSelect={setSelected} />

      {!searching && !onPage && wall.status === "error" && (
        <div className="notice" role="alert">
          <p>{wall.message}</p>
          <button type="button" onClick={() => {
              setWall({ status: "loading" });
              setWallTry((n) => n + 1);
            }}>
            Try again
          </button>
        </div>
      )}
      {!searching && !onPage && wall.status === "ok" && wall.data.length === 0 && (
        <div className="notice">
          <p>Nothing to show yet. Search for a title to start.</p>
        </div>
      )}

      <nav className="nav" aria-label="Pages" inert={!!open || onPage || searching}>
        <a className="nav-link" href="#/activity">
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M3 12h4l3 8 4-16 3 8h4" />
          </svg>
          Activity
        </a>
        <a
          className="nav-link"
          href="#/health"
          aria-label={attention ? "Health: something needs attention" : "Health"}
          title={attention ? "Health: something needs attention" : "Health"}
        >
          {attention && <i aria-hidden="true" />}
          Health
        </a>
      </nav>

      <div className="bar" inert={!!open || onPage}>
        <svg className="bar-icon" viewBox="0 0 24 24" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-4-4" />
        </svg>
        <input
          ref={bar}
          role="searchbox"
          type="text"
          aria-label="Search movies and series"
          placeholder="search"
          autoComplete="off"
          autoCapitalize="off"
          spellCheck={false}
          enterKeyHint="search"
          autoFocus={!touch}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
      </div>

      <div className="results" aria-hidden={!searching || onPage} inert={!searching || !!open || onPage}>
        {searching && results.status === "error" && (
          <div className="notice inline" role="alert">
            <p>{results.message}</p>
          </div>
        )}
        {searching && results.status === "loading" && <p className="hint">Searching…</p>}
        {searching && results.status === "ok" && results.data.length === 0 && <p className="hint">Nothing found.</p>}
        {searching && results.status === "ok" && results.data.length > 0 && <Results api={api} results={results.data} onSelect={setSelected} />}
      </div>

      {open && (
        <Popup
          api={api}
          title={open}
          onClose={() => setSelected(null)}
          onChanged={keep}
          onToast={notify}
          onDetails={() => setSelected(null)}
        />
      )}
      {route.name === "title" && (
        <TitleDetail
          key={route.id}
          api={api}
          id={route.id}
          onClose={toWall}
          onChanged={keep}
          onRemoved={(t: Title) => {
            keep({ ...t, inLibrary: false, monitored: undefined, badge: undefined });
            toWall();
            notify(`Removed “${t.title}” from marquee. Its files are still on disk.`);
          }}
          onToast={notify}
        />
      )}
      {route.name === "health" && <Health api={api} onClose={toWall} />}
      {route.name === "activity" && <Activity api={api} onClose={toWall} onChanged={keep} onToast={notify} />}
      {toast && (
        <div className="toast" role="status">
          {toast.text}
        </div>
      )}
    </div>
  );
}
