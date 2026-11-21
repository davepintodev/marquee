import { type KeyboardEvent, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Episode, HistoryEntry, Season, Title, TitleSummary } from "./api/types";
import { AboutSections, FactList, Ratings, useAbout } from "./About";
import { AddBar } from "./AddBar";
import { Art } from "./Art";
import { codeOf, countsOf, factsOf, nextWanted, seasonName, seasonShort } from "./episodes";
import { HistoryList } from "./HistoryList";
import { IconBookmark, IconButton, IconChevron, IconSearch, IconSpinner } from "./icons";
import { MonitorMark } from "./MonitorMark";
import { ProgressLine } from "./Progress";
import { ReleaseList } from "./ReleaseList";
import { BackBar, Collapse, RemoveBlock } from "./TitleParts";
import { useEscape } from "./useEscape";
import { useTitleScreen } from "./useTitleScreen";
import { inHours, stateLabel } from "./words";
import "./icons.css";
import "./title-screen.css";
import "./title-details.css";

interface Props {
  api: Api;
  id: string;
  seed?: TitleSummary;
  onClose: () => void;
  onChanged: (title: TitleSummary) => void;
  onRemoved: (title: Title) => void;
  onToast: (text: string) => void;
  onOpenTitle?: (title: TitleSummary) => void;
}

const dateOf = (iso: string) => new Date(`${iso}T12:00:00`).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" });

const FOLD_MS = 200;
const reduced = () => typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

