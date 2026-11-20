export type Shape = "portrait" | "wide";

export interface Tile {
  x: number;
  y: number;
  w: number;
  h: number;
  shape: Shape;
}

// Each band is one row of cells. Cell widths are in ninths of the band
// height, so a portrait (2:3) is 6 wide and a wide (16:9) tile is 16
// wide at full band height; a stack is two wide tiles at half height.
type Cell = "portrait" | "stack" | "hero";
const CELL_WIDTH: Record<Cell, number> = { portrait: 6, stack: 8, hero: 16 };
const CELLS: Cell[] = ["portrait", "stack", "hero"];

// Band widths the cells can add up to exactly; 2, 4 and 10 are not
// among them.
function reachable(n: number): boolean {
  return n === 0 || (n > 0 && CELLS.some((c) => reachable(n - CELL_WIDTH[c])));
}

function rng(seed: number) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function compose(total: number, next: () => number, needWide: boolean): Cell[] | null {
  const walk = (rest: number, hasWide: boolean): Cell[] | null => {
    if (rest === 0) return needWide && !hasWide ? null : [];
    const r = next();
    const order: Cell[] = r < 0.5 ? ["portrait", "stack", "hero"] : r < 0.85 ? ["stack", "portrait", "hero"] : ["hero", "stack", "portrait"];
    for (const c of order) {
      const left = rest - CELL_WIDTH[c];
      if (left < 0 || !reachable(left)) continue;
      const tail = walk(left, hasWide || c !== "portrait");
      if (tail) return [c, ...tail];
    }
    return null;
  };
  return walk(total, false);
}

export function layoutWall(width: number, height: number): Tile[] {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) return [];

  const bands = Math.max(1, Math.round(height / Math.min(380, Math.max(220, height / 3))));
  const bandH = height / bands;
  const unit = bandH / 9;
  const want = width / unit;

  const tiles: Tile[] = [];
  for (let b = 0; b < bands; b++) {
    const next = rng(b + 1);
    // Odd bands insist on a wide tile so a narrow screen still mixes shapes.
    const needWide = b % 2 === 1;
    let cells: Cell[] | null = null;
    for (let step = 0; !cells && step < want + 20; step++) {
      for (const n of [Math.round(want) + step, Math.round(want) - step]) {
        if (n <= 0 || !reachable(n) || Math.abs(n - want) / want > 0.15) continue;
        cells = compose(n, next, needWide);
        if (cells) break;
      }
    }
    cells ??= compose(nearestReachable(want), next, false) ?? [];

    const total = cells.reduce((sum, c) => sum + CELL_WIDTH[c], 0);
    const y = (b * height) / bands;
    const yEnd = ((b + 1) * height) / bands;
    const yMid = y + (yEnd - y) / 2;
    let cursor = 0;
    for (const c of cells) {
      const x = (cursor * width) / total;
      cursor += CELL_WIDTH[c];
      const w = (cursor * width) / total - x;
      if (c === "stack") {
        tiles.push({ x, y, w, h: yMid - y, shape: "wide" }, { x, y: yMid, w, h: yEnd - yMid, shape: "wide" });
      } else {
        tiles.push({ x, y, w, h: yEnd - y, shape: c === "portrait" ? "portrait" : "wide" });
      }
    }
  }
  return tiles;
}

function nearestReachable(want: number): number {
  for (let step = 0; ; step++) {
    for (const n of [Math.round(want) + step, Math.round(want) - step]) {
      if (n > 0 && reachable(n)) return n;
    }
  }
}

export interface Carved {
  tiles: Tile[];
  text: Tile | null;
  arrow: Tile | null;
}

const touches = (a: Tile, b: Tile) => Math.abs(a.x + a.w - b.x) < 1 || Math.abs(b.x + b.w - a.x) < 1;
const sameRow = (a: Tile, b: Tile) => Math.abs(a.y - b.y) < 1 && Math.abs(a.h - b.h) < 1;
const centreX = (t: Tile) => t.x + t.w / 2;
const centreY = (t: Tile) => t.y + t.h / 2;

