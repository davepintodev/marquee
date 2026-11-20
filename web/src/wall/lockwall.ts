import type { Tint } from "../tint";
import { carveWall, type Tile } from "./layout";

export interface LockPlan {
  tiles: Tile[];
  slot: Tile;
  hole: Tile | null;
}

// Room the form needs: the slot the front page carves for its words when it is big enough, else a hole of this size in the middle.
const NEED = { w: 300, h: 190 };
const HOLE_MAX = { w: 440, h: 230 };
const HOLE_MARGIN = 16;

// The front page's own layout, with its chevron slot filled by one more tile so no slot stays empty.
export function planLockWall(width: number, height: number): LockPlan {
  const c = carveWall(width, height);
  const tiles = c.arrow ? [...c.tiles, c.arrow] : c.tiles;
  if (c.text && c.text.w >= NEED.w && c.text.h >= NEED.h) return { tiles, slot: c.text, hole: null };
  const w = Math.min(HOLE_MAX.w, width - 2 * HOLE_MARGIN);
  const h = Math.min(HOLE_MAX.h, height - 2 * HOLE_MARGIN);
  const slot: Tile = { x: (width - w) / 2, y: (height - h) / 2, w, h, shape: "wide" };
  // Too small for the field: the front's words slot becomes a tile too, and the tiles under the form fade back.
  return { tiles: c.text ? [...tiles, c.text] : tiles, slot, hole: slot };
}

export function underForm(t: Tile, hole: Tile | null): boolean {
  if (!hole) return false;
  const m = 10;
  return !(t.x + t.w <= hole.x - m || t.x >= hole.x + hole.w + m || t.y + t.h <= hole.y - m || t.y >= hole.y + hole.h + m);
}

const HUES = [212, 198, 262, 18, 344, 148, 232, 38];
const SATURATIONS = [22, 30, 38, 26];

// The wall has no art before there is a session (the API refuses images), so each tile is a dark tint drawn from
// the token scale. The hue and saturation also stand in for a poster's colour when the words pick one up.
export function lockColour(i: number): Tint {
  const s = SATURATIONS[(i * 3 + (i >> 1)) % SATURATIONS.length];
  return { h: HUES[(i * 5 + (i >> 2)) % HUES.length], s, mass: s };
}

// Seconds between the nearest tile starting to peel and the farthest.
export const PEEL_SPREAD = 0.55;

export function peelOf(t: Tile, from: Tile, far: number): { dx: number; dy: number; delay: number } {
  const vx = t.x + t.w / 2 - (from.x + from.w / 2);
  const vy = t.y + t.h / 2 - (from.y + from.h / 2);
  const dist = Math.hypot(vx, vy);
  // Each tile slides about 100px outward along its own ray from the words, a little more the farther it is.
  const k = 100 / (dist || 1) + 0.1;
  return { dx: vx * k, dy: vy * k, delay: Math.pow(Math.min(1, dist / far), 0.85) * PEEL_SPREAD };
}
