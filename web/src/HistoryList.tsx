import type { HistoryEntry } from "./api/types";
import { IconRetry } from "./icons";
import { ago, reasonLabel } from "./words";

interface Props {
  entries: HistoryEntry[];
  retrying: string | null;
  onRetry: (e: HistoryEntry) => void;
  empty: string;
}

// No pills: a failure is a gutter dot and the reason in bold; Retry is a quiet icon and a word.
export function HistoryList({ entries, retrying, onRetry, empty }: Props) {
  if (entries.length === 0) return <p className="empty">{empty}</p>;
  return (
    <ul className="hx" aria-label="History">
      {entries.map((h) => (
        <li key={h.id} className="hx-row" data-failed={h.kind === "failed" || undefined} data-reason={h.reason ?? "other"}>
          <p className="hx-head">
            {h.episode && (
              <>
                <b>{h.episode.toUpperCase()}</b>
                <span aria-hidden="true"> · </span>
              </>
            )}
            <time dateTime={h.at}>{ago(h.at)}</time>
          </p>
          <p className="hx-note">
            {h.kind === "failed" && <b>{reasonLabel(h.reason)}. </b>}
            {h.message}
          </p>
          {h.retryable && (
            <button type="button" className="hx-retry" aria-label={`Retry ${h.episode ? h.episode.toUpperCase() : h.titleName}`} aria-disabled={retrying === h.id} onClick={() => onRetry(h)}>
              <IconRetry />
              Retry
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}
