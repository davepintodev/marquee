import { type KeyboardEvent, useEffect, useId, useRef, useState } from "react";
import type { Title } from "./api/types";
import { ago, inHours, reasonLabel } from "./words";

export function Attempts({ title }: { title: Title }) {
  const failed = (title.history ?? []).filter((h) => h.kind === "failed").reverse();
  const next = title.nextSearchAt;
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLSpanElement>(null);
  const tip = useId();

  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);

  if (failed.length === 0 && !next) return null;
  const summary = title.historySummary || (next ? `Next search in ${inHours(next)}.` : "");

  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key !== "Escape" || !open) return;
    e.stopPropagation();
    setOpen(false);
  };

  return (
    <span className="ts-att" ref={root} onKeyDown={onKeyDown}>
      {next && <span className="ts-att-next">{`next try in ${inHours(next)}`}</span>}
      <button type="button" className="ts-att-info" aria-label="About these attempts" aria-expanded={open} aria-controls={tip} onClick={() => setOpen(!open)}>
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <circle cx="8" cy="8" r="6.6" />
          <path d="M6.2 6.3a1.9 1.9 0 1 1 2.7 1.7c-.6.3-.9.7-.9 1.3" />
          <circle cx="8" cy="11.6" r=".5" className="dot" />
        </svg>
      </button>
      <span className="ts-att-tip" id={tip} role="tooltip" data-open={open || undefined}>
        <b>{summary}</b>
        {failed.length > 0 && (
          <ul>
            {failed.map((h) => (
              <li key={h.id} data-reason={h.reason ?? "other"}>
                <i aria-hidden="true" />
                {`${reasonLabel(h.reason)}, ${ago(h.at)}`}
              </li>
            ))}
          </ul>
        )}
      </span>
    </span>
  );
}
