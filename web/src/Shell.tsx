import { useEffect, useMemo, useState } from "react";
import { App } from "./App";
import { calm } from "./motion";
import { Unlock, type UnlockPhase } from "./Unlock";
import { createApi } from "./api/client";
import { tokenStore } from "./token";

interface Props {
  baseUrl?: string;
  fetch?: typeof fetch;
  // The page being asked for; the server answers every client route with this UI.
  path?: string;
}

// How long the unlock screen takes to hand over to the app once its token is accepted; the CSS sequence is timed to fit.
const OPEN_MS = 1450;
const OPEN_CALM_MS = 300;
// A marquee that never answers must not hold the screen shut: after this the app shows its own errors.
const GIVE_UP_MS = 6000;

export function Shell({ baseUrl, fetch: f, path = window.location.pathname }: Props) {
  // /health stays a working address: it opens the same Health page the wall links to by hash.
  useState(() => {
    if (path.replace(/\/+$/, "") === "/health") window.history.replaceState(null, "", "/#/health");
  });
  const store = useMemo(() => tokenStore(), []);
  // rejected: a token was sent and marquee refused it, as opposed to none being stored yet.
  const [lock, setLock] = useState<{ rejected: boolean } | null>(null);
  const [round, setRound] = useState(0);
  // The screen stays over the app from the moment a token is sent until marquee has taken it (checking),
  // then eases away (open). A refusal puts it back to asking without a flash of app.
  const [phase, setPhase] = useState<UnlockPhase | "gone">("gone");
  const [attempt, setAttempt] = useState(0);

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
          setPhase("ask");
          setAttempt((n) => n + 1);
        },
        onAuthorized: () => setPhase((p) => (p === "checking" ? "open" : p)),
      }),
    [baseUrl, f, store],
  );

  useEffect(() => {
    if (phase === "checking") {
      const t = setTimeout(() => setPhase("open"), GIVE_UP_MS);
      return () => clearTimeout(t);
    }
    if (phase === "open") {
      const t = setTimeout(() => setPhase("gone"), calm() ? OPEN_CALM_MS : OPEN_MS);
      return () => clearTimeout(t);
    }
  }, [phase]);

  const covered = lock !== null || phase !== "gone";
  return (
    <>
      {!lock && (
        <div className="shell-app" inert={covered} data-handover={phase === "open" ? "" : undefined}>
          <App key={round} api={api} />
        </div>
      )}
      {covered && (
        <Unlock
          rejected={lock?.rejected ?? false}
          attempt={attempt}
          phase={phase === "gone" ? "ask" : phase}
          onSubmit={(token) => {
            store.set(token);
            setLock(null);
            setPhase("checking");
            setRound((n) => n + 1);
          }}
        />
      )}
    </>
  );
}
