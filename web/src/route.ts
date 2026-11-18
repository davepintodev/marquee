import { useEffect, useState } from "react";

export type Route = { name: "wall" } | { name: "activity" } | { name: "health" } | { name: "title"; id: string };

export function parseRoute(hash: string): Route {
  if (hash === "#/activity") return { name: "activity" };
  if (hash === "#/health") return { name: "health" };
  const m = /^#\/title\/((?:movie|series)-\d+)$/.exec(hash);
  if (m) return { name: "title", id: m[1] };
  return { name: "wall" };
}

export function hrefOf(r: Route) {
  switch (r.name) {
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
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function goTo(r: Route) {
  window.location.hash = hrefOf(r);
}
