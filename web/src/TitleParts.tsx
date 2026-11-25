import { type KeyboardEvent, type ReactNode, useEffect, useRef, useState } from "react";
import type { Title } from "./api/types";
import { IconBack, IconChevron, IconTrash } from "./icons";

export function BackBar({ title, solid, onClose, back }: { title?: string; solid: boolean; onClose: () => void; back: React.Ref<HTMLButtonElement> }) {
  return (
    <header className="ts-bar" data-solid={solid}>
      <div className="ts-bar-in">
        <button ref={back} type="button" className="ts-back" onClick={onClose}>
          <IconBack />
          Back
        </button>
        {title && (
          <span className="ts-bar-title" aria-hidden="true">
            {title}
          </span>
        )}
      </div>
    </header>
  );
}

// Keep takes focus when the question opens, so a stray Enter cannot remove anything.
export function RemoveBlock({ title, busy, onRemove }: { title: Title; busy: boolean; onRemove: (deleteFiles: boolean) => void }) {
  const [confirming, setConfirming] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const open = useRef<HTMLButtonElement>(null);
  const keep = useRef<HTMLButtonElement>(null);
  const moved = useRef(false);

  useEffect(() => {
    if (!moved.current) return;
    (confirming ? keep : open).current?.focus({ preventScroll: true });
  }, [confirming]);

  const ask = (on: boolean) => {
    moved.current = true;
    setConfirming(on);
    setDeleteFiles(false);
  };

  return (
    <div
      className="rm"
      role="group"
      aria-label="Remove from marquee"
      data-asking={confirming || undefined}
      onKeyDown={(e: KeyboardEvent) => {
        if (e.key === "Escape" && confirming) {
          e.stopPropagation();
          ask(false);
        }
      }}
    >
      <div className="rm-text">
        <p className="rm-title">{confirming ? `Remove “${title.title}”?` : "Remove from marquee"}</p>
        <p className="rm-sub">
          {confirming && deleteFiles
            ? "marquee stops searching for it and deletes every file it has on disk, with the subtitles beside them. This cannot be undone."
            : "marquee stops searching for it. The files on disk stay."}
        </p>
        {confirming && (
          <label className="rm-check">
            <input type="checkbox" checked={deleteFiles} onChange={(e) => setDeleteFiles(e.target.checked)} />
            Also delete files from disk
          </label>
        )}
      </div>
      <div className="rm-act" aria-live="polite">
        {!confirming ? (
          <button ref={open} type="button" className="rm-btn" onClick={() => ask(true)}>
            <IconTrash />
            Remove…
          </button>
        ) : (
          <>
            <button ref={keep} type="button" className="rm-btn" onClick={() => ask(false)}>
              Keep it
            </button>
            <button
              type="button"
              className="rm-btn"
              data-danger="true"
              aria-label={`Yes, remove “${title.title}”${deleteFiles ? " and delete its files" : ""}`}
              aria-disabled={busy}
              onClick={() => onRemove(deleteFiles)}
            >
              <IconTrash />
              Remove
            </button>
          </>
        )}
      </div>
    </div>
  );
}

export function Collapse({ label, aside, children }: { label: string; aside?: ReactNode; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <div className="ts-sec-head ts-hist-head">
        <h2 className="collapse">
          <button type="button" className="fold-btn" aria-expanded={open} aria-controls="detail-collapse" onClick={() => setOpen(!open)}>
            {label}
            <IconChevron open={open} />
          </button>
        </h2>
        {aside}
      </div>
      <div id="detail-collapse" hidden={!open}>
        {children}
      </div>
    </>
  );
}
