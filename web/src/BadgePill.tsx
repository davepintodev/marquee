import type { Badge } from "./api/types";

export function BadgePill({ badge }: { badge?: Badge }) {
  if (!badge) return null;
  return (
    <span className={`badge badge-${badge.state}`}>
      <i aria-hidden="true" />
      {badge.label}
    </span>
  );
}