export function TitleScreen({ api, id, seed, onClose, onChanged, onRemoved, onToast, onOpenTitle }: Props) {
  const { load, setLoad, busy, error, run, remove, reload } = useTitleScreen({ api, id, seed, onChanged, onRemoved, onToast });
  const about = useAbout(api, id);
  const [enter] = useState(() => !document.documentElement.hasAttribute("data-morph"));
  const [season, setSeason] = useState<number | null>(null);
  const [seasonOpen, setSeasonOpen] = useState(true);
  const [open, setOpen] = useState<string | null>(null);
  const [closing, setClosing] = useState(false);
  const [onlyMissing, setOnlyMissing] = useState(false);
  const [scrolled, setScrolled] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const sentinel = useRef<HTMLDivElement>(null);
  const back = useRef<HTMLButtonElement>(null);
  const fold = useRef<number>(0);

  useEffect(() => {
    const opener = document.activeElement;
    back.current?.focus({ preventScroll: true });
    return () => {
      window.clearTimeout(fold.current);
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus({ preventScroll: true });
    };
  }, []);

  useEffect(() => {
    const el = sentinel.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(([e]) => setScrolled(!e.isIntersecting), { root: root.current, threshold: 0 });
    io.observe(el);
    return () => io.disconnect();
  }, [load.status]);

  const openRow = useCallback((episode: string) => {
    window.clearTimeout(fold.current);
    setClosing(false);
    setOpen(episode);
  }, []);

  const closeRow = useCallback(() => {
    window.clearTimeout(fold.current);
    if (reduced()) return setOpen(null);
    setClosing(true);
    fold.current = window.setTimeout(() => {
      setOpen(null);
      setClosing(false);
    }, FOLD_MS);
  }, []);

  // While a release list is open, or folding, Esc belongs to it; only after that does App's Esc leave the page.
  useEscape(open !== null, () => {
    if (closing || !open) return;
    const row = open;
    closeRow();
    requestAnimationFrame(() => document.querySelector<HTMLElement>(`[data-ep="${row}"]`)?.focus({ preventScroll: true }));
  });

  const t = load.status === "ok" ? load.title : null;
  const ordered = useMemo(() => (t ? [...t.seasons].sort((a, b) => (a.number === 0 ? 1 : b.number === 0 ? -1 : a.number - b.number)) : []), [t]);
  const chosen = ordered.find((s) => s.number === season) ?? defaultSeason(t, ordered);

  if (!t) {
    return (
      <div className="ts" data-enter={enter || undefined} ref={root} role="main" aria-label={load.status === "error" ? "Can't open this title" : "Loading"}>
        <BackBar solid={!seed} back={back} onClose={onClose} />
        {seed && (
          // What the list already knew, so the clicked poster has somewhere to land while the full title loads.
          <div className="ts-hero">
            <div className="ts-backdrop" aria-hidden="true">
              <Art src={api.imageUrl(seed.backdropPath)} title="" year={seed.year} />
            </div>
            <div className="ts-hero-in">
              <span className="ts-poster">
                <Art src={api.imageUrl(seed.posterPath)} title={seed.title} year={seed.year} />
              </span>
              <div className="ts-info">
                <h1 id="ts-h1">{seed.title}</h1>
                <p className="ts-facts">{`${seed.year} · ${seed.kind === "movie" ? "Movie" : "Series"}`}</p>
                {/* Empty blocks of the loaded screen's height, so its text does not jump up when the rest arrives. */}
                <div className="ts-rates" data-wait="true" aria-hidden="true" />
                {seed.overview && <p className="ts-overview">{seed.overview}</p>}
                <div className="ts-actions ts-ghost" aria-hidden="true" />
                <div className="ts-factlist ts-ghost" aria-hidden="true" />
              </div>
            </div>
          </div>
        )}
        <div className="ts-body">
          {load.status !== "error" ? (
            <p className="rl-status" role="status">
              Loading…
            </p>
          ) : (
            <div className="rl-note" role="alert">
              <p>{load.message}</p>
              <button type="button" className="btn-small" onClick={reload}>
                Try again
              </button>
            </div>
          )}
        </div>
      </div>
    );
  }

  const isSeries = t.kind === "series";
  const inLib = t.inLibrary;
  const retry = (h: HistoryEntry) => run(h.id, () => api.retry(h.id), `Searching for “${t.title}” again.`);
  const monitored = t.monitored !== false;

  return (
    <div className="ts" data-enter={enter || undefined} ref={root} role="main" aria-labelledby="ts-h1">
      <BackBar title={t.title} solid={scrolled} back={back} onClose={onClose} />

      <div className="ts-hero">
        <div className="ts-backdrop" aria-hidden="true">
          <Art src={api.imageUrl(t.backdropPath)} title="" year={t.year} />
        </div>
        <div className="ts-hero-in">
          <span className="ts-poster">
            <Art src={api.imageUrl(t.posterPath)} title={t.title} year={t.year} />
          </span>
          <div className="ts-info">
            <h1 id="ts-h1">{t.title}</h1>
            <p className="ts-facts">
              {factsOf(t)}
              {inLib && !monitored && <span className="tag">Not monitored</span>}
            </p>
            <Ratings about={about} />
            {t.overview && <p className="ts-overview">{t.overview}</p>}
            {error && (
              <p className="fail-line" role="alert">
                {error}
              </p>
            )}
            <div className="ts-actions">
              {inLib ? (
                <>
                  <button type="button" className="btn-primary with-icon" aria-disabled={busy === "search"} onClick={() => run("search", () => api.searchNow(t.id), `Searching for “${t.title}” now.`)}>
                    {busy === "search" ? <IconSpinner /> : <IconSearch />}
                    Search automatically
                  </button>
                  <IconButton
                    label={monitored ? "Monitored. Stop monitoring this title" : "Not monitored. Start monitoring this title"}
                    pressed={monitored}
                    aria-disabled={busy === "monitor"}
                    onClick={() => run("monitor", () => api.setMonitored(t.id, !monitored))}
                  >
                    <IconBookmark on={monitored} />
                  </IconButton>
                </>
              ) : (
                <AddBar api={api} title={t} onChanged={onChanged} onToast={onToast} onAdded={(added) => setLoad({ status: "ok", title: added })} />
              )}
            </div>
            <FactList about={about} />
          </div>
        </div>
      </div>
      <div ref={sentinel} className="ts-sentinel" aria-hidden="true" />

      <div className="ts-body">
        {isSeries && inLib && chosen && (
          <section className="ts-sec" aria-labelledby="ts-eps">
            <div className="ts-sec-head">
              <h2 id="ts-eps">Episodes</h2>
            </div>

            <SeasonStrip
              seasons={ordered}
              chosen={chosen.number}
              onChoose={(n) => {
                setSeason(n);
                setSeasonOpen(true);
                setOpen(null);
                setClosing(false);
              }}
            />

            <SeasonBar
              season={chosen}
              expanded={seasonOpen}
              onToggle={() => {
                setSeasonOpen(!seasonOpen);
                setOpen(null);
                setClosing(false);
              }}
              busy={busy === `season-${chosen.number}`}
              onlyMissing={onlyMissing}
              setOnlyMissing={setOnlyMissing}
              onMonitor={(on) => run(`season-${chosen.number}`, () => api.setSeasonMonitored(t.id, chosen.number, on), `${seasonName(chosen.number)} ${on ? "monitored" : "not monitored"}.`)}
            />

            <div id="ts-season-list" hidden={!seasonOpen}>
              <ul className="ts-eps" aria-label={`${seasonName(chosen.number)} episodes`}>
                {chosen.episodes
                  .filter((e) => !onlyMissing || (e.state !== "downloaded" && e.state !== "unaired"))
                  .map((e) => {
                    const expanded = open === e.id;
                    return (
                      <EpisodeRow
                        key={e.id}
                        episode={e}
                        season={chosen}
                        expanded={expanded}
                        closing={expanded && closing}
                        autoBusy={busy === `now-${e.id}`}
                        monitorBusy={busy === e.id}
                        exception={exceptionOf(chosen, e.monitored)}
                        onToggle={() => (expanded && !closing ? closeRow() : openRow(e.id))}
                        onAuto={() => run(`now-${e.id}`, () => api.searchEpisodeNow(t.id, e.id), `Searching for ${e.name} now.`)}
                        onMonitor={() => run(e.id, () => api.setEpisodeMonitored(t.id, e.id, !e.monitored))}
                      >
                        <ReleaseList
                          api={api}
                          titleId={t.id}
                          titleName={t.title}
                          episodeId={e.id}
                          what={`${seasonName(chosen.number)} · ${e.name}`}
                          onChanged={onChanged}
                          onToast={onToast}
                        />
                      </EpisodeRow>
                    );
                  })}
                {onlyMissing && chosen.episodes.every((e) => e.state === "downloaded" || e.state === "unaired") && <li className="ts-none">Nothing is missing from this season.</li>}
              </ul>
            </div>
          </section>
        )}

        {isSeries && inLib && !chosen && (
          <section className="ts-sec">
            <div className="rl-note">
              <p>This series has no episodes listed yet.</p>
            </div>
          </section>
        )}

        {isSeries && !inLib && (
          <section className="ts-sec">
            <p className="ts-lone">Seasons and episodes show up once the series is added.</p>
          </section>
        )}

        {!isSeries && (
          <section className="ts-sec" aria-labelledby="ts-rel">
            <div className="ts-sec-head">
              <h2 id="ts-rel">Releases</h2>
            </div>
            <ReleaseList api={api} titleId={t.id} titleName={t.title} what={t.title} manual onChanged={onChanged} onToast={onToast} />
          </section>
        )}

        <AboutSections api={api} about={about} onOpen={onOpenTitle} />

        {inLib && (
          <section className="ts-sec">
            <Collapse label="History" aside={<ProgressLine title={t} />}>
              <HistoryList entries={t.history ?? []} retrying={busy} onRetry={retry} empty="Nothing has happened yet." />
            </Collapse>
          </section>
        )}

        {inLib && (
          <section className="ts-sec ts-remove">
            <RemoveBlock title={t} busy={busy === "remove"} onRemove={() => remove(t)} />
          </section>
        )}
      </div>
    </div>
  );
}

