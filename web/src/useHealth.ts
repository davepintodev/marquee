import { useCallback, useEffect, useState } from "react";
import type { Api } from "./api/client";
import type { Health } from "./api/types";

export type HealthLoad =
  | { status: "loading" }
  // busy: a new check is running; the answer before it stays on screen until it lands.
  | { status: "ok"; data: Health; busy: boolean }
  | { status: "error"; message: string };

export function useHealth(api: Api) {
  const [load, setLoad] = useState<HealthLoad>({ status: "loading" });
  const [round, setRound] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    api.health(ctl.signal).then(
      (data) => !ctl.signal.aborted && setLoad({ status: "ok", data, busy: false }),
      (e) => {
        if (ctl.signal.aborted) return;
        setLoad({ status: "error", message: e instanceof Error ? e.message : "Something went wrong." });
      },
    );
    return () => ctl.abort();
  }, [api, round]);

  const again = useCallback(() => {
    setLoad((l) => (l.status === "ok" ? { ...l, busy: true } : { status: "loading" }));
    setRound((n) => n + 1);
  }, []);

  return { load, again };
}
