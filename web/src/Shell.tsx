import { useMemo, useState } from "react";
import { App } from "./App";
import { Health } from "./Health";
import { Unlock } from "./Unlock";
import { createApi } from "./api/client";
import { tokenStore } from "./token";

interface Props {
  baseUrl?: string;
  fetch?: typeof fetch;
  // The page being asked for; the server answers every client route with this UI.
  path?: string;
}

// Shell picks the page and puts the token prompt in front of it whenever
// marquee answers 401.
export function Shell({ baseUrl, fetch: f, path = window.location.pathname }: Props) {
  const store = useMemo(() => tokenStore(), []);
  // rejected: a token was sent and marquee refused it, as opposed to none being stored yet.
  const [lock, setLock] = useState<{ rejected: boolean } | null>(null);
  const [round, setRound] = useState(0);

  const api = useMemo(
    () =>
      createApi({
        baseUrl,
        fetch: f,
        getToken: store.get,
        onUnauthorized: (sent) => {
          // A late answer to a request sent before the token changed says nothing about the new one.
          if (sent !== store.get()) return;
          store.clear();
          setLock({ rejected: sent !== null });
        },
      }),
    [baseUrl, f, store],
  );

  if (lock) {
    return (
      <Unlock
        rejected={lock.rejected}
        onSubmit={(token) => {
          store.set(token);
          setLock(null);
          setRound((n) => n + 1);
        }}
      />
    );
  }
  const page = path.replace(/\/+$/, "");
  return page === "/health" ? <Health key={round} api={api} /> : <App key={round} api={api} />;
}
