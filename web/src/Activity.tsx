import { useCallback, useEffect, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Download, DownloadQueue, HistoryEntry, LiveEvent, TitleSummary } from "./api/types";
import { groupsOf, pausable, runningOf, settling, summaryOf, waitersOf, type Group, type Waiter } from "./activity";
import { GroupCard, PauseAll, type Ops } from "./Downloads";
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

interface Data {
  downloads: Download[];
  paused: boolean;
  history: HistoryEntry[];
  // The cursor of the page after the loaded history; absent when it is all loaded.
  next?: string;
  // Older pages are loaded, so a refetch of the newest page must keep them.
  older: boolean;
  waiting: Waiter[];
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

interface Touch {
  ids: Set<string>;
  flag?: boolean;
}

function restored(now: Data, before: Data, touch: Touch): Data {
  const known = new Set(before.downloads.map((d) => d.id));
  const downloads = [
    ...before.downloads.flatMap((b) => (touch.ids.has(b.id) ? [b] : now.downloads.filter((d) => d.id === b.id))),
    ...now.downloads.filter((d) => !known.has(d.id)),
  ];
  return { ...now, downloads, paused: touch.flag ? before.paused : now.paused };
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
      const [queue, page, library] = await Promise.all([api.downloads(signal), api.history({ limit: PAGE }, signal), api.library(signal)]);
      return { downloads: queue.downloads, paused: queue.paused, history: page.entries, next: page.next, older: false, waiting: waitersOf(library) };
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
    if (e.type === "downloads") {
      setLoad((l) => (l.status === "ok" ? { status: "ok", data: { ...l.data, paused: e.data.paused } } : l));
      return refetchSoon();
    }
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
              downloads: l.data.downloads.map((d) =>
                d.id === p.downloadId ? { ...d, state: p.state, percent: p.percent, bytesDone: p.bytesDone, bytesTotal: p.bytesTotal, bytesPerSecond: p.bytesPerSecond, stalled: p.state === "downloading" && d.stalled } : d,
              ),
            },
          },
    );
  });

  const data = useRef<Data>(undefined);
  data.current = load.status === "ok" ? load.data : undefined;
  const [open, setOpen] = useState<ReadonlySet<string>>(new Set());
  const toggle = (id: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  const edit = (change: (d: Data) => Data) => setLoad((l) => (l.status === "ok" ? { status: "ok", data: change(l.data) } : l));
  const replace = (changed: Download[]) => edit((d) => ({ ...d, downloads: d.downloads.map((x) => changed.find((c) => c.id === x.id) ?? x) }));

  // A refusal puts back only the downloads the action touched (and the Pause all flag when it did), so a
  // live update to another row, or another action in flight, survives it.
  const act = async (touch: Touch, optimistic: (d: Data) => Data, call: () => Promise<void>, again: () => void) => {
    const before = data.current;
    edit(optimistic);
    try {
      await call();
    } catch (e) {
      if (before) edit((d) => restored(d, before, touch));
      refetch();
      onToast(messageOf(e), { kind: "error", action: { label: "Try again", onClick: again } });
    }
  };

  const labelOf = (d: Download) => (d.episode ? `${d.episode.toUpperCase()} of ${d.titleName}` : d.titleName);
  const gone = (ids: Set<string>) => (d: Data): Data => ({ ...d, downloads: d.downloads.filter((x) => !ids.has(x.id)) });
  const held = (d: Download): Download => ({ ...d, state: "paused", stalled: false, bytesPerSecond: 0 });
  const freed = (d: Download): Download => ({ ...d, state: d.percent > 0 ? "downloading" : "queued" });
  const mapIds = (ids: Set<string>, to: (d: Download) => Download) => (d: Data): Data => ({ ...d, downloads: d.downloads.map((x) => (ids.has(x.id) ? to(x) : x)) });

  const current = (id: string) => {
    const d = data.current;
    return d ? groupsOf(d.downloads, d.waiting).find((g) => g.id === id) : undefined;
  };

  const ops: Ops = {
    pause: function pause(d) {
      const ids = new Set([d.id]);
      return void act({ ids }, mapIds(ids, held), async () => replace([await api.pauseDownload(d.id)]), () => pause(d));
    },
    resume: function resume(d) {
      const ids = new Set([d.id]);
      return void act({ ids }, mapIds(ids, freed), async () => replace([await api.resumeDownload(d.id)]), () => resume(d));
    },
    cancel: function cancel(d) {
      const ids = new Set([d.id]);
      return void act(
        { ids },
        gone(ids),
        async () => {
          await api.cancelDownload(d.id);
          onToast(`Cancelled ${labelOf(d)}. Looking for another release.`);
          refetch();
        },
        () => cancel(d),
      );
    },
    pauseGroup: function pauseGroup(g) {
      const ids = new Set(g.downloads.filter((d) => !settling(d) && pausable(d)).map((d) => d.id));
      const again = () => {
        const now = current(g.id);
        if (now) pauseGroup(now);
      };
      return void act({ ids }, mapIds(ids, held), async () => replace(await api.pauseTitleDownloads(g.id)), again);
    },
    resumeGroup: function resumeGroup(g) {
      const ids = new Set(g.downloads.filter((d) => d.state === "paused").map((d) => d.id));
      const again = () => {
        const now = current(g.id);
        if (now) resumeGroup(now);
      };
      return void act({ ids }, mapIds(ids, freed), async () => replace(await api.resumeTitleDownloads(g.id)), again);
    },
    cancelGroup: function cancelGroup(g) {
      const ids = new Set(g.downloads.filter((d) => !settling(d)).map((d) => d.id));
      const again = () => {
        const now = current(g.id);
        if (now) cancelGroup(now);
      };
      return void act(
        { ids },
        gone(ids),
        async () => {
          await api.cancelTitleDownloads(g.id);
          onToast(`Cancelled ${ids.size === 1 ? "the download" : `${ids.size} downloads`} of ${g.name}. Looking for other releases.`);
          refetch();
        },
        again,
      );
    },
  };

  const settle = (queue: DownloadQueue) => edit((d) => ({ ...d, downloads: queue.downloads, paused: queue.paused }));
  const pauseAll = function pauseAll(): void {
    const ids = new Set((data.current?.downloads ?? []).filter((d) => d.state === "downloading" || d.state === "caching").map((d) => d.id));
    void act({ ids, flag: true }, (d) => ({ ...d, paused: true, downloads: mapIds(ids, held)(d).downloads }), async () => settle(await api.pauseAllDownloads()), pauseAll);
  };
  const resumeAll = function resumeAll(): void {
    void act({ ids: new Set(), flag: true }, (d) => ({ ...d, paused: false }), async () => settle(await api.resumeAllDownloads()), resumeAll);
  };

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

  const { downloads, history, paused } = load.data;
  const groups = groupsOf(downloads, load.data.waiting);
  const going = groups.filter((g) => runningOf(g).length > 0);
  const waiting = groups.filter((g) => runningOf(g).length === 0);
  if (groups.length === 0 && history.length === 0 && !paused) {
    return (
      <Page heading="Activity" onClose={onClose} className="page-activity">
        <EmptyState fit="page">Nothing is happening yet. Downloads, searches and what they found will show up here.</EmptyState>
      </Page>
    );
  }
  const summary = summaryOf({
    downloading: downloads.filter((d) => d.state !== "queued" && d.state !== "paused").length,
    paused: downloads.filter((d) => d.state === "paused").length,
    allPaused: paused,
    waiting: groups.reduce((n, g) => n + g.waiting, 0),
    retryable: history.filter((h) => h.retryable).length,
  });
  const card = (g: Group) => <GroupCard key={g.id} api={api} g={g} paused={paused} open={open.has(g.id)} onToggle={() => toggle(g.id)} ops={ops} />;
  return (
    <Page heading="Activity" onClose={onClose} className="page-activity">
      <p className="board-sum">{summary}</p>
      {error && <Notice tone="error">{error}</Notice>}
      <div className="board-cols">
        <div className="board-main">
          <section className="section board-section" aria-labelledby="act-downloading">
            <PauseAll paused={paused} onPauseAll={pauseAll} onResumeAll={resumeAll}>
              {going.length === 0 ? (
                <Notice tone="quiet">Nothing is downloading.</Notice>
              ) : (
                <ul className="groups" aria-label="Downloads">
                  {going.map(card)}
                </ul>
              )}
            </PauseAll>
          </section>
          {waiting.length > 0 && (
            <section className="section board-section" aria-labelledby="act-waiting">
              <h2 id="act-waiting">Waiting</h2>
              <p className="board-hint">Queued behind a download, or searching for a release.</p>
              <ul className="groups" aria-label="Waiting">
                {waiting.map(card)}
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
