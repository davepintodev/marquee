import type { Episode, Season, Title } from "./api/types";
import { metaOf } from "./year";

const pad = (n: number) => String(n).padStart(2, "0");

export const codeOf = (season: number, episode: number) => `S${pad(season)}E${pad(episode)}`;
export const seasonName = (n: number) => (n === 0 ? "Specials" : `Season ${n}`);
export const seasonShort = (n: number) => (n === 0 ? "SP" : String(n));

interface Flat {
  season: number;
  episode: Episode;
  code: string;
}

function flatten(t: Pick<Title, "seasons">): Flat[] {
  return t.seasons.flatMap((s) => s.episodes.map((episode) => ({ season: s.number, episode, code: codeOf(s.number, episode.number) })));
}

export function nextWanted(t: Pick<Title, "seasons">): Flat | undefined {
  const aired = flatten(t).filter((f) => f.episode.state !== "unaired");
  return aired.find((f) => f.episode.state !== "downloaded" && f.episode.state !== "downloading" && f.episode.state !== "caching") ?? undefined;
}

export interface Counts {
  total: number;
  downloaded: number;
  missing: number;
  unaired: number;
  active: number;
}

export function countsOf(s: Pick<Season, "episodes">): Counts {
  const c: Counts = { total: s.episodes.length, downloaded: 0, missing: 0, unaired: 0, active: 0 };
  for (const e of s.episodes) {
    if (e.state === "downloaded") c.downloaded++;
    else if (e.state === "unaired") c.unaired++;
    else if (e.state === "missing" || e.state === "stalled") c.missing++;
    else c.active++;
  }
  return c;
}

export function totalsOf(t: Pick<Title, "seasons">): Counts {
  const sum: Counts = { total: 0, downloaded: 0, missing: 0, unaired: 0, active: 0 };
  for (const s of t.seasons) {
    const c = countsOf(s);
    sum.total += c.total;
    sum.downloaded += c.downloaded;
    sum.missing += c.missing;
    sum.unaired += c.unaired;
    sum.active += c.active;
  }
  return sum;
}

export function factsOf(t: Pick<Title, "kind" | "year" | "seasons">): string {
  if (t.kind === "movie") return metaOf(t);
  const regular = t.seasons.filter((s) => s.number > 0).length;
  const eps = totalsOf(t).total;
  const seasons = regular ? ` · ${regular} season${regular === 1 ? "" : "s"}` : "";
  return `${metaOf(t)}${seasons}${eps ? ` · ${eps} episodes` : ""}`;
}
