import { useEffect, useState } from "react";
import type { Api } from "./api/client";
import type { CastMember, Rating, TitleAbout, TitleSummary } from "./api/types";
import { Art } from "./Art";
import { IconChevron } from "./icons";
import { LogoImdb, LogoRottenTomatoes, LogoTmdb } from "./logos";

type AboutLoad = { status: "loading" } | { status: "ok"; about: TitleAbout } | { status: "none" };

// Scores, cast and similar titles are extras: when they cannot be had, the screen simply goes without.
export function useAbout(api: Api, id: string): AboutLoad {
  const [state, setState] = useState<{ id: string; load: AboutLoad }>({ id, load: { status: "loading" } });
  useEffect(() => {
    const ctl = new AbortController();
    api.about(id, ctl.signal).then(
      (about) => !ctl.signal.aborted && setState({ id, load: { status: "ok", about } }),
      () => !ctl.signal.aborted && setState({ id, load: { status: "none" } }),
    );
    return () => ctl.abort();
  }, [api, id]);
  return state.id === id ? state.load : { status: "loading" };
}

const fullNames: Record<Rating["source"], string> = { tmdb: "TMDB", imdb: "IMDb", rotten_tomatoes: "Rotten Tomatoes" };
const logos: Record<Rating["source"], () => React.ReactElement> = {
  tmdb: () => <LogoTmdb />,
  imdb: () => <LogoImdb />,
  rotten_tomatoes: () => <LogoRottenTomatoes />,
};

const valueOf = (r: Rating) => (r.scale === 100 ? `${Math.round(r.score)}%` : r.score.toFixed(1));

export function Ratings({ about }: { about: AboutLoad }) {
  if (about.status === "loading") return <div className="ts-rates" data-wait="true" aria-hidden="true" />;
  if (about.status === "none" || about.about.ratings.length === 0) return null;
  return (
    <ul className="ts-rates" aria-label="Scores">
      {about.about.ratings.map((r) => {
        const detail = r.scale === 100 ? `${fullNames[r.source]} rating: ${Math.round(r.score)} percent` : `${fullNames[r.source]} rating: ${valueOf(r)} out of 10${r.votes ? `, ${r.votes.toLocaleString()} votes` : ""}`;
        return (
          <li key={r.source} data-source={r.source} title={detail} aria-label={detail}>
            {logos[r.source]()}
            <b aria-hidden="true">{valueOf(r)}</b>
          </li>
        );
      })}
    </ul>
  );
}

export function FactList({ about }: { about: AboutLoad }) {
  if (about.status !== "ok") return null;
  const a = about.about;
  const items: [string, string][] = [];
  if (a.genres.length) items.push(["Genres", a.genres.slice(0, 3).join(", ")]);
  if (a.runtimeMinutes) items.push(["Runtime", `${a.runtimeMinutes} min`]);
  if (a.status) items.push(["Status", a.status]);
  if (a.language) items.push(["Language", a.language]);
  if (items.length === 0) return null;
  return (
    <dl className="ts-factlist">
      {items.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

const initialsOf = (name: string) =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0])
    .join("")
    .toUpperCase();

function hueOf(name: string) {
  let h = 0;
  for (const c of name) h = (h * 31 + c.charCodeAt(0)) % 360;
  return h;
}

function Face({ api, person }: { api: Api; person: CastMember }) {
  const [broken, setBroken] = useState(false);
  if (person.profilePath && !broken) {
    return <img src={api.imageUrl(person.profilePath)} alt="" loading="lazy" draggable={false} onError={() => setBroken(true)} />;
  }
  return (
    <span className="ts-initials" style={{ ["--h" as string]: hueOf(person.name) }} aria-hidden="true">
      {initialsOf(person.name)}
    </span>
  );
}

const FIRST_CAST = 12;

export function AboutSections({ api, about, onOpen }: { api: Api; about: AboutLoad; onOpen?: (t: TitleSummary) => void }) {
  const [all, setAll] = useState(false);
  if (about.status !== "ok") return null;
  const a = about.about;
  const cast = all ? a.cast : a.cast.slice(0, FIRST_CAST);
  return (
    <>
      {a.cast.length > 0 && (
        <section className="ts-sec" aria-labelledby="ts-cast">
          <div className="ts-sec-head">
            <h2 id="ts-cast">Cast</h2>
          </div>
          <ul className="ts-cast">
            {cast.map((p) => (
              <li key={p.personId}>
                <span className="ts-face">
                  <Face api={api} person={p} />
                </span>
                <b>{p.name}</b>
                {p.character && <small>{p.character}</small>}
              </li>
            ))}
          </ul>
          {a.cast.length > FIRST_CAST && (
            <button type="button" className="fold-btn ts-more" aria-expanded={all} onClick={() => setAll(!all)}>
              {all ? "Show fewer" : `Show all ${a.cast.length}`}
              <IconChevron open={all} />
            </button>
          )}
        </section>
      )}
      {a.similar.length > 0 && (
        <section className="ts-sec" aria-labelledby="ts-sim">
          <div className="ts-sec-head">
            <h2 id="ts-sim">More like this</h2>
          </div>
          <ul className="ts-sim">
            {a.similar.map((s) => (
              <li key={s.id}>
                <button type="button" onClick={() => onOpen?.(s)} aria-label={`${s.title}, ${s.year}`}>
                  <span className="ts-sim-art">
                    <Art src={api.imageUrl(s.posterPath)} title={s.title} year={s.year} />
                  </span>
                  <b>{s.title}</b>
                  <small>{s.year}</small>
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}
