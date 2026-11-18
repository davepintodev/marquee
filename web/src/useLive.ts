import { useEffect, useRef } from "react";
import type { Api } from "./api/client";
import type { LiveEvent } from "./api/types";

// The handler can change on every render without reopening the stream.
export function useLive(api: Api, on: (e: LiveEvent) => void) {
  const latest = useRef(on);
  useEffect(() => {
    latest.current = on;
  });
  useEffect(() => api.events((e) => latest.current(e)), [api]);
}
