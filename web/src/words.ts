import type { Badge, Episode, FailureReason } from "./api/types";

const reasons: Record<FailureReason, string> = {
  stalled: "Stalled",
  not_video: "Fake file",
  wrong_episode: "Wrong episode",
  too_small: "Too small",
  size_mismatch: "Wrong size",
  source_down: "Source down",
  local_error: "Local error",
};

export function reasonLabel(reason: FailureReason | undefined) {
  return reason ? reasons[reason] : "Failed";
}

export function stateLabel(e: Pick<Episode, "state"> & { percent?: number }) {
  switch (e.state) {
    case "downloaded":
      return "Downloaded";
    case "searching":
      return "Searching";
    case "downloading":
      return `Downloading ${e.percent ?? 0}%`;
    case "stalled":
      return "Stalled";
    case "missing":
      return "Missing";
    case "unaired":
      return "Not aired yet";
  }
}

export function seasonBadgeLabel(badge: Badge) {
  return badge.state === "partial_season" && badge.missingCount !== undefined ? `${badge.missingCount} missing` : badge.label;
}

// Rounds the way the backend words its history sentence ("retrying in 6h").
export function inHours(iso: string, now = new Date()) {
  const ms = new Date(iso).getTime() - now.getTime();
  if (ms <= 0) return "moments";
  if (ms >= 3600_000) return `${Math.floor((ms + 1800_000) / 3600_000)}h`;
  return `${Math.max(1, Math.floor((ms + 30_000) / 60_000))}m`;
}

export function ago(iso: string, now = new Date()) {
  const d = new Date(iso);
  const s = (now.getTime() - d.getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  const days = Math.floor(s / 86400);
  if (days === 1) return "1 day ago";
  if (days <= 7) return `${days} days ago`;
  return d.toLocaleDateString(undefined, d.getFullYear() === now.getFullYear() ? { month: "short", day: "numeric" } : { month: "short", day: "numeric", year: "numeric" });
}

const units = ["B", "KB", "MB", "GB", "TB"];

export function bytesOf(n: number) {
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const text = i === 0 || v >= 10 ? String(Math.round(v)) : v.toFixed(1).replace(/\.0$/, "");
  return `${text} ${units[i]}`;
}
