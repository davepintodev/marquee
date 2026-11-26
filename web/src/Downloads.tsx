import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import type { Api } from "./api/client";
import type { Download } from "./api/types";
import { groupPercent, groupState, groupSummary, pausable, runningOf, settling, type Group } from "./activity";
import { Art } from "./Art";
import { IconChevron } from "./icons";
import { hrefOf } from "./route";
import { useEscape } from "./useEscape";
import { leftOf, rateOf, sizeOf } from "./words";
import "./downloads.css";

const stateWords: Record<Download["state"], string> = {
  queued: "Queued",
  caching: "Caching",
  downloading: "Downloading",
  verifying: "Verifying",
  importing: "Importing",
  paused: "Paused",
};

export interface Ops {
  pause: (d: Download) => void;
  resume: (d: Download) => void;
  cancel: (d: Download) => void;
  pauseGroup: (g: Group) => void;
  resumeGroup: (g: Group) => void;
  cancelGroup: (g: Group) => void;
}

interface ConfirmProps {
  label: string;
  say: string;
  no: { text: string; name: string };
  yes: { text: string; name: string };
  onNo: () => void;
  onYes: () => void;
}

// Keep takes focus when the question opens, so a stray Enter cannot cancel anything.
function Confirm({ label, say, no, yes, onNo, onYes }: ConfirmProps) {
  const keep = useRef<HTMLButtonElement>(null);
  useEffect(() => keep.current?.focus({ preventScroll: true }), []);
  return (
    <div className="dl-confirm" role="group" aria-label={label}>
      <p>{say}</p>
      <button ref={keep} type="button" className="dl-btn" aria-label={no.name} onClick={onNo}>
        {no.text}
      </button>
      <button type="button" className="dl-btn" data-danger="true" aria-label={yes.name} onClick={onYes}>
        {yes.text}
      </button>
    </div>
  );
}

// The question closes by itself when nothing is left to stop: a download that moved on to verifying
// meanwhile would make the server answer 409.
function useAsk(stoppable: boolean) {
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  if (open && !stoppable) setOpen(false);
  const asking = open && stoppable;
  const dismiss = () => {
    setOpen(false);
    trigger.current?.focus({ preventScroll: true });
  };
  useEscape(asking, dismiss);
  return { asking, ask: () => setOpen(true), dismiss, answer: () => setOpen(false), trigger };
}

function successor(row: HTMLElement | null): HTMLElement | null {
  for (let n = row?.nextElementSibling; n; n = n.nextElementSibling) {
    const button = n.querySelector<HTMLElement>(".drow-act button");
    if (button) return button;
  }
  return row?.closest(".grp")?.querySelector<HTMLElement>(".grp-name a") ?? null;
}

function Row({ d, name, paused, alone, ops }: { d: Download; name: string; paused: boolean; alone: boolean; ops: Ops }) {
  const { asking, ask, dismiss, answer, trigger } = useAsk(!settling(d));
  const li = useRef<HTMLLIElement>(null);
  const label = d.episode ? `${d.episode.toUpperCase()} of ${name}` : name;
  const state = d.stalled ? "stalled" : d.state;
  const facts: string[] = [];
  if (d.state === "downloading" || d.state === "paused") {
    if (d.bytesTotal > 0 && d.percent > 0) facts.push(sizeOf(d.bytesDone, d.bytesTotal));
    if (d.state === "downloading" && d.bytesPerSecond > 0) facts.push(rateOf(d.bytesPerSecond));
    const left = d.state === "downloading" ? leftOf(d) : null;
    if (left) facts.push(left);
  }
  return (
    <li ref={li} className="drow" data-state={state}>
      <p className="drow-line">
        <b className={d.episode ? "drow-ep" : "drow-ep drow-rel"} title={d.releaseName}>
          {d.episode ? d.episode.toUpperCase() : d.releaseName}
        </b>
        <span className="drow-state">
          <i aria-hidden="true" />
          {d.stalled ? "Stalled" : stateWords[d.state]}
        </span>
        {d.state !== "queued" && <span className="drow-pct">{`${d.percent}%`}</span>}
      </p>
      {(facts.length > 0 || d.stalled) && <p className="drow-facts">{d.stalled ? "No data has arrived for a while. marquee will try the next release." : facts.join(" · ")}</p>}
      {!alone && d.state !== "queued" && (
        <span className="drow-bar" aria-hidden="true">
          <i style={{ width: `${d.percent}%` }} />
        </span>
      )}
      <div className="drow-act">
        {!paused && pausable(d) && (
          <button type="button" className="dl-btn" aria-label={`Pause ${label}`} onClick={() => ops.pause(d)}>
            Pause
          </button>
        )}
        {!paused && d.state === "paused" && (
          <button type="button" className="dl-btn" aria-label={`Resume ${label}`} onClick={() => ops.resume(d)}>
            Resume
          </button>
        )}
        {!settling(d) && (
          <button ref={trigger} type="button" className="dl-btn" data-tone="bad" aria-label={`Cancel ${label}`} aria-expanded={asking} onClick={ask}>
            Cancel
          </button>
        )}
      </div>
      {asking && (
        <Confirm
          label={`Cancel ${label}`}
          say="This blocks the release and searches for another."
          no={{ text: "Keep", name: `Keep ${label}` }}
          yes={{ text: "Yes, cancel", name: `Yes, cancel ${label}` }}
          onNo={dismiss}
          onYes={() => {
            answer();
            successor(li.current)?.focus({ preventScroll: true });
            ops.cancel(d);
          }}
        />
      )}
    </li>
  );
}

