import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Badge, LiveEvent, Title, TitleSummary } from "./api/types";
import { Activity } from "./Activity";
import { Front } from "./Front";
import { Health } from "./Health";
import { Library } from "./Library";
import { EmptyState, ErrorState } from "./State";
import { Results, ResultsSkeleton } from "./Results";
import { leaveTitle, leaveTo, parseRoute, pushTo, useRoute } from "./route";
import { TitleScreen } from "./TitleScreen";
import { Toaster, type ToastMsg, type ToastRaise } from "./Toast";
import { useLive } from "./useLive";
import { useViewport } from "./useViewport";

// Waiting this long after the last keystroke keeps one TMDB search per
// pause instead of one per letter.
const TYPING_PAUSE_MS = 200;

type Load<T> = { status: "loading" } | { status: "ok"; data: T } | { status: "error"; message: string };

function messageOf(e: unknown) {
  return e instanceof Error ? e.message : "Something went wrong.";
}

function isAbort(e: unknown) {
  return e instanceof DOMException && e.name === "AbortError";
}

// keys off: the screen is over the app, so the keyboard is not its to act on.
export function App({ api, keys = true, onForget }: { api: Api; keys?: boolean; onForget?: () => void }) {
  const [query, setQuery] = useState("");
  const [wall, setWall] = useState<Load<TitleSummary[]>>({ status: "loading" });
  const [wallTry, setWallTry] = useState(0);
  // Set by a rescan: the wall it fetches next is newer than the edits and events held in `changed` and `live`.
  const wallIsNewer = useRef(false);
  const [searchTry, setSearchTry] = useState(0);
  const [answer, setAnswer] = useState<{ query: string; load: Load<TitleSummary[]> }>({ query: "", load: { status: "loading" } });
  // The title the owner last clicked: the title screen opens on it and uses it when the service does not know the title yet.
  const [selected, setSelected] = useState<TitleSummary | null>(null);
  const openTitle = useCallback((t: TitleSummary) => {
    setSelected(t);
    pushTo({ name: "title", id: t.id }, "page");
  }, []);
  // Titles changed from the title screen: the wall and results lists were fetched before.
  const [changed, setChanged] = useState<Record<string, TitleSummary>>({});
  // Badges the event stream has reported since the lists were fetched; null means the title left the library.
  const [live, setLive] = useState<Record<string, Badge | null>>({});
  const [toast, setToast] = useState<ToastMsg | null>(null);
  const bar = useRef<HTMLInputElement>(null);
  const gridScreen = useRef<HTMLDivElement>(null);
  const touch = typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
  const route = useRoute();
  const { width, height } = useViewport();
  const atFront = route.name === "wall";
  const onPage = route.name === "activity" || route.name === "health" || route.name === "title";
  // Failure here is quiet by design: the Health link shows no dot when Health cannot be read.
  const [attention, setAttention] = useState(false);
  const enter = useCallback((seed = "") => {
    setQuery(seed);
    pushTo({ name: "grid" }, "grid");
  }, []);
  const leave = useCallback(() => leaveTo({ name: "wall" }, "grid"), []);
  // A rescan can adopt titles the wall has not seen; refetching in place keeps the posters on screen meanwhile.
  const refreshWall = useCallback(() => {
    wallIsNewer.current = true;
    setWallTry((n) => n + 1);
  }, []);
  const retryWall = useCallback(() => {
    setWall({ status: "loading" });
    setWallTry((n) => n + 1);
  }, []);
  const closePage = useCallback(() => (parseRoute(window.location.hash).name === "title" ? leaveTitle() : leaveTo({ name: "grid" }, "page")), []);
  const openPage = (name: "activity" | "health") => (e: React.MouseEvent) => {
    e.preventDefault();
    pushTo({ name }, "page");
  };

  const q = atFront ? "" : query.trim();
  const searching = q !== "";

  useEffect(() => {
    const ctl = new AbortController();
    const dropStale = wallIsNewer.current;
    wallIsNewer.current = false;
    api.wall(ctl.signal).then(
      (items) => {
        if (ctl.signal.aborted) return;
        if (dropStale) {
          const ids = new Set(items.map((i) => i.title.id));
          const rest = <T,>(o: Record<string, T>) => Object.fromEntries(Object.entries(o).filter(([id]) => !ids.has(id)));
          setChanged(rest);
          setLive(rest);
        }
        setWall({ status: "ok", data: items.map((i) => i.title) });
      },
      (e) => !ctl.signal.aborted && !isAbort(e) && setWall({ status: "error", message: messageOf(e) }),
    );
    return () => ctl.abort();
  }, [api, wallTry]);

  useEffect(() => {
    if (onPage || atFront) return;
    const ctl = new AbortController();
    api.health(ctl.signal).then(
      (h) => !ctl.signal.aborted && setAttention(h.checks.some((c) => c.status === "error")),
      () => !ctl.signal.aborted && setAttention(false),
    );
    return () => ctl.abort();
  }, [api, onPage, atFront]);

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
  }, [api, q, searchTry]);

  const clear = useCallback(() => {
    setQuery("");
    bar.current?.focus();
  }, []);

  useEffect(() => {
    if (!keys) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (onPage) closePage();
      else if (atFront) return;
      else if (searching) clear();
      else leave();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [keys, clear, onPage, atFront, searching, closePage, leave]);

  useEffect(() => {
    if (!keys || !atFront || onPage) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      const t = e.target;
      const onControl = t instanceof HTMLElement && /^(BUTTON|A|INPUT|TEXTAREA|SELECT)$/.test(t.tagName);
      if (onControl && (e.key === "Enter" || e.key === " ")) return;
      if (e.key !== "Enter" && e.key.length !== 1) return;
      e.preventDefault();
      enter(e.key === "Enter" || e.key.trim() === "" || e.key === "/" ? "" : e.key);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [keys, atFront, onPage, enter]);

  // A control left focused in the grid would swallow the keys that walk in from the wall.
  useEffect(() => {
    if (atFront && gridScreen.current?.contains(document.activeElement)) (document.activeElement as HTMLElement).blur();
  }, [atFront]);

  // The search field takes focus as soon as the grid is showing, so letters typed right after the first
  // one (which opened it) land in the field.
  useEffect(() => {
    if (atFront || touch || onPage) return;
    bar.current?.focus();
  }, [atFront, onPage, touch]);

  // Focus returns to the opener when it can take it, but a poster on the wall cannot (the wall is inert in the grid),
  // so a title's page closing hands it to the same title's tile, when the grid shows one.
  const pageOf = useRef("");
  const onTitle = route.name === "title" ? route.id : "";
  useEffect(() => {
    const left = pageOf.current;
    pageOf.current = onTitle;
    if (!left || onTitle || atFront) return;
    gridScreen.current?.querySelector<HTMLElement>(`.stage:not([inert]) [data-tid="${CSS.escape(left)}"]`)?.focus({ preventScroll: true });
  }, [onTitle, atFront]);

  // The wall is the library first, so a title that joins it is fetched for once the wall is up.
  const wallIds = useRef<Set<string> | null>(null);
  const asked = useRef(new Set<string>());
  useEffect(() => {
    wallIds.current = wall.status === "ok" ? new Set(wall.data.map((t) => t.id)) : null;
    if (wall.status === "ok") asked.current.clear();
  }, [wall]);
  const joinWall = useCallback((id: string) => {
    if (!wallIds.current || wallIds.current.has(id) || asked.current.has(id)) return;
    asked.current.add(id);
    setWallTry((n) => n + 1);
  }, []);

  useLive(api, (e: LiveEvent) => {
    if (e.type === "reconnected") {
      // Events in between were lost, so what the lists show is no longer known.
      setLive({});
      setWallTry((n) => n + 1);
      return;
    }
    const id = e.data.titleId;
    if (e.type === "title" && e.data.badge) joinWall(id);
    const badge: Badge | null =
      e.type === "title"
        ? e.data.badge
        : e.data.state === "caching"
          ? { state: "caching", label: `Caching ${e.data.percent}%`, percent: e.data.percent }
          : { state: "downloading", label: `Downloading ${e.data.percent}%`, percent: e.data.percent };
    setLive((l) => (l[id] === null && e.type === "progress" ? l : { ...l, [id]: badge }));
  });

  const current = useCallback(
    (t: TitleSummary) => {
      const base = changed[t.id] ?? t;
      if (!(t.id in live)) return base;
      const badge = live[t.id];
      return badge ? { ...base, inLibrary: true, monitored: base.monitored ?? true, badge } : { ...base, inLibrary: false, monitored: undefined, badge: undefined };
    },
    [changed, live],
  );
  const wallTitles = useMemo(() => (wall.status === "ok" ? wall.data.map(current) : wall.status === "error" ? [] : null), [wall, current]);
  const results: Load<TitleSummary[]> =
    answer.query !== q ? { status: "loading" } : answer.load.status === "ok" ? { status: "ok", data: answer.load.data.map(current) } : answer.load;

  // What the owner just saw from the service is newer than any event before it.
  const keep = useCallback((t: TitleSummary) => {
    if (t.inLibrary) joinWall(t.id);
    setChanged((c) => ({ ...c, [t.id]: t }));
    setLive((l) => {
      const rest = { ...l };
      delete rest[t.id];
      return rest;
    });
  }, [joinWall]);
  const notify = useCallback<ToastRaise>((text, more) => setToast((prev) => ({ kind: "ok", ...more, text, n: (prev?.n ?? 0) + 1 })), []);
  const dismissToast = useCallback(() => setToast(null), []);

  return (
    <div className="app" data-screen={atFront ? "front" : "grid"} data-mode={onPage ? "page" : searching ? "search" : "library"}>
      <div className="front" inert={!atFront}>
        <Front api={api} items={wallTitles} width={width} height={height} onEnter={() => enter()} onSelect={openTitle} />
        {atFront && wall.status === "error" && (
          <ErrorState action={{ label: "Try again", onClick: retryWall }}>{wall.message}</ErrorState>
        )}
        {atFront && wall.status === "ok" && wall.data.length === 0 && (
          <EmptyState action={{ label: "Add a title", onClick: () => enter() }}>Nothing to show yet.</EmptyState>
        )}
      </div>

      <div ref={gridScreen} className="grid-screen" inert={atFront}>
        <header className="top" inert={onPage}>
          <span className="mark">marquee</span>
          <div className="search">
            <svg className="search-icon" viewBox="0 0 24 24" aria-hidden="true">
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
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            {query && (
              <button type="button" className="search-clear" aria-label="Clear search" onClick={clear}>
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M7 7l10 10M17 7L7 17" />
                </svg>
              </button>
            )}
          </div>
          <nav className="nav" aria-label="Pages">
            <a className="nav-link" href="#/activity" onClick={openPage("activity")}>
              Activity
            </a>
            <a
              className="nav-link"
              href="#/health"
              onClick={openPage("health")}
              aria-label={attention ? "Health: something needs attention" : "Health"}
              title={attention ? "Health: something needs attention" : "Health"}
            >
              {attention && <i aria-hidden="true" />}
              Health
            </a>
          </nav>
        </header>

        <div className="stage library-stage" aria-hidden={searching} inert={searching || onPage}>
          <div className="stage-in">
            <Library
              api={api}
              items={wallTitles}
              onSelect={openTitle}
              active={!atFront && !onPage}
              notice={
                wall.status === "error" ? (
                  <ErrorState action={{ label: "Try again", onClick: retryWall }}>{wall.message}</ErrorState>
                ) : wall.status === "ok" && wall.data.length === 0 ? (
                  <EmptyState>Nothing to show yet. Search for a title to start.</EmptyState>
                ) : undefined
              }
            />
          </div>
        </div>

        <div className="stage results-stage" aria-hidden={!searching || onPage} inert={!searching || onPage}>
          <div className="stage-in">
            {searching && results.status === "error" && (
              <ErrorState
                action={{
                  label: "Try again",
                  onClick: () => {
                    setAnswer({ query: "", load: { status: "loading" } });
                    setSearchTry((n) => n + 1);
                  },
                }}
              >
                {results.message}
              </ErrorState>
            )}
            {searching && results.status === "loading" && <ResultsSkeleton />}
            {searching && results.status === "ok" && results.data.length === 0 && <EmptyState>Nothing found for “{q}”.</EmptyState>}
            {searching && results.status === "ok" && results.data.length > 0 && <Results api={api} results={results.data} onSelect={openTitle} />}
          </div>
        </div>
      </div>

      {route.name === "title" && (
        <TitleScreen
          key={route.id}
          api={api}
          id={route.id}
          seed={selected?.id === route.id ? current(selected) : undefined}
          onClose={closePage}
          onChanged={keep}
          onRemoved={(t: Title, deletedFiles: boolean) => {
            keep({ ...t, inLibrary: false, monitored: undefined, badge: undefined });
            closePage();
            notify(deletedFiles ? `Removed “${t.title}” from marquee and deleted its files from disk.` : `Removed “${t.title}” from marquee. Its files are still on disk.`);
          }}
          onToast={notify}
          onOpenTitle={openTitle}
        />
      )}
      {route.name === "health" && <Health api={api} onClose={closePage} onForget={onForget} onToast={notify} onRescanned={refreshWall} />}
      {route.name === "activity" && <Activity api={api} onClose={closePage} onChanged={keep} onToast={notify} />}
      <Toaster toast={toast} onDismiss={dismissToast} />
    </div>
  );
}
