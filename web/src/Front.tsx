import { useMemo, useState, type CSSProperties, type PointerEvent } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Art } from "./Art";
import { Invite } from "./Invite";
import { bleedOf, carveWall, nearestTile, snapTile, spreadTitles, tilesTouching, type Tile } from "./wall/layout";

interface Props {
  api: Api;
  items: TitleSummary[] | null;
  width: number;
  height: number;
  onEnter: () => void;
  onSelect: (title: TitleSummary) => void;
}

// The front page: posters bleeding off every edge, with the words and an arrow in two empty slots.
// items is null while the wall loads; an empty list (nothing yet, or a failed load) keeps still
// placeholders so the screen never pulses forever. With fewer titles than tiles the list repeats,
// spread so a title is never next to itself.
export function Front({ api, items, width, height, onEnter, onSelect }: Props) {
  const bleed = bleedOf(width);
  const w = width + 2 * bleed;
  const h = height + 2 * bleed;
  const { tiles, text, arrow } = useMemo(() => carveWall(w, h), [w, h]);
  const titles = items && items.length > 0 ? items : null;
  const placed = useMemo(() => {
    if (!titles) return null;
    return spreadTitles(tiles, titles.length).map((k, i) => {
      const t = titles[k];
      return { title: t, src: api.imageUrl(tiles[i].shape === "portrait" ? t.posterPath : t.backdropPath) };
    });
  }, [api, tiles, titles]);

  const origin = text ?? { x: w / 2, y: h / 2, w: 0, h: 0 };
  const ox = origin.x + origin.w / 2;
  const oy = origin.y + origin.h / 2;

  // Part of a slot can lie past the screen edge; the words keep inside what shows.
  const hidden = (s: Tile): CSSProperties =>
    ({
      "--il": `${Math.max(0, bleed - s.x)}px`,
      "--ir": `${Math.max(0, s.x + s.w - (bleed + width))}px`,
      "--it": `${Math.max(0, bleed - s.y)}px`,
      "--ib": `${Math.max(0, s.y + s.h - (bleed + height))}px`,
    }) as CSSProperties;

  const rest = useMemo(() => (placed && text ? tilesTouching(tiles, text, bleed, width, height).map((i) => placed[i].src) : []), [placed, tiles, text, bleed, width, height]);

  const [hover, setHover] = useState<number | null>(null);
  const track = (e: PointerEvent<HTMLDivElement>) => {
    if (e.pointerType !== "mouse" || (e.target as HTMLElement).closest(".slot")) return;
    const box = e.currentTarget.getBoundingClientRect();
    const i = nearestTile(tiles, e.clientX - box.left, e.clientY - box.top);
    setHover(i >= 0 ? i : null);
  };

  return (
    <div
      className="pwall"
      aria-label="Posters"
      aria-busy={items === null}
      style={{ left: -bleed, top: -bleed, width: w, height: h, "--bleed": `${bleed}px` } as CSSProperties}
      onPointerMove={track}
      onPointerLeave={() => setHover(null)}
    >
      {tiles.map((s, i) => {
        const r = Math.round(Math.hypot(s.x + s.w / 2 - ox, s.y + s.h / 2 - oy) * 0.5);
        const style = { ...snapTile(s), "--r": r } as CSSProperties;
        if (!placed) return <div key={i} className="ptile pplaceholder" style={style} />;
        const { title: t, src } = placed[i];
        const label = t.badge ? `${t.title} (${t.year}), ${t.badge.label}` : `${t.title} (${t.year})`;
        return (
          <button key={i} type="button" className="ptile" data-shape={s.shape} style={style} aria-label={label} title={label} onClick={() => onSelect(t)}>
            <span className="ptile-frame">
              <Art src={src} title={t.title} year={t.year} />
            </span>
          </button>
        );
      })}
      {placed && text && arrow && (
        <Invite onEnter={onEnter} rest={rest} pointerSrc={hover !== null ? (placed[hover]?.src ?? null) : null} textStyle={{ ...snapTile(text), ...hidden(text) }} arrowStyle={snapTile(arrow)} />
      )}
    </div>
  );
}
