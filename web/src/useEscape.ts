import { useEffect, useRef } from "react";

// Layers that want Esc, the last one opened on top. One listener on the document serves them all: it runs after
// any control's own Esc handling (which stops the event) and before App's window listener, which leaves the page.
const layers: { current: () => void }[] = [];

function onKeyDown(e: KeyboardEvent) {
  if (e.key !== "Escape" || layers.length === 0) return;
  e.stopPropagation();
  layers[layers.length - 1].current();
}

export function useEscape(active: boolean, close: () => void) {
  const latest = useRef(close);
  useEffect(() => {
    latest.current = close;
  });
  useEffect(() => {
    if (!active) return;
    layers.push(latest);
    if (layers.length === 1) document.addEventListener("keydown", onKeyDown);
    return () => {
      layers.splice(layers.indexOf(latest), 1);
      if (layers.length === 0) document.removeEventListener("keydown", onKeyDown);
    };
  }, [active]);
}
