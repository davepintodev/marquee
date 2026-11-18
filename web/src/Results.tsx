import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Art } from "./Art";
import { BadgePill } from "./BadgePill";

interface Props {
  api: Api;
  results: TitleSummary[];
  onSelect?: (title: TitleSummary) => void;
}

export function Results({ api, results, onSelect }: Props) {
  return (
    <ul className="grid" aria-label="Results">
      {results.map((t) => (
        <li key={t.id}>
          <button type="button" className="card" onClick={() => onSelect?.(t)}>
            <span className="card-art">
              <Art src={api.imageUrl(t.posterPath)} title={t.title} year={t.year} />
              <BadgePill badge={t.inLibrary ? t.badge : undefined} />
            </span>
            <span className="card-name">{t.title}</span>
            <span className="card-meta">{`${t.year} · ${t.kind}`}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}
