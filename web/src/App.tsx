import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Popup } from "./Popup";
import { Results } from "./Results";
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
      if (selected) setSelected(null);
      else clear();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [clear, selected]);

  const current = useCallback((t: TitleSummary) => changed[t.id] ?? t, [changed]);
  const wallTitles = useMemo(() => (wall.status === "ok" ? wall.data.map(current) : null), [wall, current]);
  const results: Load<TitleSummary[]> =
    answer.query !== q ? { status: "loading" } : answer.load.status === "ok" ? { status: "ok", data: answer.load.data.map(current) } : answer.load;
  const open = selected ? current(selected) : null;

  const touch = typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;

  return (
    <div className="app" data-mode={searching ? "search" : "wall"}>
      <Wall api={api} inert={searching || !!open} items={wallTitles} width={width} height={height} onSelect={setSelected} />

      {!searching && wall.status === "error" && (
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
      {!searching && wall.status === "ok" && wall.data.length === 0 && (
        <div className="notice">
          <p>Nothing to show yet. Search for a title to start.</p>
        </div>
      )}

      <div className="bar" inert={!!open}>
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

      <div className="results" aria-hidden={!searching} inert={!searching || !!open}>
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
          onChanged={(t) => setChanged((c) => ({ ...c, [t.id]: t }))}
          onToast={(text) => setToast((prev) => ({ n: (prev?.n ?? 0) + 1, text }))}
        />
      )}
      {toast && (
        <div className="toast" role="status">
          {toast.text}
        </div>
      )}
    </div>
  );
}
