import { describe, expect, test } from "vitest";
import { layoutWall, type Tile } from "./layout";

const screens = [
  { name: "phone portrait", w: 390, h: 844 },
  { name: "phone landscape", w: 844, h: 390 },
  { name: "tablet", w: 1024, h: 768 },
  { name: "laptop", w: 1440, h: 900 },
  { name: "full hd", w: 1920, h: 1080 },
  { name: "ultrawide", w: 3440, h: 1440 },
  { name: "super ultrawide", w: 5120, h: 1440 },
];

const ratio = { portrait: 2 / 3, wide: 16 / 9 };

// Area of the screen covered by the union of the tiles, counted by
// painting each tile onto a 1px grid.
function coverage(tiles: Tile[], w: number, h: number) {
  const hit = new Uint8Array(w * h);
  let overlap = 0;
  for (const t of tiles) {
    for (let y = Math.round(t.y); y < Math.round(t.y + t.h); y++) {
      for (let x = Math.round(t.x); x < Math.round(t.x + t.w); x++) {
        if (x < 0 || y < 0 || x >= w || y >= h) continue;
        if (hit[y * w + x]) overlap++;
        hit[y * w + x] = 1;
      }
    }
  }
  return { covered: hit.reduce((a, b) => a + b, 0), overlap };
}

describe.each(screens)("wall layout at $name ($w×$h)", ({ w, h }) => {
  const tiles = layoutWall(w, h);

  test("tiles cover the screen with no gaps and no overlap", () => {
    const { covered, overlap } = coverage(tiles, w, h);
    expect(covered).toBe(w * h);
    expect(overlap).toBe(0);
  });

  test("no tile leaves the screen", () => {
    for (const t of tiles) {
      expect(t.x).toBeGreaterThanOrEqual(-0.001);
      expect(t.y).toBeGreaterThanOrEqual(-0.001);
      expect(t.x + t.w).toBeLessThanOrEqual(w + 0.001);
      expect(t.y + t.h).toBeLessThanOrEqual(h + 0.001);
    }
  });

  test("portrait tiles are close to 2:3 and wide tiles to 16:9", () => {
    for (const t of tiles) {
      const off = t.w / t.h / ratio[t.shape];
      expect(off, `${t.shape} tile ${t.w}×${t.h}`).toBeGreaterThan(0.8);
      expect(off, `${t.shape} tile ${t.w}×${t.h}`).toBeLessThan(1.25);
    }
  });

  test("no sliver: every tile is at least a tenth of the short side", () => {
    for (const t of tiles) {
      expect(Math.min(t.w, t.h)).toBeGreaterThanOrEqual(Math.min(w, h) / 10);
    }
  });

  test("mixes portrait and wide tiles", () => {
    expect(new Set(tiles.map((t) => t.shape))).toEqual(new Set(["portrait", "wide"]));
  });

  test("the first tile sits in the top-left corner", () => {
    expect(tiles[0]).toMatchObject({ x: 0, y: 0 });
  });
});

test("the same screen always gives the same layout", () => {
  expect(layoutWall(1440, 900)).toEqual(layoutWall(1440, 900));
});

test("a degenerate screen gives no tiles instead of crashing", () => {
  expect(layoutWall(0, 0)).toEqual([]);
  expect(layoutWall(-5, 300)).toEqual([]);
  expect(layoutWall(Number.NaN, 300)).toEqual([]);
});