interface CardProps {
  api: Api;
  g: Group;
  paused: boolean;
  open: boolean;
  onToggle: () => void;
  ops: Ops;
}

export function GroupCard({ api, g, paused, open, onToggle, ops }: CardProps) {
  const rowsId = useId();
  const running = runningOf(g);
  const alone = g.downloads.length === 1 && g.waiting === 0;
  const stoppable = g.downloads.filter((d) => !settling(d));
  const { asking, ask, dismiss, answer, trigger } = useAsk(stoppable.length > 0);
  const rows = g.downloads.length > 0 && (alone || open);
  const percent = groupPercent(g);
  const state = g.downloads.length === 0 ? (g.wait?.state ?? "queued") : groupState(g);
  const count = g.downloads.length;
  return (
    <li className="grp" data-state={state}>
      <div className="grp-head">
        <span className="grp-art" aria-hidden="true">
          <Art src={api.imageUrl(`/api/images/poster/${g.id}`)} title={g.name} year={0} />
        </span>
        <div className="grp-main">
          <h3 className="grp-name">
            <a href={hrefOf({ name: "title", id: g.id })} title={g.wait ? waitNote(g) : undefined}>
              {g.name}
            </a>
          </h3>
          {!alone && <p className="grp-sum">{groupSummary(g)}</p>}
          {running.length > 0 && (
            <span className="grp-bar" role="progressbar" aria-label={`${g.name} progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={alone ? g.downloads[0].percent : percent}>
              <i style={{ width: `${alone ? g.downloads[0].percent : percent}%` }} />
            </span>
          )}
        </div>
        {count > 0 && !alone && (
          <div className="grp-act">
            {!paused && stoppable.some(pausable) && (
              <button type="button" className="dl-btn" aria-label={`Pause all downloads of ${g.name}`} onClick={() => ops.pauseGroup(g)}>
                Pause all
              </button>
            )}
            {!paused && g.downloads.some((d) => d.state === "paused") && (
              <button type="button" className="dl-btn" aria-label={`Resume downloads of ${g.name}`} onClick={() => ops.resumeGroup(g)}>
                Resume
              </button>
            )}
            {stoppable.length > 0 && (
              <button ref={trigger} type="button" className="dl-btn" data-tone="bad" aria-label={`Cancel all downloads of ${g.name}`} aria-expanded={asking} onClick={ask}>
                Cancel all
              </button>
            )}
            <button type="button" className="dl-more" aria-expanded={open} aria-controls={rowsId} aria-label={`${open ? "Hide" : "Show"} ${count} ${count === 1 ? "download" : "downloads"} of ${g.name}`} onClick={onToggle}>
              {count} {g.downloads.some((d) => d.episode) ? (count === 1 ? "episode" : "episodes") : "downloads"}
              <IconChevron open={open} />
            </button>
          </div>
        )}
      </div>
      {asking && (
        <Confirm
          label={`Cancel all downloads of ${g.name}`}
          say={`Cancel ${stoppable.length === 1 ? "the download" : `all ${stoppable.length} downloads`} of ${g.name}? Their releases are blocked and marquee searches again.`}
          no={{ text: "Keep", name: `Keep downloads of ${g.name}` }}
          yes={{ text: "Yes, cancel all", name: `Yes, cancel all downloads of ${g.name}` }}
          onNo={dismiss}
          onYes={() => {
            answer();
            ops.cancelGroup(g);
          }}
        />
      )}
      {rows && (
        <ul id={rowsId} className={alone ? "rows rows-alone" : "rows"} aria-label={`${g.name} downloads`}>
          {g.downloads.map((d) => (
            <Row key={d.id} d={d} name={g.name} paused={paused} alone={alone} ops={ops} />
          ))}
        </ul>
      )}
    </li>
  );
}

function waitNote(g: Group) {
  const eps = g.wait?.episodes ?? [];
  return eps.length > 0 ? `Waiting for ${eps.map((e) => e.toUpperCase()).join(", ")} to appear.` : "Waiting for a release to appear.";
}

interface SectionProps {
  paused: boolean;
  onPauseAll: () => void;
  onResumeAll: () => void;
  children: ReactNode;
}

// The one switch for every download. While it is on the section says so, so a row that reads Paused has its reason.
export function PauseAll({ paused, onPauseAll, onResumeAll, children }: SectionProps) {
  return (
    <>
      <div className="dl-top">
        <h2 id="act-downloading">Downloading</h2>
        <button type="button" className="dl-btn dl-all" aria-label={paused ? "Resume all downloads" : "Pause all downloads"} onClick={paused ? onResumeAll : onPauseAll}>
          {paused ? "Resume all" : "Pause all downloads"}
        </button>
      </div>
      <p className="board-hint">Files arriving right now.</p>
      {paused && (
        <p className="dl-paused" role="status">
          <b>Downloads paused</b>
          <span>Nothing new starts. A download you paused on its own stays paused after Resume all.</span>
        </p>
      )}
      {children}
    </>
  );
}
