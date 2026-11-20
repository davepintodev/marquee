import { flushSync } from "react-dom";
import type { Route } from "./route";

const NAME = "poster";

// The poster that was clicked is remembered, so a title shown more than once on the wall carries over from the right tile.
let clicked: Element | null = null;
if (typeof document !== "undefined") {
  document.addEventListener(
    "click",
    (e) => {
      const hit = e.target instanceof Element ? e.target.closest("[data-tid]") : null;
      if (hit) clicked = hit;
    },
    true,
  );
}

const reduced = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
const shown = (el: Element) => el.isConnected && !el.closest("[inert]");
const artOf = (tile: Element | null) => tile?.querySelector<HTMLElement>(".ptile-frame, .cell-art") ?? null;
const mark = (el: HTMLElement | null, on: boolean) => (on ? el?.style.setProperty("view-transition-name", NAME) : el?.style.removeProperty("view-transition-name"));

function tileFor(id: string) {
  if (clicked?.getAttribute("data-tid") === id && shown(clicked)) return artOf(clicked);
  for (const t of document.querySelectorAll(`[data-tid="${id}"]`)) if (shown(t)) return artOf(t);
  return null;
}

// A title screen opening carries the clicked poster over into its own; leaving carries it back to its tile.
// Browsers without view transitions, and a reader who asked for less motion, just switch.
export function morphRoute(prev: Route, next: Route, commit: () => void) {
  const opening = next.name === "title" && prev.name !== "title";
  const closing = prev.name === "title" && next.name !== "title";
  const id = next.name === "title" && opening ? next.id : prev.name === "title" && closing ? prev.id : null;
  if (id === null || typeof document.startViewTransition !== "function" || reduced()) return commit();

  const root = document.documentElement;
  const from = opening ? tileFor(id) : null;
  let to = null as HTMLElement | null;
  mark(from, true);
  root.dataset.morph = "";
  const run = document.startViewTransition(() => {
    mark(from, false);
    flushSync(commit);
    if (closing) {
      to = tileFor(id);
      to?.scrollIntoView({ block: "nearest" });
      mark(to, true);
    }
  });
  run.finished.finally(() => {
    mark(to, false);
    delete root.dataset.morph;
  });
}
