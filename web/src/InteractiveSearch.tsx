import { useEffect, useRef, useState } from "react";
import { ApiError, type Api } from "./api/client";
import type { Episode, Release, Releases, Title, TitleSummary } from "./api/types";
import { bytesOf } from "./words";

interface Props {
  api: Api;
  title: TitleSummary;
  onBack: () => void;
  onChanged: (title: TitleSummary) => void;
  onToast: (text: string) => void;
}

type Load<T> = { status: "loading" } | { status: "ok"; data: T } | { status: "error"; message: string; notFound?: boolean };

const isAbort = (e: unknown) => e instanceof DOMException && e.name === "AbortError";
const messageOf = (e: unknown) => (e instanceof Error ? e.message : "Something went wrong.");

interface Option {
  id: string;
  label: string;
}

// Unaired episodes have nothing to find. The default is the first one that is still wanted.
function episodeOptions(t: Title): { options: Option[]; first: string | undefined } {
  const aired: { e: Episode; season: number }[] = t.seasons.flatMap((s) => s.episodes.filter((e) => e.state !== "unaired").map((e) => ({ e, season: s.number })));
  const options = aired.map(({ e, season }) => ({ id: e.id, label: `S${String(season).padStart(2, "0")}E${String(e.number).padStart(2, "0")} · ${e.name}` }));
  const wanted = aired.find(({ e }) => e.state !== "downloaded") ?? aired[0];
  return { options, first: wanted?.e.id };
}

