import { describe, expect, test } from "vitest";
import { bleedOf, carveWall, GAP, layoutWall, nearestTile, snapTile, spreadTitles, tilesTouching, type Tile } from "./layout";

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

describe.each(screens)("carved wall at $name ($w×$h)", ({ w, h }) => {
  test("two slots, no overlap with the posters or each other", () => {
    const { tiles, text, arrow } = carveWall(w, h);
    expect(text).not.toBeNull();
    expect(arrow).not.toBeNull();
    const rects = [text!, arrow!, ...tiles];
    for (let i = 0; i < rects.length; i++) {
      for (let j = i + 1; j < rects.length; j++) {
        const a = rects[i];
        const b = rects[j];
        const apart = a.x + a.w <= b.x + 1 || b.x + b.w <= a.x + 1 || a.y + a.h <= b.y + 1 || b.y + b.h <= a.y + 1;
        expect(apart).toBe(true);
      }
    }
  });

  test("the slots and the posters still cover the whole screen", () => {
    const { tiles, text, arrow } = carveWall(w, h);
    const { covered, overlap } = coverage([text!, arrow!, ...tiles], w, h);
    expect(overlap).toBe(0);
    expect(covered).toBe(w * h);
  });

  test("the text slot is near the middle, the arrow sits on the bottom edge below the words", () => {
    const { text, arrow } = carveWall(w, h);
    expect(Math.abs(text!.y + text!.h / 2 - h / 2)).toBeLessThan(h * 0.3);
    expect(arrow!.y + arrow!.h).toBeGreaterThanOrEqual(h - 1);
    expect(arrow!.y).toBeGreaterThanOrEqual(text!.y + text!.h - 1);
  });

  test("the text slot takes a pair of neighbouring tiles when the row has one", () => {
    const whole = layoutWall(w, h);
    const { text } = carveWall(w, h);
    const inside = whole.filter((t) => t.x >= text!.x - 1 && t.x + t.w <= text!.x + text!.w + 1 && t.y >= text!.y - 1 && t.y + t.h <= text!.y + text!.h + 1);
    expect(inside.length).toBeLessThanOrEqual(2);
    expect(inside.length).toBeGreaterThanOrEqual(1);
  });
});

test("a screen too small to carve keeps every tile and no slots", () => {
  const { tiles, text, arrow } = carveWall(120, 120);
  expect(text).toBeNull();
  expect(arrow).toBeNull();
  expect(tiles.length).toBeGreaterThan(0);
  expect(carveWall(0, 0)).toEqual({ tiles: [], text: null, arrow: null });
});

describe.each(screens)("title spread at $name ($w×$h)", ({ w, h }) => {
  test("a repeated title never sits on neighbouring tiles", () => {
    const { tiles } = carveWall(w, h);
    const pick = spreadTitles(tiles, 14);
    const slack = 2;
    tiles.forEach((a, i) => {
      tiles.forEach((b, j) => {
        if (j <= i || pick[i] !== pick[j]) return;
        const apart = a.x + a.w + slack < b.x || b.x + b.w + slack < a.x || a.y + a.h + slack < b.y || b.y + b.h + slack < a.y;
        expect(apart).toBe(true);
      });
    });
  });

  test("with enough titles each one is used once, in order", () => {
    const { tiles } = carveWall(w, h);
    expect(spreadTitles(tiles, tiles.length)).toEqual(tiles.map((_, i) => i));
  });
});

test("no titles give every tile the first slot of the list instead of crashing", () => {
  const { tiles } = carveWall(1440, 900);
  expect(spreadTitles(tiles, 0)).toEqual(tiles.map(() => 0));
});

test.each([
  { width: 390, bleed: 5 + 14 },
  { width: 640, bleed: 5 + 14 },
  { width: 1000, bleed: 5 + 22 },
  { width: 1440, bleed: 5 + 28 },
  { width: 5120, bleed: 5 + 28 },
])("the edge bleed at $width wide is $bleed, 14 to 28 past the half-gap", ({ width, bleed }) => {
  expect(bleedOf(width)).toBe(bleed);
});

describe.each(screens)("snapped gaps at $name ($w×$h)", ({ w, h }) => {
  test("every neighbour pair is exactly one gap apart, in whole pixels", () => {
    const { tiles, text, arrow } = carveWall(w, h);
    const boxes = [text!, arrow!, ...tiles].map(snapTile);
    const visible = boxes.map((b) => ({ x0: b.left + b.padding, x1: b.left + b.width - b.padding, y0: b.top + b.padding, y1: b.top + b.height - b.padding }));
    expect(boxes.every((b) => [b.left, b.top, b.width, b.height].every(Number.isInteger))).toBe(true);
    let pairs = 0;
    visible.forEach((a, i) => {
      visible.forEach((b, j) => {
        if (j <= i) return;
        const overlapY = Math.min(a.y1, b.y1) - Math.max(a.y0, b.y0);
        const overlapX = Math.min(a.x1, b.x1) - Math.max(a.x0, b.x0);
        if (overlapY > 0) {
          const gapX = Math.max(b.x0 - a.x1, a.x0 - b.x1);
          if (gapX < GAP + 1) {
            expect(gapX).toBe(GAP);
            pairs++;
          }
        }
        if (overlapX > 0) {
          const gapY = Math.max(b.y0 - a.y1, a.y0 - b.y1);
          if (gapY < GAP + 1) {
            expect(gapY).toBe(GAP);
            pairs++;
          }
        }
      });
    });
    expect(pairs).toBeGreaterThan(tiles.length);
  });
});

describe("nearestTile", () => {
  const tiles: Tile[] = [
    { x: 0, y: 0, w: 100, h: 100, shape: "portrait" },
    { x: 100, y: 0, w: 100, h: 100, shape: "portrait" },
    { x: 0, y: 100, w: 200, h: 100, shape: "wide" },
  ];
  test.each([
    { name: "inside the first tile", x: 40, y: 40, want: 0 },
    { name: "inside the second tile", x: 150, y: 10, want: 1 },
    { name: "inside the wide tile", x: 10, y: 190, want: 2 },
    { name: "outside, nearest the first", x: -30, y: 20, want: 0 },
    { name: "outside, nearest the second", x: 260, y: 20, want: 1 },
    { name: "outside, nearest the wide one", x: 100, y: 400, want: 2 },
  ])("$name", ({ x, y, want }) => {
    expect(nearestTile(tiles, x, y)).toBe(want);
  });
  test("no tiles, no answer", () => {
    expect(nearestTile([], 5, 5)).toBe(-1);
  });
});

describe("tilesTouching", () => {
  const slot: Tile = { x: 200, y: 100, w: 200, h: 300, shape: "wide" };
  const at = (x: number, y: number): Tile => ({ x, y, w: 100, h: 100, shape: "portrait" });
  const tiles = [at(100, 300), at(410, 300), at(250, 0), at(0, 300), at(500, 300)];
  test("the posters within a gap of the slot", () => {
    expect(tilesTouching(tiles, slot, 0, 2000, 2000)).toEqual([0, 1, 2]);
  });
  test("posters wholly past the screen edge do not count", () => {
    // With a 205px bleed the poster ending at x=200 lies in the bleed, and the one above ends at y=100.
    expect(tilesTouching(tiles, slot, 205, 2000, 2000)).toEqual([1]);
  });
});