export function carveWall(width: number, height: number): Carved {
  const all = layoutWall(width, height);
  if (all.length < 5) return { tiles: all, text: null, arrow: null };

  const bandH = Math.max(...all.map((t) => t.h));
  const isFull = (t: Tile) => t.h >= bandH * 0.9;
  const onBottom = (t: Tile) => t.y + t.h >= height - 1;
  const mid = { x: width / 2, y: height / 2 };
  const dist = (t: Tile) => Math.hypot(centreX(t) - mid.x, centreY(t) - mid.y) - 0.35 * t.w;
  const upper = all.filter((t) => isFull(t) && !onBottom(t));
  const pool = upper.length >= 1 ? upper : all.filter((t) => !onBottom(t));
  const first = (pool.length > 0 ? pool : all).reduce((best, t) => (dist(t) < dist(best) ? t : best));

  let text: Tile = first;
  let used = [first];
  const mate = all
    .filter((t) => t !== first && sameRow(t, first) && touches(t, first) && !onBottom(t))
    .sort((a, b) => dist(a) - dist(b))[0];
  if (mate) {
    const x = Math.min(first.x, mate.x);
    text = { x, y: first.y, w: Math.max(first.x + first.w, mate.x + mate.w) - x, h: first.h, shape: "wide" };
    used = [first, mate];
  }

  const room = all.filter((t) => onBottom(t) && !used.includes(t));
  const base = room.length > 0 ? room : all.filter((t) => !used.includes(t));
  const chosen = base.reduce((best, t) => {
    const score = (u: Tile) => Math.abs(centreX(u) - centreX(text)) + (u.w * u.h) / (bandH * 2);
    return score(t) < score(best) ? t : best;
  });

  const rest = all.filter((t) => !used.includes(t) && t !== chosen);
  if (!isFull(chosen)) return { tiles: rest, text, arrow: chosen };
  const half = chosen.h / 2;
  const keep: Tile = { ...chosen, h: half, shape: "wide" };
  const arrow: Tile = { ...chosen, y: chosen.y + half, h: chosen.h - half, shape: "wide" };
  return { tiles: [...rest, keep], text, arrow };
}

export function spreadTitles(tiles: Tile[], count: number): number[] {
  if (count <= 0) return tiles.map(() => 0);
  const used = new Array<number>(count).fill(0);
  const placed: { x: number; y: number }[][] = Array.from({ length: count }, () => []);
  return tiles.map((t) => {
    const c = { x: centreX(t), y: centreY(t) };
    const least = Math.min(...used);
    let best = -1;
    let bestGap = -1;
    for (let k = 0; k < count; k++) {
      if (used[k] > least + 1) continue;
      const far = placed[k].length === 0 ? Infinity : Math.min(...placed[k].map((p) => Math.hypot(p.x - c.x, p.y - c.y)));
      const gap = far - (used[k] - least) * 240;
      if (gap > bestGap) {
        best = k;
        bestGap = gap;
      }
    }
    used[best]++;
    placed[best].push(c);
    return best;
  });
}

export const GAP = 10;

export const EDGE_INSET = GAP / 2;

export interface Box {
  left: number;
  top: number;
  width: number;
  height: number;
  padding: number;
}

export function snapTile(t: Tile): Box {
  const left = Math.round(t.x);
  const top = Math.round(t.y);
  return { left, top, width: Math.round(t.x + t.w) - left, height: Math.round(t.y + t.h) - top, padding: GAP / 2 };
}

export function nearestTile(tiles: Tile[], x: number, y: number): number {
  let best = -1;
  let bestDist = Infinity;
  tiles.forEach((t, i) => {
    const d = Math.hypot(Math.max(t.x - x, 0, x - (t.x + t.w)), Math.max(t.y - y, 0, y - (t.y + t.h)));
    if (d < bestDist) {
      bestDist = d;
      best = i;
    }
  });
  return best;
}

export function tilesTouching(tiles: Tile[], slot: Tile, bleed: number, width: number, height: number): number[] {
  const m = GAP + 2;
  const touching = (t: Tile) => t.x < slot.x + slot.w + m && t.x + t.w > slot.x - m && t.y < slot.y + slot.h + m && t.y + t.h > slot.y - m;
  const shown = (t: Tile) => t.x + t.w > bleed && t.x < bleed + width && t.y + t.h > bleed && t.y < bleed + height;
  return tiles.flatMap((t, i) => (touching(t) && shown(t) ? [i] : []));
}
