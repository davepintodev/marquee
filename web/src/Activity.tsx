import { useCallback, useEffect, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Download, HistoryEntry, LiveEvent, TitleSummary } from "./api/types";
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

interface Data {
  downloads: Download[];
  history: HistoryEntry[];
  searching: TitleSummary[];
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

export function Activity({ api, onClose, onChanged, onToast }: Props) {
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [retrying, setRetrying] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const working = useRef(false);

  const fetchAll = useCallback(
    async (signal?: AbortSignal): Promise<Data> => {
      const [downloads, history, library] = await Promise.all([api.downloads(signal), api.history(signal), api.library(signal)]);
      return { downloads, history, searching: library.filter((t) => t.badge?.state === "searching") };
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
    fetchAll().then((data) => setLoad({ status: "ok", data }), () => {});
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
      onChanged(title);
      onToast(`Searching for “${h.titleName}” again.`);
      setLoad({ status: "ok", data: await fetchAll() });
    } catch (e) {
      setError(messageOf(e));
    } finally {
      working.current = false;
      setRetrying(null);
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

  const { downloads, history, searching } = load.data;
  const busy = new Set(downloads.map((d) => d.titleId));
  const waiting = searching.filter((t) => !busy.has(t.id));
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
            {waiting.map((t) => (
              <li key={t.id} className="row">
                <div className="row-main">
                  <p className="row-title">
                    <a href={hrefOf({ name: "title", id: t.id })}>{t.title}</a>
                  </p>
                  <p className="row-note">Waiting for a release to appear.</p>
                </div>
                <span className="percent mute">{t.badge?.label}</span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section className="section">
        <h2>History</h2>
        <HistoryList entries={history} showTitle retrying={retrying} onRetry={retry} empty="Nothing has happened yet." />
      </section>
    </Page>
  );
}
