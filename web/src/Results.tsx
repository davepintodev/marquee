import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Cell } from "./Cell";

interface Props {
  api: Api;
  results: TitleSummary[];
  onSelect?: (title: TitleSummary) => void;
}

export function Results({ api, results, onSelect }: Props) {
  return (
    <div className="found">
      <p className="found-count">{results.length === 1 ? "1 result" : `${results.length} results`}</p>
      <ul className="grid" aria-label="Results">
        {results.map((t) => (
          <li key={t.id}>
            <Cell api={api} title={t} markOwned onSelect={onSelect} />
          </li>
        ))}
      </ul>
    </div>
  );
}
