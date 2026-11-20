import { useEffect, useMemo, useState, type CSSProperties } from "react";
import { mostColourful, tintOf, type Tint } from "./tint";

interface Props {
  onEnter: () => void;
  textStyle: CSSProperties;
  arrowStyle: CSSProperties;
  // The posters that touch the words, for the colour at rest.
  rest: string[];
  // The poster under (or nearest) the mouse pointer; null when it is elsewhere or there is none.
  pointerSrc: string | null;
}

// The colour answers the pointer: it is the dominant colour of the poster under it, and at rest (and on
// touch screens) of the most colourful poster touching the words.
function useTint(rest: string[], pointerSrc: string | null) {
  const [tint, setTint] = useState<Tint | null>(null);
  const restKey = rest.join("|");
  const restSrcs = useMemo(() => (restKey ? restKey.split("|") : []), [restKey]);
  useEffect(() => {
    let live = true;
    const found = pointerSrc ? tintOf(pointerSrc) : Promise.all(restSrcs.map(tintOf)).then(mostColourful);
    void found.then((t) => live && t && setTint(t));
    return () => {
      live = false;
    };
  }, [pointerSrc, restSrcs]);
  return tint ? ({ "--th": tint.h, "--ts": `${tint.s}%` } as CSSProperties) : {};
}

// Two empty slots in the poster wall: the words, and a plain chevron under them. Both open the app.
export function Invite({ onEnter, textStyle, arrowStyle, rest, pointerSrc }: Props) {
  const colour = useTint(rest, pointerSrc);
  return (
    <>
      <button type="button" className="slot slot-text" aria-label="What are we watching tonight?" style={{ ...textStyle, ...colour }} onClick={onEnter}>
        <span className="slot-words">
          <span>What are we</span>
          <span>
            watching <em>tonight?</em>
          </span>
        </span>
      </button>
      <button type="button" className="slot slot-arrow" style={{ ...arrowStyle, ...colour }} onClick={onEnter} tabIndex={-1} aria-hidden="true">
        <svg viewBox="0 0 24 24">
          <path d="M3 8l9 9 9-9" />
        </svg>
      </button>
    </>
  );
}
