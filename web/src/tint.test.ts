import { describe, expect, test } from "vitest";
import { mostColourful, tintFromPixels, type Tint } from "./tint";

const pixels = (...rgb: [number, number, number][]) => Uint8ClampedArray.from(rgb.flatMap(([r, g, b]) => [r, g, b, 255]));

describe("tintFromPixels", () => {
  test.each([
    { name: "red", px: pixels([220, 30, 30]), h: 0 },
    { name: "yellow", px: pixels([220, 220, 30]), h: 60 },
    { name: "green", px: pixels([30, 220, 30]), h: 120 },
    { name: "cyan", px: pixels([30, 220, 220]), h: 180 },
    { name: "blue", px: pixels([30, 30, 220]), h: 240 },
    { name: "magenta", px: pixels([220, 30, 220]), h: 300 },
  ])("a $name image has hue $h and some mass", ({ px, h }) => {
    const t = tintFromPixels(px);
    expect(t.h).toBe(h);
    expect(t.mass).toBeGreaterThan(0);
  });

  test("a vivid sliver beats a grey field", () => {
    const grey: [number, number, number] = [120, 120, 120];
    const t = tintFromPixels(pixels(...Array.from({ length: 15 }, () => grey), [20, 20, 230]));
    expect(t.h).toBe(240);
  });

  test("a grey or near-black image has no mass, a cool default hue and the muted floor of saturation", () => {
    expect(tintFromPixels(pixels([128, 128, 128], [130, 128, 128]))).toEqual({ h: 215, s: 14, mass: 0 });
    expect(tintFromPixels(pixels([10, 0, 0], [0, 5, 0]))).toMatchObject({ h: 215, mass: 0 });
  });

  test("the more colourful the image, the more saturated the words, up to a cap", () => {
    const dull = tintFromPixels(pixels([150, 120, 120], [120, 120, 120], [120, 120, 120], [120, 120, 120]));
    const vivid = tintFromPixels(pixels([255, 0, 0], [255, 0, 0], [255, 0, 0], [255, 0, 0]));
    expect(vivid.s).toBeGreaterThan(dull.s);
    expect(vivid.s).toBe(52);
    expect(dull.s).toBeGreaterThanOrEqual(14);
  });

  test("an empty image does not divide by zero", () => {
    const t = tintFromPixels(new Uint8ClampedArray());
    expect(Number.isFinite(t.s)).toBe(true);
    expect(t.mass).toBe(0);
  });
});

describe("mostColourful", () => {
  const tint = (h: number, mass: number): Tint => ({ h, s: 30, mass });
  test.each([
    { name: "no posters", from: [] as (Tint | null)[], want: null },
    { name: "none could be read", from: [null, null], want: null },
    { name: "one", from: [tint(10, 1)], want: tint(10, 1) },
    { name: "the heaviest of several", from: [tint(10, 1), tint(200, 5), tint(300, 3)], want: tint(200, 5) },
    { name: "skips posters that could not be read", from: [null, tint(90, 2), null], want: tint(90, 2) },
    { name: "the first of a tie", from: [tint(10, 2), tint(20, 2)], want: tint(10, 2) },
  ])("$name", ({ from, want }) => {
    expect(mostColourful(from)).toEqual(want);
  });
});
