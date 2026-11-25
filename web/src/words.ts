import type { Badge, Episode, FailureReason, LibraryRescan, Title } from "./api/types";

const reasons: Record<FailureReason, string> = {
  stalled: "Stalled",
  not_video: "Fake file",
  wrong_episode: "Wrong episode",
  too_small: "Too small",
  size_mismatch: "Wrong size",
  source_down: "Source down",
  local_error: "Local error",
  duplicate_episode: "Duplicate episode",
  caching_timeout: "Caching timed out",
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
    case "caching":
      return `Caching ${e.percent ?? 0}%`;
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

const TAG_WORDS: Record<string, string> = {
  truehd: "TrueHD",
  "dts-hd": "DTS-HD",
  "dts-x": "DTS:X",
  "dd+-atmos": "DD+ Atmos",
  "dd+": "DD+",
  dts: "DTS",
  dd: "DD",
  aac: "AAC",
  flac: "FLAC",
  opus: "Opus",
  "dv-p7": "DV P7",
  dv: "DV",
  "hdr10+": "HDR10+",
  hdr10: "HDR10",
  hdr: "HDR",
  av1: "AV1",
  hevc: "HEVC",
  avc: "AVC",
};

export const tagWord = (tag: string) => TAG_WORDS[tag] ?? tag.toUpperCase();

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

export function rescanSummary(r: LibraryRescan) {
  const head = `Rescanned ${count(r.titlesScanned, "title", "titles")}`;
  const parts = [r.adopted > 0 && `${r.adopted} adopted`, r.foundOnDisk > 0 && `${r.foundOnDisk} found on disk`, r.missingOnDisk > 0 && `${r.missingOnDisk} gone from disk`].filter(Boolean);
  return parts.length > 0 ? `${head}: ${parts.join(", ")}.` : `${head}. Nothing changed.`;
}

const haveOf = (t: Pick<Title, "kind" | "badge" | "seasons">) =>
  new Set(t.kind === "movie" ? (t.badge?.state === "downloaded" ? ["movie"] : []) : t.seasons.flatMap((s) => s.episodes.filter((e) => e.state === "downloaded").map((e) => e.id)));

export function titleRescanSummary(before: Title, after: Title) {
  const had = haveOf(before);
  const has = haveOf(after);
  const found = [...has].filter((id) => !had.has(id)).length;
  const gone = [...had].filter((id) => !has.has(id)).length;
  const head = `Rescanned “${after.title}”`;
  if (after.kind === "movie") return found ? `${head}: its file is on disk.` : gone ? `${head}: its file is gone from disk.` : `${head}. Nothing changed.`;
  const parts = [found > 0 && `${count(found, "file", "files")} found on disk`, gone > 0 && `${count(gone, "file", "files")} gone from disk`].filter(Boolean);
  return parts.length > 0 ? `${head}: ${parts.join(", ")}.` : `${head}. Nothing changed.`;
}
