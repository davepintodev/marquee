import type { Title } from "./api/types";
import { Attempts } from "./Attempts";
import { IconCheck } from "./icons";
import { totalsOf } from "./episodes";
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

export function Wait({ title }: { title: Title }) {
  const state = waitOf(title);
  const line = state && waitLine(state, title.nextSearchAt);
  if (!line) return null;
  return (
    <span className="ts-wait" data-state={state}>
      <i aria-hidden="true" />
      {line}
    </span>
  );
}

export function ProgressLine({ title }: { title: Title }) {
  return (
    <div className="ts-prog">
      <Have title={title} />
      <Attempts title={title} />
    </div>
  );
}
