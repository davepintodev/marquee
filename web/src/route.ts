import { useEffect, useRef, useState } from "react";
import { morphRoute } from "./morph";

export type Route = { name: "wall" } | { name: "grid" } | { name: "activity" } | { name: "health" } | { name: "title"; id: string };

export function parseRoute(hash: string): Route {
  if (hash === "#/grid") return { name: "grid" };
  if (hash === "#/activity") return { name: "activity" };
  if (hash === "#/health") return { name: "health" };
  const m = /^#\/title\/((?:movie|series)-\d+)$/.exec(hash);
  if (m) return { name: "title", id: m[1] };
  return { name: "wall" };
}

export function hrefOf(r: Route) {
  switch (r.name) {
    case "grid":
      return "#/grid";
    case "activity":
      return "#/activity";
    case "health":
      return "#/health";
    case "title":
      return `#/title/${r.id}`;
    case "wall":
      return "#/";
  }
}

export function useRoute() {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash));
  const shown = useRef(route);
  useEffect(() => {
    const on = () => {
      const next = parseRoute(window.location.hash);
      const prev = shown.current;
      shown.current = next;
      morphRoute(prev, next, () => setRoute(next));
    };
    window.addEventListener("hashchange", on);
    window.addEventListener("popstate", on);
    return () => {
      window.removeEventListener("hashchange", on);
      window.removeEventListener("popstate", on);
    };
  }, []);
  return route;
}

function goTo(r: Route) {
  window.location.hash = hrefOf(r);
}

// Screens the app opened itself carry a marker in the history entry, so leaving them is the
// browser's own back and the back button and Esc agree on where they lead.
export function pushTo(r: Route, marker: "grid" | "page") {
  window.history.pushState({ marquee: marker, from: parseRoute(window.location.hash).name }, "", hrefOf(r));
  window.dispatchEvent(new PopStateEvent("popstate"));
}

// Where Back on a title screen goes: "library" is always the grid; "origin" is whichever screen opened the title.
export const BACK_FROM_TITLE: "library" | "origin" = "library";

export function leaveTitle() {
  const state = window.history.state as { marquee?: string; from?: string } | null;
  if (state?.marquee !== "page") return goTo({ name: "grid" });
  if (BACK_FROM_TITLE === "origin" || state.from === "grid") return window.history.back();
  // Opened from the wall: the grid takes the title's place above the wall's entry, so the browser's back still reaches the wall.
  window.history.replaceState({ marquee: "grid" }, "", hrefOf({ name: "grid" }));
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export function leaveTo(r: Route, marker: "grid" | "page") {
  if ((window.history.state as { marquee?: string } | null)?.marquee === marker) window.history.back();
  else goTo(r);
}
