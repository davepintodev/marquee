import { Fragment, useId, useState } from "react";
import type { Api } from "./api/client";
import type { Health as HealthData } from "./api/types";
import { ForgetToken } from "./ForgetToken";
import { IconChevron } from "./icons";
import { Page } from "./Page";
import { useHealth } from "./useHealth";
import { useViewport } from "./useViewport";
import { Busy, HealthSkeleton, Say } from "./Skeleton";
import { ErrorState } from "./State";

type Check = HealthData["checks"][number];
type Status = Check["status"];

const STATUS_WORD = { ok: "OK", warning: "Warning", error: "Problem" } as const;
const REASON_WORD = { unmatched: "No match", ambiguous: "More than one match", unparsed_file: "Ignored file" } as const;

// The path a title takes, in the order it takes it. A check this list does not know goes at the end.
const PATH = ["fake_data", "tmdb", "aiostreams", "dry_run", "temp_path", "library_paths", "jellyfin"];

type Stop = { check: Check; gate: boolean; out: "flow" | "paused" | "broken" | "end" };

// A dry-run that is on is a gate: nothing downstream of it is moving. A failed stop breaks the link that leads to it.
function stops(checks: Check[]): Stop[] {
  const rank = (c: Check) => {
    const i = PATH.indexOf(c.id);
    return i < 0 ? PATH.length : i;
  };
  const ordered = [...checks].sort((a, b) => rank(a) - rank(b));
  let closed = false;
  return ordered.map((check, i) => {
    const gate = check.id === "dry_run" && check.status !== "ok";
    if (gate) closed = true;
    const next = ordered[i + 1];
    const out = !next ? "end" : next.status === "error" ? "broken" : closed ? "paused" : "flow";
    return { check, gate, out };
  });
}

// One answer to the one question: can marquee be trusted to download? `used` is the check the answer came from.
function verdict(checks: Check[]): { title: string; why: string; used?: Check } {
  const blocker = checks.find((c) => c.status === "error" && c.id !== "jellyfin");
  if (blocker) return { title: "Can’t download right now", why: blocker.message, used: blocker };
  const dry = checks.find((c) => c.id === "dry_run" && c.status !== "ok");
  if (dry) return { title: "Not downloading yet", why: dry.message, used: dry };
  const soft = checks.find((c) => c.status === "error") ?? checks.find((c) => c.status === "warning");
  if (soft) return { title: soft.status === "error" ? "Ready to download" : "Ready, with a note", why: soft.message, used: soft };
  return { title: "Ready to download", why: "Every check passed." };
}

function Path({ value }: { value: string }) {
  return (
    <>
      {value.split("/").map((p, i) => (
        <Fragment key={i}>
          {i > 0 && "/"}
          {i > 0 && <wbr />}
          {p}
        </Fragment>
      ))}
    </>
  );
}

const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

// A plain-text control that folds and unfolds one region.
function Fold({ open, onToggle, controls, children }: { open: boolean; onToggle: () => void; controls?: string; children: string }) {
  return (
    <button type="button" className="bd-act bd-fold" aria-expanded={open} aria-controls={controls} onClick={onToggle}>
      {children}
      <IconChevron open={open} />
    </button>
  );
}

export function Health({ api, onClose, onForget }: { api: Api; onClose: () => void; onForget?: () => void }) {
  const { load, again } = useHealth(api);
  return (
    <Page heading="Health" onClose={onClose} className="page-board">
      <div className="bd" aria-busy={load.status === "loading" || (load.status === "ok" && load.busy)}>
        {load.status === "loading" && (
          <>
            <Say>Checking</Say>
            <HealthSkeleton />
          </>
        )}
        {load.status === "error" && (
          <ErrorState fit="page" action={{ label: "Try again", onClick: again }}>
            {load.message}
          </ErrorState>
        )}
        {load.status === "ok" && <Board data={load.data} busy={load.busy} again={again} onForget={onForget} />}
      </div>
    </Page>
  );
}

