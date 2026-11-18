import { useCallback, useEffect, useState } from "react";
import type { Api } from "./api/client";
import type { Health as HealthData } from "./api/types";
import { Page } from "./Page";

type Load = { status: "loading" } | { status: "ok"; data: HealthData } | { status: "error"; message: string };

const STATUS_WORD = { ok: "OK", warning: "Warning", error: "Problem" } as const;

const SUMMARY = {
  ok: "Everything is working.",
  warning: "Working, with something to look at.",
  error: "Something needs attention.",
} as const;

const REASON_WORD = { unmatched: "No match", ambiguous: "Several matches", unparsed_file: "Ignored file" } as const;

export function Health({ api, onClose }: { api: Api; onClose: () => void }) {
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [round, setRound] = useState(0);

  useEffect(() => {
    const ctl = new AbortController();
    api.health(ctl.signal).then(
      (data) => !ctl.signal.aborted && setLoad({ status: "ok", data }),
      (e) => {
        if (ctl.signal.aborted) return;
        setLoad({ status: "error", message: e instanceof Error ? e.message : "Something went wrong." });
      },
    );
    return () => ctl.abort();
  }, [api, round]);

  const again = useCallback(() => {
    setLoad({ status: "loading" });
    setRound((n) => n + 1);
  }, []);

  return (
    <Page heading="Health" onClose={onClose}>
      {load.status === "loading" && <p className="hint">Checking…</p>}
      {load.status === "error" && (
        <div className="notice inline" role="alert">
          <p>{load.message}</p>
          <button type="button" onClick={again}>
            Try again
          </button>
        </div>
      )}
      {load.status === "ok" && (
        <>
          <p className="page-summary" data-status={load.data.status}>
            {SUMMARY[load.data.status]}
          </p>
          <ul className="checks" aria-label="Checks">
            {load.data.checks.map((c) => (
              <li key={c.id} className="check" data-status={c.status}>
                <span className="check-word">{STATUS_WORD[c.status]}</span>
                <span className="check-name">{c.name}</span>
                <span className="check-message">{c.message}</span>
              </li>
            ))}
          </ul>
          {load.data.libraryProblems.length > 0 && (
            <>
              <h2>Library</h2>
              <p className="page-note">Folders marquee could not match, and files it ignored. Adding the title picks it up.</p>
              <ul className="checks" aria-label="Library problems">
                {load.data.libraryProblems.map((p) => (
                  <li key={p.path} className="check" data-status="warning">
                    <span className="check-word">{REASON_WORD[p.reason]}</span>
                    <span className="check-name check-path">{p.path}</span>
                    <span className="check-message">{p.message}</span>
                  </li>
                ))}
              </ul>
            </>
          )}
          <button type="button" className="btn-secondary" onClick={again}>
            Check again
          </button>
        </>
      )}
    </Page>
  );
}
