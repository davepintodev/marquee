import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Art } from "./Art";
import { Meter, StateLine } from "./Meter";
import { metaOf, tileName } from "./year";

interface Props {
  api: Api;
  title: TitleSummary;
  markOwned?: boolean;
  onSelect?: (title: TitleSummary) => void;
}

export function Cell({ api, title: t, markOwned, onSelect }: Props) {
  const label = tileName(t);
  return (
    <button type="button" className="cell" data-tid={t.id} aria-label={label} title={label} onClick={() => onSelect?.(t)}>
      <span className="cell-art">
        <Art src={api.imageUrl(t.posterPath)} title={t.title} year={t.year} />
        <Meter title={t} />
      </span>
      <span className="cell-name">{t.title}</span>
      <span className="cell-meta">{metaOf(t)}</span>
      {markOwned && <StateLine title={t} />}
    </button>
  );
}
