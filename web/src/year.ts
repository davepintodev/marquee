import type { TitleSummary } from "./api/types";

type Dated = Pick<TitleSummary, "kind" | "year">;

// TMDB titles with no release date arrive as year 0.
const yearOf = (t: Pick<TitleSummary, "year">) => (t.year > 0 ? String(t.year) : "");

const kindLabel = (t: Pick<TitleSummary, "kind">) => (t.kind === "movie" ? "Movie" : "Series");

export const metaOf = (t: Dated) => [yearOf(t), kindLabel(t)].filter(Boolean).join(" · ");

export function tileName(t: Pick<TitleSummary, "title" | "year" | "badge">) {
  const named = t.year > 0 ? `${t.title} (${t.year})` : t.title;
  return t.badge ? `${named}, ${t.badge.label}` : named;
}