function EpisodeRow({
  episode: e,
  season,
  expanded,
  closing,
  autoBusy,
  monitorBusy,
  exception,
  onToggle,
  onAuto,
  onMonitor,
  children,
}: {
  episode: Episode;
  season: Season;
  expanded: boolean;
  closing: boolean;
  autoBusy: boolean;
  monitorBusy: boolean;
  exception: "on" | "off" | null;
  onToggle: () => void;
  onAuto: () => void;
  onMonitor: () => void;
  children: React.ReactNode;
}) {
  const aired = e.state !== "unaired";
  const done = e.state === "downloaded";
  const spinning = autoBusy || e.state === "searching";
  const downloading = e.state === "downloading";
  const panel = `ts-panel-${e.id}`;
  const word = stateLabel(e);
  return (
    <li className="ts-ep" data-open={expanded} data-off={!e.monitored} data-done={done} data-state={e.state}>
      <div className="ts-ep-row">
        <MonitorMark have={done} on={e.monitored} name={e.name} busy={monitorBusy} onToggle={onMonitor} />
        <button
          type="button"
          className="ts-ep-main"
          data-ep={e.id}
          aria-expanded={aired ? expanded : undefined}
          aria-controls={expanded ? panel : undefined}
          aria-disabled={!aired}
          aria-label={`${codeOf(season.number, e.number)} ${e.name}, ${word}${aired ? ". Choose a release" : ""}`}
          onClick={() => aired && onToggle()}
        >
          <span className="ts-ep-name">
            <span>
              <span className="ts-ep-n">{String(e.number).padStart(2, "0")}</span>
              {e.name}
              {exception === "off" && <em className="ts-flag">Not monitored</em>}
              {exception === "on" && (
                <em className="ts-flag on">
                  <IconBookmark on />
                  Monitored
                </em>
              )}
            </span>
            <small>
              {dateOf(e.airDate)}
              {e.nextSearchAt && e.monitored && ` · next search in ${inHours(e.nextSearchAt)}`}
            </small>
          </span>
          <span className="ts-ep-end">
            <span className="st" data-state={e.state}>
              {word}
            </span>
            {aired && <span className="ts-ep-hint">{expanded ? "Hide releases" : "Choose release"}</span>}
          </span>
        </button>
        {aired ? (
          <IconButton
            label={downloading ? "Downloading now" : done ? "Search again automatically" : "Search automatically"}
            name={`${downloading ? "Downloading now" : "Search automatically for"} ${e.name}`}
            quiet
            className="ts-find"
            data-here={done}
            aria-disabled={spinning || downloading}
            aria-busy={spinning}
            onClick={() => !spinning && !downloading && onAuto()}
          >
            {spinning ? <IconSpinner /> : <IconSearch />}
          </IconButton>
        ) : (
          <span className="ts-find-ph" aria-hidden="true" />
        )}
      </div>
      {expanded && (
        <div className="ts-ep-wrap" data-closing={closing} id={panel}>
          <div className="ts-ep-panel">
            <div className="ts-ep-panel-in">{children}</div>
          </div>
        </div>
      )}
    </li>
  );
}

