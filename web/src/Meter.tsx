import type { TitleSummary } from "./api/types";

export function Meter({ title }: { title: TitleSummary }) {
  const b = title.inLibrary ? title.badge : undefined;
  if (!b || b.state === "downloaded") return null;
  const width = b.state === "downloading" || b.state === "caching" ? `${b.percent ?? 0}%` : "100%";
  return (
    <span className="meter" data-state={b.state} aria-hidden="true">
      <i style={{ width }} />
    </span>
  );
}

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
