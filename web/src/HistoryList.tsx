import type { HistoryEntry } from "./api/types";
import { hrefOf } from "./route";
import { ago, reasonLabel } from "./words";

interface Props {
  entries: HistoryEntry[];
  // Activity lists every title, so each entry names its own; a title's own history does not.
  showTitle?: boolean;
  retrying: string | null;
  onRetry: (e: HistoryEntry) => void;
  empty: string;
}

export function HistoryList({ entries, showTitle, retrying, onRetry, empty }: Props) {
  if (entries.length === 0) return <p className="empty">{empty}</p>;
  return (
    <ul className="rows" aria-label="History">
      {entries.map((h) => (
        <li key={h.id} className={`row history-${h.kind}`}>
          <div className="row-main">
            <p className="row-title">
              {showTitle && (
                <>
                  <a href={hrefOf({ name: "title", id: h.titleId })}>{h.titleName}</a>
                  {h.episode ? ` · ${h.episode.toUpperCase()}` : ""}
                  <span className="sep"> · </span>
                </>
              )}
              {!showTitle && h.episode && <>{h.episode.toUpperCase()}<span className="sep"> · </span></>}
              <time dateTime={h.at}>{ago(h.at)}</time>
            </p>
            <p className="row-note">
              {h.kind === "failed" && <span className="reason">{reasonLabel(h.reason)}</span>}
              {h.message}
            </p>
          </div>
          {h.retryable && (
            <button type="button" className="btn-small" aria-label={`Retry ${h.titleName}`} aria-disabled={retrying === h.id} onClick={() => onRetry(h)}>
              Retry
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}