function exceptionOf(s: Season, monitored: boolean): "on" | "off" | null {
  const on = s.episodes.filter((e) => e.monitored).length;
  if (on === 0 || on === s.episodes.length) return null;
  return on * 2 >= s.episodes.length ? (monitored ? null : "off") : monitored ? "on" : null;
}

function defaultSeason(t: Title | null, ordered: Season[]): Season | undefined {
  if (!t || ordered.length === 0) return undefined;
  const next = nextWanted(t);
  if (next) return ordered.find((s) => s.number === next.season);
  return [...ordered].reverse().find((s) => s.number > 0) ?? ordered[0];
}

function SeasonBar({
  season,
  expanded,
  onToggle,
  busy,
  onlyMissing,
  setOnlyMissing,
  onMonitor,
}: {
  season: Season;
  expanded: boolean;
  onToggle: () => void;
  busy: boolean;
  onlyMissing: boolean;
  setOnlyMissing: (v: boolean) => void;
  onMonitor: (on: boolean) => void;
}) {
  const c = countsOf(season);
  const aired = c.total - c.unaired;
  const on = season.episodes.filter((e) => e.monitored).length;
  const all = on === season.episodes.length;
  const rest = [c.missing ? `${c.missing} missing` : "", c.unaired ? `${c.unaired} not aired` : ""].filter(Boolean).join(" · ");
  return (
    <div className="ts-season-bar">
      <h3 className="ts-season-title">
        <button type="button" className="fold-btn ts-season-toggle" aria-expanded={expanded} aria-controls="ts-season-list" onClick={onToggle}>
          <b>{seasonName(season.number)}</b>
          <span className="ts-season-sum">
            <em>{`${c.downloaded} of ${aired}`}</em>
            {rest && ` · ${rest}`}
          </span>
          <IconChevron open={expanded} />
        </button>
      </h3>
      <div className="ts-season-tools">
        {expanded && (
          <div className="rl-filters" role="group" aria-label="Which episodes to list">
            <button type="button" className="rl-chip" aria-pressed={!onlyMissing} onClick={() => setOnlyMissing(false)}>
              All
            </button>
            <button type="button" className="rl-chip" aria-pressed={onlyMissing} onClick={() => setOnlyMissing(true)}>
              Missing
            </button>
          </div>
        )}
        {season.episodes.length > 0 && (
          <button type="button" className="ul-link ts-mon with-icon" aria-pressed={all} aria-disabled={busy} onClick={() => onMonitor(!all)}>
            <IconBookmark on={on > 0} />
            {all ? "Monitoring season" : on === 0 ? "Monitor season" : `Monitoring ${on} of ${season.episodes.length}`}
          </button>
        )}
      </div>
    </div>
  );
}

