import { useEffect, useState } from "react";

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
  useEffect(() => {
    const on = () => setRoute(parseRoute(window.location.hash));
    window.addEventListener("hashchange", on);
    window.addEventListener("popstate", on);
    return () => {
      window.removeEventListener("hashchange", on);
      window.removeEventListener("popstate", on);
    };
  }, []);
  return route;
}

export function goTo(r: Route) {
  window.location.hash = hrefOf(r);
}

// Screens the app opened itself carry a marker in the history entry, so leaving them is the
// browser's own back and the back button and Esc agree on where they lead.
export function pushTo(r: Route, marker: "grid" | "page") {
  window.history.pushState({ marquee: marker }, "", hrefOf(r));
  window.dispatchEvent(new PopStateEvent("popstate"));
}

export function leaveTo(r: Route, marker: "grid" | "page") {
  if ((window.history.state as { marquee?: string } | null)?.marquee === marker) window.history.back();
  else goTo(r);
}
