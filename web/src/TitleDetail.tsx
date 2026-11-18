import { useCallback, useEffect, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Episode, HistoryEntry, LiveEvent, Season, Title } from "./api/types";
import { Art } from "./Art";
import { BadgePill, StatePill } from "./BadgePill";
import { HistoryList } from "./HistoryList";
import { Page } from "./Page";
import { useLive } from "./useLive";
import { inHours, seasonBadgeLabel, stateLabel } from "./words";

interface Props {
  api: Api;
  id: string;
  onClose: () => void;
  onChanged: (title: Title) => void;
  onRemoved: (title: Title) => void;
  onToast: (text: string) => void;
}

type Load = { status: "loading" } | { status: "ok"; title: Title } | { status: "error"; message: string };

const messageOf = (e: unknown) => (e instanceof Error ? e.message : "Something went wrong.");
const isAbort = (e: unknown) => e instanceof DOMException && e.name === "AbortError";

export function TitleDetail({ api, id, onClose, onChanged, onRemoved, onToast }: Props) {
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const working = useRef(false);

  useEffect(() => {
    const ctl = new AbortController();
    api.title(id, ctl.signal).then(
      (title) => !ctl.signal.aborted && setLoad({ status: "ok", title }),
      (e) => !ctl.signal.aborted && !isAbort(e) && setLoad({ status: "error", message: messageOf(e) }),
    );
    return () => ctl.abort();
  }, [api, id, attempt]);

  const refetch = useCallback(() => {
    api.title(id).then((title) => setLoad({ status: "ok", title }), () => {});
  }, [api, id]);

  useLive(api, (e: LiveEvent) => {
    if (e.type === "reconnected") return refetch();
    if (e.data.titleId !== id) return;
    if (e.type === "title") return refetch();
    setLoad((l) => (l.status === "ok" ? { status: "ok", title: withProgress(l.title, e.data.episode, e.data.percent) } : l));
  });

  const run = async (key: string, act: () => Promise<Title>, toast?: string) => {
    if (working.current) return;
    working.current = true;
    setBusy(key);
    setError(null);
    try {
      const title = await act();
      setLoad({ status: "ok", title });
      onChanged(title);
      if (toast) onToast(toast);
    } catch (e) {
      setError(messageOf(e));
    } finally {
      working.current = false;
      setBusy(null);
    }
  };

  const remove = async (title: Title) => {
    if (working.current) return;
    working.current = true;
    setBusy("remove");
    setError(null);
    try {
      await api.removeTitle(title.id);
      onRemoved(title);
    } catch (e) {
      setError(messageOf(e));
      setConfirming(false);
    } finally {
      working.current = false;
      setBusy(null);
    }
  };

  if (load.status !== "ok") {
    return (
      <Page heading={load.status === "loading" ? "Loading…" : "Can't open this title"} onClose={onClose}>
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

  const t = load.title;
  const retry = async (h: HistoryEntry) => {
    await run(h.id, () => api.retry(h.id), `Searching for “${t.title}” again.`);
  };

  return (
    <Page heading={t.title} onClose={onClose}>
      <div className="detail-head">
        <span className="detail-poster">
          <Art src={api.imageUrl(t.posterPath)} title={t.title} year={t.year} />
        </span>
        <div className="detail-info">
          <p className="popup-meta">{`${t.year} · ${t.kind}`}</p>
          <div className="detail-state">
            <BadgePill badge={t.badge} inline />
            {t.monitored === false && <span className="tag">Unmonitored</span>}
          </div>
          <p className="detail-summary">{t.historySummary}</p>
          {t.overview && <p className="detail-overview">{t.overview}</p>}
          {error && (
            <p className="popup-error" role="alert">
              {error}
            </p>
          )}
          <div className="detail-actions">
            <button type="button" className="btn-primary" aria-disabled={busy === "search"} onClick={() => run("search", () => api.searchNow(t.id), `Searching for “${t.title}” now.`)}>
              Search now
            </button>
            <button type="button" className="btn-secondary" aria-disabled={busy === "monitor"} onClick={() => run("monitor", () => api.setMonitored(t.id, t.monitored === false))}>
              {t.monitored === false ? "Monitor" : "Unmonitor"}
            </button>
            {!confirming && (
              <button type="button" className="btn-secondary" onClick={() => setConfirming(true)}>
                Remove from marquee
              </button>
            )}
          </div>
          {confirming && (
            <div className="confirm" role="group" aria-label="Confirm removal">
              <p>{`Remove “${t.title}” from marquee? Its files stay on disk; marquee just stops looking for it.`}</p>
              <div className="detail-actions">
                <button type="button" className="btn-danger" aria-disabled={busy === "remove"} onClick={() => remove(t)}>
                  Remove
                </button>
                <button type="button" className="btn-secondary" onClick={() => setConfirming(false)}>
                  Cancel
                </button>
              </div>
            </div>
          )}
        </div>
      </div>

      {t.seasons.map((s) => (
        <SeasonView
          key={s.number}
          season={s}
          busy={busy}
          onSearch={(e) => run(e.id, () => api.searchEpisodeNow(t.id, e.id), `Searching for ${e.name} now.`)}
          onMonitor={(e) => run(e.id, () => api.setEpisodeMonitored(t.id, e.id, !e.monitored))}
        />
      ))}

      <section className="section">
        <h2>History</h2>
        <HistoryList entries={t.history ?? []} retrying={busy} onRetry={retry} empty="Nothing has happened yet." />
      </section>
    </Page>
  );
}

function withProgress(t: Title, episode: string | undefined, percent: number): Title {
  if (!episode) return { ...t, badge: t.badge && { ...t.badge, label: `Downloading ${percent}%`, percent } };
  return {
    ...t,
    seasons: t.seasons.map((s) => ({
      ...s,
      episodes: s.episodes.map((e) => (e.id === episode ? { ...e, state: "downloading", percent } : e)),
    })),
  };
}

function SeasonView({ season, busy, onSearch, onMonitor }: { season: Season; busy: string | null; onSearch: (e: Episode) => void; onMonitor: (e: Episode) => void }) {
  // Seasons that are complete fold away so a long series opens on what is missing.
  const open = season.episodes.some((e) => e.state !== "downloaded" && e.state !== "unaired");
  const label = `Season ${season.number}`;
  return (
    <section className="season" aria-label={season.number === 0 ? "Specials" : label}>
      <details open={open}>
        <summary>
          <span className="season-name">{season.number === 0 ? "Specials" : label}</span>
          {season.badge && <StatePill state={season.badge.state} label={seasonBadgeLabel(season.badge)} />}
        </summary>
        <ul className="rows">
          {season.episodes.map((e) => (
            <li key={e.id} className={e.monitored ? "row" : "row unmonitored"}>
              <div className="row-main">
                <p className="row-title">
                  <span className="ep">{`E${String(e.number).padStart(2, "0")}`}</span> {e.name}
                </p>
                <p className="row-note">
                  <span>{e.airDate}</span>
                  {!e.monitored && <span className="tag">Unmonitored</span>}
                  {e.nextSearchAt && <span>{`Next search in ${inHours(e.nextSearchAt)}`}</span>}
                </p>
              </div>
              <StatePill state={e.state} label={stateLabel(e)} />
              {e.state !== "unaired" && e.state !== "downloaded" && e.state !== "downloading" && (
                <button type="button" className="btn-small" aria-label={`Search now for ${e.name}`} aria-disabled={busy === e.id} onClick={() => onSearch(e)}>
                  Search now
                </button>
              )}
              <button type="button" className="btn-small ghost" aria-label={`${e.monitored ? "Unmonitor" : "Monitor"} ${e.name}`} aria-disabled={busy === e.id} onClick={() => onMonitor(e)}>
                {e.monitored ? "Unmonitor" : "Monitor"}
              </button>
            </li>
          ))}
        </ul>
      </details>
    </section>
  );
}