function SeasonStrip({ seasons, chosen, onChoose }: { seasons: Season[]; chosen: number; onChoose: (n: number) => void }) {
  const tabs = useRef<(HTMLButtonElement | null)[]>([]);
  useEffect(() => {
    const i = seasons.findIndex((s) => s.number === chosen);
    const el = tabs.current[i];
    const strip = el?.parentElement;
    if (el && strip) strip.scrollLeft = el.offsetLeft - strip.clientWidth / 2 + el.offsetWidth / 2;
  }, [chosen, seasons]);
  const onKey = (e: KeyboardEvent) => {
    const i = seasons.findIndex((s) => s.number === chosen);
    const go = e.key === "ArrowRight" ? i + 1 : e.key === "ArrowLeft" ? i - 1 : e.key === "Home" ? 0 : e.key === "End" ? seasons.length - 1 : -1;
    if (go < 0 || go >= seasons.length) return;
    e.preventDefault();
    onChoose(seasons[go].number);
    tabs.current[go]?.focus({ preventScroll: true });
  };
  return (
    <div className="ts-strip" role="tablist" aria-label="Seasons" onKeyDown={onKey}>
      {seasons.map((s, i) => {
        const c = countsOf(s);
        const aired = c.total - c.unaired;
        const on = s.number === chosen;
        return (
          <button
            key={s.number}
            ref={(el) => {
              tabs.current[i] = el;
            }}
            type="button"
            role="tab"
            className="ts-tab"
            aria-selected={on}
            tabIndex={on ? 0 : -1}
            aria-label={`${seasonName(s.number)}, ${c.downloaded} of ${aired} downloaded`}
            onClick={() => onChoose(s.number)}
          >
            <b>{seasonShort(s.number)}</b>
          </button>
        );
      })}
    </div>
  );
}
