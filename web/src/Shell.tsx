import { useMemo, useState } from "react";
import { App } from "./App";
import { Unlock } from "./Unlock";
import { createApi } from "./api/client";
import { tokenStore } from "./token";

interface Props {
  baseUrl?: string;
  fetch?: typeof fetch;
  // The page being asked for; the server answers every client route with this UI.
  path?: string;
}

export function Shell({ baseUrl, fetch: f, path = window.location.pathname }: Props) {
  // /health stays a working address: it opens the same Health page the wall links to by hash.
  useState(() => {
    if (path.replace(/\/+$/, "") === "/health") window.history.replaceState(null, "", "/#/health");
  });
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
  return <App key={round} api={api} />;
}
