import { describe, expect, test } from "vitest";
import { carveWall } from "./layout";
import { lockColour, peelOf, planLockWall, PEEL_SPREAD, underForm } from "./lockwall";

const screens = [
  { name: "laptop", w: 1280, h: 800 },
  { name: "phone", w: 390, h: 844 },
  { name: "narrow phone", w: 320, h: 568 },
  { name: "phone landscape", w: 844, h: 390 },
];

describe.each(screens)("lock wall at $name ($w×$h)", ({ w, h }) => {
  const plan = planLockWall(w, h);

  test("every slot of the front page wall is a tile here: the chevron slot is filled", () => {
    const carved = carveWall(w, h);
    expect(carved.arrow).not.toBeNull();
    expect(plan.tiles).toContainEqual(carved.arrow);
  });

  test("the form's slot is empty of tiles unless the screen is too small, and then the tiles under it are marked", () => {
    const s = plan.slot;
    const inside = plan.tiles.filter((t) => t.x < s.x + s.w - 1 && t.x + t.w > s.x + 1 && t.y < s.y + s.h - 1 && t.y + t.h > s.y + 1);
    if (plan.hole) {
      expect(inside.length).toBeGreaterThan(0);
      expect(inside.every((t) => underForm(t, plan.hole))).toBe(true);
    } else {
      expect(inside).toEqual([]);
    }
  });

  test("the form's slot is big enough for the words and the field", () => {
    expect(plan.slot.w).toBeGreaterThanOrEqual(Math.min(300, w - 32));
    expect(plan.slot.h).toBeGreaterThanOrEqual(Math.min(190, h - 32));
  });
});

test("a hole is only used where the front page's own words slot is too small", () => {
  expect(planLockWall(1280, 800).hole).toBeNull();
  expect(planLockWall(320, 568).hole).not.toBeNull();
});

test("tile colours are the same on every call and stay calm: dark-wall saturation, a spread of hues", () => {
  const colours = Array.from({ length: 40 }, (_, i) => lockColour(i));
  expect(colours).toEqual(Array.from({ length: 40 }, (_, i) => lockColour(i)));
  for (const c of colours) {
    expect(c.s).toBeGreaterThanOrEqual(14);
    expect(c.s).toBeLessThanOrEqual(52);
    expect(c.h).toBeGreaterThanOrEqual(0);
    expect(c.h).toBeLessThan(360);
    expect(c.mass).toBe(c.s);
  }
  expect(new Set(colours.map((c) => c.h)).size).toBeGreaterThanOrEqual(6);
  expect(new Set(colours.map((c) => c.s)).size).toBeGreaterThanOrEqual(3);
});

describe("ring peel", () => {
  const slot = { x: 400, y: 300, w: 300, h: 200, shape: "wide" as const };
  const tile = (x: number, y: number) => ({ x, y, w: 120, h: 180, shape: "portrait" as const });
  const far = 900;

  test("tiles nearer the words start earlier, the farthest by the whole spread", () => {
    const near = peelOf(tile(280, 310), slot, far);
    const mid = peelOf(tile(100, 310), slot, far);
    const edge = peelOf(tile(1000, 800), slot, far);
    expect(near.delay).toBeLessThan(mid.delay);
    expect(mid.delay).toBeLessThan(edge.delay);
    expect(peelOf(tile(1500, 1500), slot, 100).delay).toBeCloseTo(PEEL_SPREAD, 5);
    expect(near.delay).toBeGreaterThanOrEqual(0);
  });

  test("each tile slides outward along the ray from the words' centre, a little", () => {
    const right = peelOf(tile(900, 340), slot, far);
    const left = peelOf(tile(0, 340), slot, far);
    const below = peelOf(tile(480, 700), slot, far);
    expect(right.dx).toBeGreaterThan(0);
    expect(left.dx).toBeLessThan(0);
    expect(below.dy).toBeGreaterThan(0);
    expect(Math.hypot(right.dx, right.dy)).toBeGreaterThan(90);
    expect(Math.hypot(right.dx, right.dy)).toBeLessThan(400);
  });

  test("a tile centred on the words does not divide by zero", () => {
    const r = peelOf({ x: 400, y: 300, w: 300, h: 200, shape: "wide" }, slot, far);
    expect(Number.isFinite(r.dx) && Number.isFinite(r.dy) && Number.isFinite(r.delay)).toBe(true);
  });
});
