import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import type { Api } from "./api/client";
import type { Download } from "./api/types";
import { Art } from "./Art";
import { calm } from "./motion";
import { hrefOf } from "./route";
import { bytesOf } from "./words";

const stateWords: Record<Download["state"], string> = {
  queued: "Queued",
  downloading: "Downloading",
  verifying: "Verifying",
  importing: "Importing",
};

export function DownloadCard({ api, d }: { api: Api; d: Download }) {
  const state = d.stalled ? "stalled" : d.state;
  return (
    <li className="show" data-state={state}>
      <span className="show-art">
        <Art src={api.imageUrl(`/api/images/backdrop/${d.titleId}`)} title={d.titleName} year={0} />
        <span className="show-bar" role="progressbar" aria-label={`${d.titleName} progress`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={d.percent}>
          <i style={{ width: `${d.percent}%` }} />
        </span>
      </span>
      <div className="show-body">
        <div className="show-head">
          <p className="show-title">
            <a href={hrefOf({ name: "title", id: d.titleId })}>{d.titleName}</a>
            {d.episode && <span className="show-ep">{d.episode.toUpperCase()}</span>}
          </p>
          <span className="show-percent">{`${d.percent}%`}</span>
        </div>
        <p className="show-state">
          <i aria-hidden="true" />
          {d.stalled ? "Stalled" : stateWords[d.state]}
          {d.bytesTotal > 0 && <span>{`${bytesOf(d.bytesDone)} of ${bytesOf(d.bytesTotal)}`}</span>}
        </p>
        {d.stalled && <p className="show-note">No data has arrived for a while. marquee will try the next release.</p>}
        <p className="show-release">{d.releaseName}</p>
      </div>
    </li>
  );
}

export interface Upcoming {
  key: string;
  id: string;
  name: string;
  word: string;
  note?: string;
  state: "queued" | "searching";
}

export function WaitingCard({ api, n }: { api: Api; n: Upcoming }) {
  return (
    <li className="next" data-state={n.state}>
      <a href={hrefOf({ name: "title", id: n.id })} title={n.note}>
        <span className="next-art">
          <Art src={api.imageUrl(`/api/images/poster/${n.id}`)} title={n.name} year={0} />
          <span className="next-bar" aria-hidden="true">
            <i />
          </span>
        </span>
        <span className="next-name">{n.name}</span>
        <span className="next-word">{n.word}</span>
      </a>
    </li>
  );
}

// Cards scroll sideways a screenful at a time; the arrows appear only while some are out of view.
export function Carousel({ label, children }: { label: string; children: ReactNode }) {
  const list = useRef<HTMLUListElement>(null);
  const [edge, setEdge] = useState({ start: true, end: true });
  const measure = useCallback(() => {
    const el = list.current;
    if (!el) return;
    setEdge({ start: el.scrollLeft < 4, end: el.scrollLeft + el.clientWidth >= el.scrollWidth - 4 });
  }, []);
  useEffect(() => {
    const el = list.current;
    if (!el) return;
    measure();
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [measure, children]);
  const go = (dir: 1 | -1) => list.current?.scrollBy({ left: dir * list.current.clientWidth * 0.9, behavior: calm() ? "auto" : "smooth" });
  const overflow = !(edge.start && edge.end);
  const name = label.charAt(0).toUpperCase() + label.slice(1);
  return (
    <>
      {overflow && (
        <div className="reel-nav">
          <button type="button" aria-label={`Previous ${label}`} disabled={edge.start} onClick={() => go(-1)}>
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M15 5l-7 7 7 7" />
            </svg>
          </button>
          <button type="button" aria-label={`Next ${label}`} disabled={edge.end} onClick={() => go(1)}>
            <svg viewBox="0 0 24 24" aria-hidden="true">
              <path d="M9 5l7 7-7 7" />
            </svg>
          </button>
        </div>
      )}
      <ul className="reel" aria-label={name} ref={list} onScroll={measure}>
        {children}
      </ul>
    </>
  );
}
