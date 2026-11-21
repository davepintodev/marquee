import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Cell } from "./Cell";
import { colsOf } from "./Library";
import { useViewport } from "./useViewport";
import { Say, Sk } from "./Skeleton";

interface Props {
  api: Api;
  results: TitleSummary[];
  onSelect?: (title: TitleSummary) => void;
}

export function Results({ api, results, onSelect }: Props) {
  const { width } = useViewport();
  const cols = colsOf(width);
  return (
    <div className="found">
      <p className="found-count">{results.length === 1 ? "1 result" : `${results.length} results`}</p>
      <ul className="grid" aria-label="Results" style={{ ["--cols" as string]: cols }}>
        {results.map((t) => (
          <li key={t.id}>
            <Cell api={api} title={t} markOwned onSelect={onSelect} />
          </li>
        ))}
      </ul>
    </div>
  );
}

export function ResultsSkeleton() {
  const { width } = useViewport();
  const cols = colsOf(width);
  return (
    <div className="found" aria-busy="true">
      <Say>Searching</Say>
      <div className="found-count">
        <Sk shape="line" width={84} />
      </div>
      <ul className="grid" aria-hidden="true" style={{ ["--cols" as string]: cols }}>
        {Array.from({ length: cols * 2 }, (_, i) => (
          <li key={i} className="sk sk-poster" />
        ))}
      </ul>
    </div>
  );
}
