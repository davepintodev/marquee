import type { TitleSummary } from "./api/types";

// A slim bar at the poster's foot says how far a title is: it grows while
// downloading and sits full in the state's colour when something is
// missing or stuck. A finished title carries no mark, except in search
// results (owned), where it tells a title in the library from one that is not.
export function Meter({ title, owned }: { title: TitleSummary; owned?: boolean }) {
  const b = title.inLibrary ? title.badge : undefined;
  if (!b || (b.state === "downloaded" && !owned)) return null;
  const width = b.state === "downloading" ? `${b.percent ?? 0}%` : "100%";
  return (
    <span className="meter" data-state={b.state} aria-hidden="true">
      <i style={{ width }} />
    </span>
  );
}

// The state in plain words, under a search result.
export function StateLine({ title }: { title: TitleSummary }) {
  const b = title.inLibrary ? title.badge : undefined;
  if (!b) return null;
  return (
    <span className={`state-line badge-${b.state}`}>
      <i aria-hidden="true" />
      {b.label}
    </span>
  );
}
