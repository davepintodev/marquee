import { useCallback, useEffect, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Download, HistoryEntry, LiveEvent, Title, TitleSummary } from "./api/types";
import { HistoryList } from "./HistoryList";
import { Page } from "./Page";
import { hrefOf } from "./route";
import { useLive } from "./useLive";
import { bytesOf } from "./words";

interface Props {
  api: Api;
  onClose: () => void;
  onChanged: (title: TitleSummary) => void;
  onToast: (text: string) => void;
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
function merged(prev: Data | undefined, fresh: Data): Data {
  if (!prev?.older) return fresh;
  const seen = new Set(fresh.history.map((h) => h.id));
  return { ...fresh, history: [...fresh.history, ...prev.history.filter((h) => !seen.has(h.id))], next: prev.next, older: true };
}

type Load = { status: "loading" } | { status: "ok"; data: Data } | { status: "error"; message: string };

const messageOf = (e: unknown) => (e instanceof Error ? e.message : "Something went wrong.");
const isAbort = (e: unknown) => e instanceof DOMException && e.name === "AbortError";

const stateWords: Record<Download["state"], string> = {
  queued: "Queued",
  downloading: "Downloading",
  verifying: "Verifying",
  importing: "Importing",
};

function searchingEpisodes(t: Title) {
  return t.seasons.flatMap((s) => s.episodes.filter((e) => e.monitored && e.state === "searching").map((e) => e.id.toUpperCase()));
}

export function Activity({ api, onClose, onChanged, onToast }: Props) {
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [retrying, setRetrying] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const working = useRef(false);
  // A title's badge says nothing about one searching episode, so a title retried here is asked about directly until it stops searching.
  const retried = useRef(new Set<string>());

  const fetchAll = useCallback(
    async (signal?: AbortSignal): Promise<Data> => {
      const asked = [...retried.current];
      const [downloads, page, library, titles] = await Promise.all([
        api.downloads(signal),
        api.history({ limit: PAGE }, signal),
        api.library(signal),
        Promise.all(asked.map((id) => api.title(id, signal).catch(() => null))),
      ]);
      const waiting: Waiting[] = library
        .filter((t) => t.badge?.state === "searching")
        .map((t) => ({ id: t.id, name: t.title, note: "Waiting for a release to appear.", label: t.badge?.label ?? "Searching" }));
      titles.forEach((t, i) => {
        const episodes = t ? searchingEpisodes(t) : [];
        if (episodes.length === 0) retried.current.delete(asked[i]);
        else if (t && !waiting.some((w) => w.id === t.id)) waiting.push({ id: t.id, name: t.title, note: `Waiting for ${episodes.join(", ")} to appear.`, label: "Searching" });
      });
      return { downloads, history: page.entries, next: page.next, older: false, waiting };
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

  useLive(api, (e: LiveEvent) => {
    if (e.type !== "progress") return refetch();
    const p = e.data;
    if (load.status !== "ok" || !load.data.downloads.some((d) => d.id === p.downloadId)) return refetch();
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
      if (title.kind === "series") retried.current.add(title.id);
      onChanged(title);
      onToast(`Searching for “${h.titleName}” again.`);
      const data = await fetchAll();
      setLoad((l) => ({ status: "ok", data: merged(l.status === "ok" ? l.data : undefined, data) }));
    } catch (e) {
      setError(messageOf(e));
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
      <Page heading="Activity" onClose={onClose} wide>
        {load.status === "loading" && <p className="empty">Loading…</p>}
        {load.status === "error" && (
          <div className="empty" role="alert">
            <p>{load.message}</p>
            <button type="button" className="btn-small" onClick={() => { setLoad({ status: "loading" }); setAttempt((n) => n + 1); }}>
              Try again
            </button>
          </div>
        )}
      </Page>
    );
  }

  const { downloads, history } = load.data;
  const busy = new Set(downloads.map((d) => d.titleId));
  const waiting = load.data.waiting.filter((w) => !busy.has(w.id));
  return (
    <Page heading="Activity" onClose={onClose} wide>
      {error && (
        <p className="popup-error" role="alert">
          {error}
        </p>
      )}
      <section className="section">
        <h2>Queue</h2>
        {downloads.length + waiting.length === 0 ? (
          <p className="empty">Nothing is downloading.</p>
        ) : (
          <ul className="rows" aria-label="Queue">
            {downloads.map((d) => (
              <li key={d.id} className="row row-download">
                <div className="row-main">
                  <p className="row-title">
                    <a href={hrefOf({ name: "title", id: d.titleId })}>{d.titleName}</a>
                    {d.episode ? ` · ${d.episode.toUpperCase()}` : ""}
                  </p>
                  <p className="row-note">{d.releaseName}</p>
                  <div className="bar-track" role="progressbar" aria-label={`${d.titleName} progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={d.percent}>
                    <div className={d.stalled ? "bar-fill stalled" : "bar-fill"} style={{ width: `${d.percent}%` }} />
                  </div>
                  <p className="row-note">
                    {d.stalled ? "Stalled: no data has arrived for a while. marquee will try the next release. " : `${stateWords[d.state]}. `}
                    {d.bytesTotal > 0 && `${bytesOf(d.bytesDone)} of ${bytesOf(d.bytesTotal)}`}
                  </p>
                </div>
                <span className="percent">{`${d.percent}%`}</span>
              </li>
            ))}
            {waiting.map((w) => (
              <li key={w.id} className="row">
                <div className="row-main">
                  <p className="row-title">
                    <a href={hrefOf({ name: "title", id: w.id })}>{w.name}</a>
                  </p>
                  <p className="row-note">{w.note}</p>
                </div>
                <span className="percent mute">{w.label}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="section">
        <h2>History</h2>
        <HistoryList entries={history} showTitle retrying={retrying} onRetry={retry} empty="Nothing has happened yet." />
        {load.data.next && (
          <button type="button" className="btn-small" onClick={() => loadOlder(load.data.next as string)}>
            Show older
          </button>
        )}
      </section>
    </Page>
  );
}
