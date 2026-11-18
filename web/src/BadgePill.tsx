import type { Badge } from "./api/types";

export function StatePill({ state, label }: { state: string; label: string }) {
  return (
    <span className={`badge inline badge-${state}`}>
      <i aria-hidden="true" />
      {label}
    </span>
  );
}

export function BadgePill({ badge, inline }: { badge?: Badge; inline?: boolean }) {
  if (!badge) return null;
  if (inline) return <StatePill state={badge.state} label={badge.label} />;
  return (
    <span className={`badge badge-${badge.state}`}>
      <i aria-hidden="true" />
      {badge.label}
    </span>
  );
}
