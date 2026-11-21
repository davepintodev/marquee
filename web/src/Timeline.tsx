import type { HistoryEntry } from "./api/types";
import { byDay } from "./activity";
import { hrefOf } from "./route";
import { ago, reasonLabel } from "./words";
import { Busy } from "./Skeleton";

interface Props {
  entries: HistoryEntry[];
  retrying: string | null;
  onRetry: (e: HistoryEntry) => void;
}

const clock = (d: Date) => d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });

export function Timeline({ entries, retrying, onRetry }: Props) {
  return (
    <div className="days">
      {byDay(entries).map((day) => (
        <section key={day.key} className="day" aria-label={day.label}>
          <h3>{day.label}</h3>
          <ul>
            {day.entries.map((h) => (
              <li key={h.id} className="log" data-kind={h.kind}>
                <div className="log-main">
                  <p className="log-title">
                    <time dateTime={h.at} title={ago(h.at)}>
                      {clock(new Date(h.at))}
                    </time>
                    <a href={hrefOf({ name: "title", id: h.titleId })}>{h.titleName}</a>
                    {h.episode && <span className="log-ep">{h.episode.toUpperCase()}</span>}
                  </p>
                  <p className="log-note">
                    {h.kind === "failed" && (
                      <>
                        <b className="log-reason">{reasonLabel(h.reason)}</b>{" "}
                      </>
                    )}
                    {h.message}
                  </p>
                </div>
                {h.retryable && (
                  <button type="button" className="link-btn" aria-label={`Retry ${h.titleName}`} aria-disabled={retrying === h.id} onClick={() => onRetry(h)}>
                    {retrying === h.id ? <Busy say="Searching" /> : "Retry"}
                  </button>
                )}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}
