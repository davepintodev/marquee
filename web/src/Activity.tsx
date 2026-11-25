import { useCallback, useEffect, useRef, useState, type CSSProperties } from "react";
import type { Api } from "./api/client";
import type { Download, HistoryEntry, LiveEvent, TitleSummary } from "./api/types";
import { galleryColumns, summaryOf } from "./activity";
import { Carousel, DownloadCard, WaitingCard, type Upcoming } from "./Gallery";
import { Page } from "./Page";
import { Timeline } from "./Timeline";
import { useLive } from "./useLive";
import { Notice } from "./Notice";
import { EmptyState, ErrorState } from "./State";
import type { ToastRaise } from "./Toast";
import { ActivitySkeleton } from "./Skeleton";

interface Props {
  api: Api;
  onClose: () => void;
  onChanged: (title: TitleSummary) => void;
  onToast: ToastRaise;
}

interface Waiting {
  id: string;
  name: string;
  note: string;
  label: string;
}

interface Data {
  downloads: Download[];
  history: HistoryEntry[];
  // The cursor of the page after the loaded history; absent when it is all loaded.
  next?: string;
  // Older pages are loaded, so a refetch of the newest page must keep them.
  older: boolean;
  waiting: Waiting[];
}

const PAGE = 100;

// A refetch brings the newest page; entries of the older pages already loaded stay below it, once each.
// When the newest page does not reach them (more than a page arrived meanwhile), keeping them would hide
// the entries in between, so the list starts over from the newest page.
function merged(prev: Data | undefined, fresh: Data): Data {
  if (!prev?.older || !fresh.next) return fresh;
  const seen = new Set(fresh.history.map((h) => h.id));
  if (!prev.history.some((h) => seen.has(h.id))) return fresh;
  return { ...fresh, history: [...fresh.history, ...prev.history.filter((h) => !seen.has(h.id))], next: prev.next, older: true };
}

type Load = { status: "loading" } | { status: "ok"; data: Data } | { status: "error"; message: string };

const messageOf = (e: unknown) => (e instanceof Error ? e.message : "Something went wrong.");
const isAbort = (e: unknown) => e instanceof DOMException && e.name === "AbortError";

const BURST = 250;

function waitingOf(library: TitleSummary[]): Waiting[] {
  return library.flatMap((t): Waiting[] => {
    const episodes = t.searchingEpisodes ?? [];
    if (episodes.length > 0) return [{ id: t.id, name: t.title, note: `Waiting for ${episodes.map((e) => e.toUpperCase()).join(", ")} to appear.`, label: t.badge?.state === "searching" ? t.badge.label : "Searching" }];
    if (t.badge?.state === "searching") return [{ id: t.id, name: t.title, note: "Waiting for a release to appear.", label: t.badge.label }];
    return [];
  });
}