export function InteractiveSearch({ api, title, onBack, onChanged, onToast }: Props) {
  const series = title.kind === "series";
  const back = useRef<HTMLButtonElement>(null);
  const working = useRef(false);
  const [episodeAnswer, setEpisodeAnswer] = useState<{ key: string; load: Load<Option[]> } | null>(null);
  const [episode, setEpisode] = useState<string | undefined>();
  const [releaseAnswer, setReleaseAnswer] = useState<{ key: string; load: Load<Releases> } | null>(null);
  const [episodeAttempt, setEpisodeAttempt] = useState(0);
  const [releaseAttempt, setReleaseAttempt] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [queued, setQueued] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => back.current?.focus({ preventScroll: true }), []);

  // An answer belongs to the request that asked for it; anything else still reads as loading.
  const episodeKey = `${title.id}/${episodeAttempt}`;
  const episodes: Load<Option[]> = episodeAnswer?.key === episodeKey ? episodeAnswer.load : { status: "loading" };
  useEffect(() => {
    if (!series) return;
    const ctl = new AbortController();
    api.title(title.id, ctl.signal).then(
      (t) => {
        if (ctl.signal.aborted) return;
        const { options, first } = episodeOptions(t);
        setEpisodeAnswer({ key: episodeKey, load: { status: "ok", data: options } });
        setEpisode((current) => current ?? first);
      },
      (e) => !ctl.signal.aborted && !isAbort(e) && setEpisodeAnswer({ key: episodeKey, load: { status: "error", message: messageOf(e), notFound: e instanceof ApiError && e.status === 404 } }),
    );
    return () => ctl.abort();
  }, [api, series, title.id, episodeKey]);

  const wanted = series ? episode : undefined;
  const ready = !series || episode !== undefined;
  const releaseKey = `${title.id}/${wanted}/${releaseAttempt}`;
  const releases: Load<Releases> = releaseAnswer?.key === releaseKey ? releaseAnswer.load : { status: "loading" };
  useEffect(() => {
    if (!ready) return;
    const ctl = new AbortController();
    api.releases(title.id, wanted, ctl.signal).then(
      (data) => !ctl.signal.aborted && setReleaseAnswer({ key: releaseKey, load: { status: "ok", data } }),
      (e) => !ctl.signal.aborted && !isAbort(e) && setReleaseAnswer({ key: releaseKey, load: { status: "error", message: messageOf(e) } }),
    );
    return () => ctl.abort();
  }, [api, title.id, wanted, ready, releaseKey]);

  const grab = async (r: Release) => {
    if (working.current) return;
    working.current = true;
    setBusy(r.id);
    setError(null);
    try {
      await api.grab(title.id, r.id, wanted);
      setQueued((q) => new Set(q).add(`${wanted ?? ""}/${r.id}`));
      onToast(`Queued ${r.name} for “${title.title}”.`);
      // The grab may have added the title; its tile and popup follow.
      api.title(title.id).then(onChanged, () => {});
    } catch (e) {
      setError(messageOf(e));
    } finally {
      working.current = false;
      setBusy(null);
    }
  };

  const retryEpisodes = () => setEpisodeAttempt((n) => n + 1);
  const retryReleases = () => setReleaseAttempt((n) => n + 1);
  const row = (r: Release, note: string | undefined, pick?: "plain" | "anyway") => (
    <li key={r.id} className="row">
      <div className="row-main">
        <p className="row-title">{r.name}</p>
        <p className="row-note">
          <span>{r.quality}</span>
          <span>{bytesOf(r.sizeBytes)}</span>
          <span>{r.source}</span>
          <span className="tag">{r.cached ? "Cached" : "Not cached"}</span>
        </p>
        {note && <p className="row-note">{note}</p>}
      </div>
      {queued.has(`${wanted ?? ""}/${r.id}`) ? (
        <span className="rel-queued">Queued</span>
      ) : (
        pick && (
          <button type="button" className="btn-small" aria-label={pick === "anyway" ? `Pick ${r.name} anyway` : `Pick ${r.name}`} aria-disabled={busy === r.id} onClick={() => grab(r)}>
            {pick === "anyway" ? "Pick anyway" : "Pick"}
          </button>
        )
      )}
    </li>
  );

  let body;
  if (episodes.status === "error") {
    body = episodes.notFound ? (
      <p className="ix-empty">Add &amp; monitor this series first, then pick an episode to search.</p>
    ) : (
      <div className="ix-empty" role="alert">
        <p>{episodes.message}</p>
        <button type="button" className="btn-small" onClick={retryEpisodes}>
          Try again
        </button>
      </div>
    );
  } else if (episodes.status === "ok" && episodes.data.length === 0) {
    body = <p className="ix-empty">No episode has aired yet, so there is nothing to search.</p>;
  } else if (releases.status === "loading") {
    body = <p className="ix-empty">Asking AIOStreams…</p>;
  } else if (releases.status === "error") {
    body = (
      <div className="ix-empty" role="alert">
        <p>{releases.message}</p>
        <button type="button" className="btn-small" onClick={retryReleases}>
          Try again
        </button>
      </div>
    );
  } else {
    const { candidates, dropped } = releases.data;
    body = (
      <>
        {error && (
          <p className="popup-error" role="alert">
            {error}
          </p>
        )}
        {candidates.length > 0 ? (
          <ul className="rows" aria-label="Releases">
            {candidates.map((r) => row(r, undefined, "plain"))}
          </ul>
        ) : (
          <p className="ix-empty">{dropped.length > 0 ? "Nothing to pick: every release was dropped." : "Nothing found for this title or episode."}</p>
        )}
        {dropped.length > 0 && (
          <details className="rels-dropped">
            <summary>{`Dropped (${dropped.length})`}</summary>
            <ul className="rows" aria-label="Dropped">
              {dropped.map((d) => row(d.release, d.message, d.reason === "blocklisted" ? "anyway" : undefined))}
            </ul>
          </details>
        )}
      </>
    );
  }

  return (
    <>
      <div className="ix">
        {episodes.status === "ok" && episodes.data.length > 0 && (
          <select className="ix-episode" aria-label="Episode" value={episode} onChange={(e) => {
              setError(null);
              setEpisode(e.target.value);
            }}>
            {episodes.data.map((o) => (
              <option key={o.id} value={o.id}>
                {o.label}
              </option>
            ))}
          </select>
        )}
        {body}
      </div>
      <div className="popup-actions">
        <button ref={back} type="button" className="btn-secondary" onClick={onBack}>
          Back
        </button>
      </div>
    </>
  );
}
