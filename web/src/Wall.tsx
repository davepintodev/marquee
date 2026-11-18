import { useMemo } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Art } from "./Art";
import { BadgePill } from "./BadgePill";
import { layoutWall } from "./wall/layout";

const GAP = 10;

interface Props {
  api: Api;
  items: TitleSummary[] | null;
  width: number;
  height: number;
  top?: number;
  edge?: number;
  inert?: boolean;
  onSelect?: (title: TitleSummary) => void;
}

// items is null while the wall loads; an empty list (nothing yet, or a
// failed load) keeps still placeholders so the screen never pulses forever.
// When there are fewer titles than tiles the list repeats so the screen
// is never left with holes.
export function Wall({ api, items, width, height, top = 0, edge = 0, inert, onSelect }: Props) {
  const slots = useMemo(() => layoutWall(width - 2 * edge, height - top - edge), [width, height, top, edge]);
  return (
    <div className="wall" aria-label="Wall" aria-busy={items === null} data-testid="wall" style={{ top, left: edge, right: edge, bottom: edge }} inert={inert}>
      {slots.map((s, i) => {
        const style = { left: s.x, top: s.y, width: s.w, height: s.h, padding: GAP / 2 };
        if (!items || items.length === 0) return <div key={i} className="tile placeholder" style={style} />;
        const t = items[i % items.length];
        const path = s.shape === "portrait" ? t.posterPath : t.backdropPath;
        const label = t.badge ? `${t.title} (${t.year}), ${t.badge.label}` : `${t.title} (${t.year})`;
        return (
          <button key={i} type="button" className="tile" data-shape={s.shape} style={style} aria-label={label} title={t.title} onClick={() => onSelect?.(t)}>
            <span className="tile-frame">
              <Art src={api.imageUrl(path)} title={t.title} year={t.year} />
              <BadgePill badge={t.badge} />
            </span>
          </button>
        );
      })}
    </div>
  );
}
