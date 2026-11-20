export interface Tint {
  h: number;
  s: number;
  mass: number;
}

const SIZE = 16;

// Dominant hue of an image's RGBA pixels (chroma-weighted, so a vivid sliver beats a grey field) and
// how colourful it is overall, which sets the saturation of the words: a misty grey poster gives muted
// words. mass is how much colour the hue rests on; a grey image has none.
export function tintFromPixels(data: ArrayLike<number>): Tint {
  let x = 0;
  let y = 0;
  let total = 0;
  for (let i = 0; i < data.length; i += 4) {
    const r = data[i] / 255;
    const g = data[i + 1] / 255;
    const b = data[i + 2] / 255;
    const max = Math.max(r, g, b);
    const chroma = max - Math.min(r, g, b);
    total += chroma;
    if (chroma < 0.08 || max < 0.2) continue;
    const hue = max === r ? ((g - b) / chroma) % 6 : max === g ? (b - r) / chroma + 2 : (r - g) / chroma + 4;
    const w = chroma * max;
    x += Math.cos((hue * Math.PI) / 3) * w;
    y += Math.sin((hue * Math.PI) / 3) * w;
  }
  const mass = Math.hypot(x, y);
  const s = Math.round(Math.min(52, Math.max(14, 12 + (total / Math.max(1, data.length / 4)) * 120)));
  const h = mass === 0 ? 215 : Math.round((((Math.atan2(y, x) * 180) / Math.PI) % 360 + 360) % 360);
  return { h, s, mass };
}

// The most colourful of the tints that could be read, or null when none could.
export function mostColourful(tints: (Tint | null)[]): Tint | null {
  return tints.reduce<Tint | null>((best, t) => (t && (!best || t.mass > best.mass) ? t : best), null);
}

function sample(src: string): Promise<Tint | null> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => {
      try {
        const canvas = document.createElement("canvas");
        canvas.width = SIZE;
        canvas.height = SIZE;
        const ctx = canvas.getContext("2d");
        if (!ctx) return resolve(null);
        ctx.drawImage(img, 0, 0, SIZE, SIZE);
        resolve(tintFromPixels(ctx.getImageData(0, 0, SIZE, SIZE).data));
      } catch {
        resolve(null);
      }
    };
    img.onerror = () => resolve(null);
    img.src = src;
  });
}

const cache = new Map<string, Promise<Tint | null>>();

export function tintOf(src: string): Promise<Tint | null> {
  let t = cache.get(src);
  if (!t) {
    t = sample(src);
    cache.set(src, t);
  }
  return t;
}