function Board({ data, busy, again, onForget }: { data: HealthData; busy: boolean; again: () => void; onForget?: () => void }) {
  const v = verdict(data.checks);
  const line = stops(data.checks);
  const n = (s: Status) => data.checks.filter((c) => c.status === s).length;
  const counts = [n("error") > 0 && plural(n("error"), "problem", "problems"), n("warning") > 0 && plural(n("warning"), "warning", "warnings"), `${n("ok")} passing`]
    .filter(Boolean)
    .join(" · ");
  const lineId = useId();
  const [pathOpen, setPathOpen] = useState(false);
  const [libOpen, setLibOpen] = useState(false);
  const pathId = useId();
  const libId = useId();
  // Matches `.bd-wrap` `@container (min-width: 56rem)` inside `.page-layer`'s 2 * 16px edge padding so
  // `aria-controls` never points at `.bd-detail` while CSS hides it on narrow screens; the rem follows the root font size.
  const { width } = useViewport();
  const rootPx = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
  const wide = width >= 56 * rootPx + 32;
  const problems = data.libraryProblems;
  const totals = data.libraryTotals;
  const columns = line.map((s) => `minmax(0, ${s.gate ? 1.4 : s.check.status === "error" ? 1.3 : 1}fr)`).join(" ");

  return (
    <div className="bd-body" data-busy={busy}>
      <header className="bd-verdict" data-blocked={v.used?.status === "error" && v.used.id !== "jellyfin"}>
        <h2>{v.title}</h2>
        <p className="bd-why">{v.why}</p>
        <div className="bd-bar">
          <p className="bd-counts">{counts}</p>
          <button type="button" className="bd-act" aria-disabled={busy} onClick={() => {
              if (busy) return;
              again();
            }}>
            {busy ? <Busy say="Checking" width={76} /> : "Check again"}
          </button>
        </div>
      </header>

      <section className="bd-sec bd-wrap" aria-label="Checks" data-open={pathOpen}>
        <p className="bd-note">A title’s path, from search to Jellyfin. A dashed stretch means nothing is moving there.</p>
        <ol id={lineId} className="bd-line" aria-label="Checks, in the order a title travels" style={{ "--cols": columns } as React.CSSProperties}>
          {line.map(({ check: c, gate, out }) => (
            <li key={c.id} className="bd-stop" data-status={c.status} data-gate={gate} data-out={out} data-id={c.id}>
              <span className="bd-mk" aria-hidden="true">
                <i />
              </span>
              <span className="bd-link" aria-hidden="true" />
              <span className="bd-card">
                <span className="bd-name">
                  {c.name}
                  <span className="bd-word">{STATUS_WORD[c.status]}</span>
                </span>
                {pathOpen && <span className="bd-msg bd-inline">{c.message}</span>}
              </span>
            </li>
          ))}
        </ol>
        <Fold open={pathOpen} onToggle={() => setPathOpen((o) => !o)} controls={pathOpen ? (wide ? pathId : lineId) : undefined}>
          {pathOpen ? "Hide details" : "Show details"}
        </Fold>
        {pathOpen && (
          <ul id={pathId} className="bd-detail" aria-label="Check details">
            {line.map(({ check: c }) => (
              <li key={c.id} className="bd-row" data-status={c.status}>
                <span className="bd-rname">
                  <i aria-hidden="true" />
                  {c.name}
                </span>
                <span className="bd-msg">{c.message}</span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="bd-sec" aria-label="Library">
        <h2 className="bd-h">Library</h2>
        <p className="bd-stats">
          <span className="bd-tot">
            {plural(totals.movies, "movie", "movies")} · {plural(totals.episodes, "episode", "episodes")}
          </span>
          <span className="bd-probn">{problems.length > 0 ? plural(problems.length, "problem", "problems") : "No problems"}</span>
          {problems.length > 0 && (
            <Fold open={libOpen} onToggle={() => setLibOpen((o) => !o)} controls={libOpen ? libId : undefined}>
              {libOpen ? "Hide problems" : "Show problems"}
            </Fold>
          )}
        </p>
        {libOpen && problems.length > 0 && (
          <div id={libId} className="bd-libbody">
            <p className="bd-note">Folders marquee could not match, and files it ignored. Adding the title picks it up.</p>
            <ul className="bd-probs" aria-label="Library problems">
              {problems.map((p) => (
                <li key={p.path} className="bd-prob">
                  <span className="bd-reason">
                    <i aria-hidden="true" />
                    {REASON_WORD[p.reason]}
                  </span>
                  <span className="bd-path">
                    <Path value={p.path} />
                  </span>
                  <span className="bd-msg">{p.message}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </section>

      <section className="bd-sec bd-token" aria-label="API token">
        <h2 className="bd-h">API token</h2>
        {onForget ? (
          <p className="bd-note">
            This device is unlocked with the token saved in this browser. <ForgetToken onForget={onForget} />
          </p>
        ) : (
          <p className="bd-note">This marquee does not ask for a token.</p>
        )}
      </section>
    </div>
  );
}