export function Activity({ api, onClose, onChanged, onToast }: Props) {
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [retrying, setRetrying] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const working = useRef(false);
  const pending = useRef<ReturnType<typeof setTimeout>>(undefined);

  const fetchAll = useCallback(
    async (signal?: AbortSignal): Promise<Data> => {
      const [downloads, page, library] = await Promise.all([api.downloads(signal), api.history({ limit: PAGE }, signal), api.library(signal)]);
      return { downloads, history: page.entries, next: page.next, older: false, waiting: waitingOf(library) };
    },
    [api],
  );

  useEffect(() => {
    const ctl = new AbortController();
    fetchAll(ctl.signal).then(
      (data) => !ctl.signal.aborted && setLoad({ status: "ok", data }),
      (e) => !ctl.signal.aborted && !isAbort(e) && setLoad({ status: "error", message: messageOf(e) }),
    );
    return () => ctl.abort();
  }, [fetchAll, attempt]);

  const refetch = useCallback(() => {
    fetchAll().then(
      (data) => setLoad((l) => ({ status: "ok", data: merged(l.status === "ok" ? l.data : undefined, data) })),
      () => {},
    );
  }, [fetchAll]);

  // The server emits a burst of events for one change; they share one refetch.
  const refetchSoon = useCallback(() => {
    clearTimeout(pending.current);
    pending.current = setTimeout(refetch, BURST);
  }, [refetch]);
  useEffect(() => () => clearTimeout(pending.current), []);

  useLive(api, (e: LiveEvent) => {
    if (e.type !== "progress") return refetchSoon();
    const p = e.data;
    if (load.status !== "ok" || !load.data.downloads.some((d) => d.id === p.downloadId)) return refetchSoon();
    setLoad((l) =>
      l.status !== "ok"
        ? l
        : {
            status: "ok",
            data: {
              ...l.data,
              downloads: l.data.downloads.map((d) => (d.id === p.downloadId ? { ...d, state: p.state, percent: p.percent, bytesDone: p.bytesDone, bytesTotal: p.bytesTotal } : d)),
            },
          },
    );
  });

  const retry = async (h: HistoryEntry) => {
    if (working.current) return;
    working.current = true;
    setRetrying(h.id);
    setError(null);
    try {
      const title = await api.retry(h.id);
      onChanged(title);
      onToast(`Searching for “${h.titleName}” again.`);
      const data = await fetchAll();
      setLoad((l) => ({ status: "ok", data: merged(l.status === "ok" ? l.data : undefined, data) }));
    } catch (e) {
      onToast(messageOf(e), { kind: "error", action: { label: "Try again", onClick: () => retry(h) } });
    } finally {
      working.current = false;
      setRetrying(null);
    }
  };

  const loadOlder = async (cursor: string) => {
    if (working.current) return;
    working.current = true;
    setError(null);
    try {
      const page = await api.history({ limit: PAGE, cursor });
      // A refetch that landed meanwhile moved the cursor; the click asks again.
      setLoad((l) => {
        if (l.status !== "ok" || l.data.next !== cursor) return l;
        const seen = new Set(l.data.history.map((h) => h.id));
        return { status: "ok", data: { ...l.data, history: [...l.data.history, ...page.entries.filter((h) => !seen.has(h.id))], next: page.next, older: true } };
      });
    } catch (e) {
      setError(messageOf(e));
    } finally {
      working.current = false;
    }
  };

  if (load.status !== "ok") {
    return (
      <Page heading="Activity" onClose={onClose} className="page-activity">
        {load.status === "loading" && <ActivitySkeleton />}
        {load.status === "error" && (
          <ErrorState
            fit="page"
            action={{
              label: "Try again",
              onClick: () => {
                setLoad({ status: "loading" });
                setAttempt((n) => n + 1);
              },
            }}
          >
            {load.message}
          </ErrorState>
        )}
      </Page>
    );
  }

  const { downloads, history } = load.data;
  const busy = new Set(downloads.map((d) => d.titleId));
  const running = downloads.filter((d) => d.state !== "queued");
  const upcoming: Upcoming[] = [
    ...downloads
      .filter((d) => d.state === "queued")
      .map((d): Upcoming => ({ key: `download-${d.id}`, id: d.titleId, name: d.titleName, word: d.episode ? `${d.episode.toUpperCase()} queued` : "Queued", state: "queued" })),
    ...load.data.waiting.filter((w) => !busy.has(w.id)).map((w): Upcoming => ({ key: `title-${w.id}`, id: w.id, name: w.name, word: w.label, note: w.note, state: "searching" })),
  ];
  if (downloads.length === 0 && upcoming.length === 0 && history.length === 0) {
    return (
      <Page heading="Activity" onClose={onClose} className="page-activity">
        <EmptyState fit="page">Nothing is happening yet. Downloads, searches and what they found will show up here.</EmptyState>
      </Page>
    );
  }
  const summary = summaryOf({ downloading: running.length, waiting: upcoming.length, retryable: history.filter((h) => h.retryable).length });
  return (
    <Page heading="Activity" onClose={onClose} className="page-activity">
      <p className="board-sum">{summary}</p>
      {error && <Notice tone="error">{error}</Notice>}
      <div className="board-cols" style={{ "--n": galleryColumns(running.length, upcoming.length) } as CSSProperties}>
        <div className="board-main">
          <section className="section board-section" aria-labelledby="act-downloading">
            <h2 id="act-downloading">Downloading</h2>
            <p className="board-hint">Files arriving right now.</p>
            {running.length === 0 ? (
              <Notice tone="quiet">Nothing is downloading.</Notice>
            ) : (
              <Carousel label="downloads">
                {running.map((d) => (
                  <DownloadCard key={d.id} api={api} d={d} />
                ))}
              </Carousel>
            )}
          </section>
          {upcoming.length > 0 && (
            <section className="section board-section" aria-labelledby="act-waiting">
              <h2 id="act-waiting">Waiting</h2>
              <p className="board-hint">Queued behind a download, or searching for a release.</p>
              <ul className="strip" aria-label="Waiting">
                {upcoming.map((n) => (
                  <WaitingCard key={n.key} api={api} n={n} />
                ))}
              </ul>
            </section>
          )}
        </div>
        <section className="section board-section" aria-labelledby="act-history">
          <h2 id="act-history">History</h2>
          <p className="board-hint">What finished and what failed.</p>
          {history.length === 0 ? <Notice tone="quiet">Nothing has happened yet.</Notice> : <Timeline entries={history} retrying={retrying} onRetry={retry} />}
          {load.data.next && (
            <button type="button" className="link-btn more" onClick={() => loadOlder(load.data.next as string)}>
              Show older
            </button>
          )}
        </section>
      </div>
    </Page>
  );
}
