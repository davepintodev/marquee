import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Art } from "./Art";
import { Meter, StateLine } from "./Meter";

interface Props {
  api: Api;
  title: TitleSummary;
  markOwned?: boolean;
  onSelect?: (title: TitleSummary) => void;
}

export function Cell({ api, title: t, markOwned, onSelect }: Props) {
  const label = t.badge ? `${t.title} (${t.year}), ${t.badge.label}` : `${t.title} (${t.year})`;
  return (
    <button type="button" className="cell" aria-label={label} title={label} onClick={() => onSelect?.(t)}>
      <span className="cell-art">
        <Art src={api.imageUrl(t.posterPath)} title={t.title} year={t.year} />
        <Meter title={t} />
      </span>
      <span className="cell-name">{t.title}</span>
      <span className="cell-meta">{`${t.year} · ${t.kind === "movie" ? "Movie" : "Series"}`}</span>
      {markOwned && <StateLine title={t} />}
    </button>
  );
}
