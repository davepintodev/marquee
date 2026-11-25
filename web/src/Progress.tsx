import type { Title } from "./api/types";
import { Attempts } from "./Attempts";
import { IconCheck } from "./icons";
import { totalsOf } from "./episodes";
import { Sk } from "./Skeleton";
import { waitLine, waitOf } from "./words";

function figures(t: Title) {
  const c = totalsOf(t);
  const aired = c.total - c.unaired;
  return { have: c.downloaded, aired };
}

export function Have({ title }: { title: Title }) {
  if (title.kind === "series") {
    const f = figures(title);
    if (f.aired === 0) return null;
    return (
      <p className="ts-have" role="img" aria-label={`${f.have} of ${f.aired} aired episodes downloaded`}>
        <b>{`${f.have} of ${f.aired}`}</b>
        <span>downloaded</span>
      </p>
    );
  }
  if (!title.badge || waitOf(title)) return null;
  return (
    <p className="ts-have" data-state={title.badge.state}>
      {title.badge.state === "downloaded" && <IconCheck />}
      <b>{title.badge.label}</b>
    </p>
  );
}

export function Wait({ title, onSearch, busy }: { title: Title; onSearch?: () => void; busy?: boolean }) {
  const state = waitOf(title);
  if (!state) return null;
  const line = waitLine(state, title.nextSearchAt);
  const off = busy || state === "searching";
  return (
    <p className="ts-wait" data-state={state}>
      <i aria-hidden="true" />
      {line && <span>{line}</span>}
      {onSearch && (
        <button type="button" className="ts-wait-now" aria-disabled={off} aria-busy={busy} onClick={() => !off && onSearch()}>
          {busy ? <Sk shape="dot" /> : "Search now"}
        </button>
      )}
    </p>
  );
}

export function ProgressLine({ title, onSearch, busy }: { title: Title; onSearch?: () => void; busy?: boolean }) {
  return (
    <div className="ts-prog">
      <Have title={title} />
      <Wait title={title} onSearch={onSearch} busy={busy} />
      <Attempts title={title} />
    </div>
  );
}
