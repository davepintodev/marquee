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
  onSelect?: (title: TitleSummary) => void;
}

// items arrive in display order (library first, then trending). When
// there are fewer titles than tiles the list repeats so the screen is
// never left with holes; null means still loading.
export function Wall({ api, items, width, height, onSelect }: Props) {
  const slots = useMemo(() => layoutWall(width, height), [width, height]);
  return (
    <div className="wall" aria-label="Wall" data-testid="wall">
      {slots.map((s, i) => {
        const style = { left: s.x, top: s.y, width: s.w, height: s.h, padding: GAP / 2 };
        if (!items || items.length === 0) return <div key={i} className="tile skeleton" style={style} />;
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
