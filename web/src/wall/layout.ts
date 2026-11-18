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

// mulberry32: a deterministic generator, so a screen size always
// gives the same wall.
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
