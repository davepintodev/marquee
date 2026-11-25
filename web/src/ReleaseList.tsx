import { useEffect, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Release, Releases, TitleSummary } from "./api/types";
import { IconChevron } from "./icons";
import { bytesOf, tagWord } from "./words";
import "./release.css";
import { Notice } from "./Notice";
import { ReleasesSkeleton } from "./Skeleton";
import { EmptyState, ErrorState } from "./State";

type Load = { status: "loading" } | { status: "ok"; data: Releases } | { status: "error"; message: string };

const NONE: Release[] = [];

const FIRST = 3;
const isAbort = (e: unknown) => e instanceof DOMException && e.name === "AbortError";
const messageOf = (e: unknown) => (e instanceof Error ? e.message : "Something went wrong.");

function split(quality: string) {
  const m = /^(.*?)-?(\d{3,4}p)$/.exec(quality);
  return m ? { res: m[2], src: m[1] } : { res: quality, src: "" };
}

interface Props {
  api: Api;
  titleId: string;
  titleName: string;
  episodeId?: string;
  what: string;
  // Waits for a click before asking AIOStreams, which rate-limits.
  manual?: boolean;
  onChanged: (title: TitleSummary) => void;
  onToast: (text: string) => void;
}

export function ReleaseList({ api, titleId, titleName, episodeId, what, manual, onChanged, onToast }: Props) {
  const key = `${titleId}/${episodeId ?? ""}`;
  const [started, setStarted] = useState<string | null>(manual ? null : key);
  const [attempt, setAttempt] = useState(0);
  const [answer, setAnswer] = useState<{ key: string; load: Load } | null>(null);
  // Both belong to one episode's list, so a new episode starts them over.
  const [pick, setPick] = useState<{ key: string; res: string; error: string | null }>({ key, res: "all", error: null });
  const res = pick.key === key ? pick.res : "all";
  const error = pick.key === key ? pick.error : null;
  const setRes = (v: string) => setPick({ key, res: v, error: null });
  const setError = (e: string | null) => setPick((p) => ({ key, res: p.key === key ? p.res : "all", error: e }));
  const [busy, setBusy] = useState<string | null>(null);
  const [folds, setFolds] = useState({ more: false, dropped: false });
  const [queued, setQueued] = useState<ReadonlySet<string>>(new Set());
  const working = useRef(false);

  const go = started === key || !manual;
  const askKey = `${key}/${attempt}`;
  const load: Load = answer?.key === askKey ? answer.load : { status: "loading" };

  useEffect(() => {
    if (!go) return;
    const ctl = new AbortController();
    api.releases(titleId, episodeId, ctl.signal).then(
      (data) => !ctl.signal.aborted && setAnswer({ key: askKey, load: { status: "ok", data } }),
      (e) => !ctl.signal.aborted && !isAbort(e) && setAnswer({ key: askKey, load: { status: "error", message: messageOf(e) } }),
    );
    return () => ctl.abort();
  }, [api, titleId, episodeId, askKey, go]);

  const candidates = load.status === "ok" ? load.data.candidates : NONE;
  const seen = new Map<string, number>();
  for (const r of candidates) seen.set(split(r.quality).res, (seen.get(split(r.quality).res) ?? 0) + 1);
  const resolutions = [...seen.entries()].sort((a, b) => parseInt(b[0]) - parseInt(a[0]));
  const [sort, setSort] = useState<{ by: "rank" | "size" | "quality"; dir: 1 | -1 }>({ by: "rank", dir: 1 });
  const filtered = res === "all" ? candidates : candidates.filter((r) => split(r.quality).res === res);
  const sortKey = (r: Release) => (sort.by === "size" ? r.sizeBytes : parseInt(split(r.quality).res) || 0);
  const shown = sort.by === "rank" ? filtered : [...filtered].sort((a, b) => (sortKey(a) - sortKey(b)) * sort.dir);
  const sortBy = (by: "size" | "quality") => setSort((s) => (s.by === by ? { by, dir: (s.dir * -1) as 1 | -1 } : { by, dir: -1 }));

  const grab = async (r: Release) => {
    if (working.current) return;
    working.current = true;
    setBusy(r.id);
    setError(null);
    try {
      await api.grab(titleId, r.id, episodeId);
      setQueued((q) => new Set(q).add(`${key}/${r.id}`));
      onToast(`Queued ${r.name}.`);
      api.title(titleId).then(onChanged, () => {});
    } catch (e) {
      setError(messageOf(e));
    } finally {
      working.current = false;
      setBusy(null);
    }
  };

  const act = (r: Release, pick: "plain" | "anyway" | undefined, i: number) =>
    queued.has(`${key}/${r.id}`) ? (
      <span className="rl-queued">Queued</span>
    ) : (
      pick && (
        <button type="button" className="btn-small" data-pick={i} aria-label={`${pick === "anyway" ? "Pick anyway" : "Pick"} ${r.name}`} aria-disabled={busy === r.id} onClick={() => grab(r)}>
          {pick === "anyway" ? "Pick anyway" : "Pick"}
        </button>
      )
    );

  const tableRow = (r: Release, i: number, opts: { top?: boolean; why?: string; pick?: "plain" | "anyway" }) => {
    const { res: q, src } = split(r.quality);
    return (
      <li key={r.id} className="rt-row" data-top={opts.top ? "true" : undefined} data-dropped={opts.why ? "true" : undefined}>
        <span className="rt-q">
          <b>{q}</b> <small>{src}</small>
        </span>
        <span className="rt-file">
          {r.name}
          {r.tags && r.tags.length > 0 && (
            <span className="rl-tags">
              {r.tags.map((t) => (
                <span key={t} className="rl-tag">
                  {tagWord(t)}
                </span>
              ))}
            </span>
          )}
          {opts.top && <small className="rl-pick-tag">marquee’s pick</small>}
          {opts.why && <small className="rl-why">{opts.why}</small>}
        </span>
        <span className="rt-src">{r.source}</span>
        <span className="rt-c" data-cached={r.cached}>
          <i aria-hidden="true" />
          {r.cached ? "Cached" : "No"}
        </span>
        <span className="rt-size">{bytesOf(r.sizeBytes)}</span>
        <span className="rt-act">{act(r, opts.pick, i)}</span>
      </li>
    );
  };

  const head = (
    <div className="rt-head" role="presentation">
      <button type="button" onClick={() => sortBy("quality")} aria-sort={sort.by === "quality" ? (sort.dir === 1 ? "ascending" : "descending") : undefined}>
        Quality{sort.by === "quality" ? (sort.dir === 1 ? " ↑" : " ↓") : ""}
      </button>
      <span>Release</span>
      <span>Source</span>
      <span>Cache</span>
      <button type="button" className="rt-h-size" onClick={() => sortBy("size")} aria-sort={sort.by === "size" ? (sort.dir === 1 ? "ascending" : "descending") : undefined}>
        Size{sort.by === "size" ? (sort.dir === 1 ? " ↑" : " ↓") : ""}
      </button>
      <span />
    </div>
  );

  if (!go) {
    return (
      <EmptyState fit="inline" tone="info" action={{ label: "Search releases", onClick: () => setStarted(key) }}>
        Search AIOStreams for <strong>{what}</strong> and choose the file yourself.
      </EmptyState>
    );
  }
  if (load.status === "loading") {
    return <ReleasesSkeleton say={`Asking AIOStreams for ${what}`} />;
  }
  if (load.status === "error") {
    return (
      <ErrorState fit="inline" action={{ label: "Try again", onClick: () => setAttempt((n) => n + 1) }}>
        {load.message}
      </ErrorState>
    );
  }

  const { dropped } = load.data;
  // The fold counts marquee's own order, so a chosen sort or resolution shows everything it matches.
  const inOrder = res === "all" && sort.by === "rank";
  const folded = inOrder && shown.length > FIRST + 1;
  const lead = folded && !folds.more ? shown.slice(0, FIRST) : shown;
  const later = folded ? shown.length - FIRST : 0;
  return (
    <div className="rl rl-t">
      {candidates.length > 0 && (inOrder || resolutions.length > 1) && (
        <div className="rl-head">
          <p className="rl-count">{inOrder ? "in marquee’s order" : ""}</p>
          {resolutions.length > 1 && (
            <div className="rl-filters" role="group" aria-label="Filter by resolution">
              <button type="button" className="rl-chip" aria-pressed={res === "all"} onClick={() => setRes("all")}>
                All
              </button>
              {resolutions.map(([r]) => (
                <button key={r} type="button" className="rl-chip" aria-pressed={res === r} onClick={() => setRes(r)}>
                  {r}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
      {error && <Notice tone="error">{error}</Notice>}
      {candidates.length > 0 ? (
        <div className="rl-main">
          {head}
          <ul className="rt-list" aria-label={`Releases for ${what}`}>
            {lead.map((r, i) => tableRow(r, i, { top: inOrder && i === 0, pick: "plain" }))}
          </ul>
          {later > 0 && (
            <button type="button" className="fold-btn rl-fold" data-fold="more" aria-expanded={folds.more} aria-label={`Show ${later} more releases`} onClick={() => setFolds((f) => ({ ...f, more: !f.more }))}>
              Show more
              <IconChevron open={folds.more} />
            </button>
          )}
        </div>
      ) : (
        <EmptyState fit="inline" action={{ label: "Search again", onClick: () => setAttempt((n) => n + 1) }}>
          {dropped.length > 0 ? (
            <>
              Every release for <strong>{what}</strong> was dropped. They are listed below if you want one anyway.
            </>
          ) : (
            <>
              AIOStreams has nothing for <strong>{what}</strong> in {titleName} right now.
            </>
          )}
        </EmptyState>
      )}
      {dropped.length > 0 && (
        <div className="rl-skipped">
          <button type="button" className="fold-btn rl-fold" data-fold="dropped" aria-expanded={folds.dropped} aria-label={`Show ${dropped.length} dropped releases`} onClick={() => setFolds((f) => ({ ...f, dropped: !f.dropped }))}>
            Dropped releases
            <IconChevron open={folds.dropped} />
          </button>
          {folds.dropped && (
            <>
              <p className="rl-skip-note">marquee will not try these.</p>
              <ul className="rt-list" aria-label="Dropped releases">
                {dropped.map((d, i) => tableRow(d.release, i, { why: d.message, pick: d.reason === "blocklisted" || d.reason === "profile_rejected" ? "anyway" : undefined }))}
              </ul>
            </>
          )}
        </div>
      )}
    </div>
  );
}
