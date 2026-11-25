import { type KeyboardEvent, useEffect, useRef, useState } from "react";

interface Props {
  what: string;
  shared: string[];
  busy: boolean;
  onChoose: (reSearch: boolean) => void;
}

// Cancel takes focus when the question opens, so a stray Enter cannot delete anything.
export function WrongFile({ what, shared, busy, onChoose }: Props) {
  const [asking, setAsking] = useState(false);
  const open = useRef<HTMLButtonElement>(null);
  const cancel = useRef<HTMLButtonElement>(null);
  const moved = useRef(false);

  useEffect(() => {
    if (!moved.current) return;
    (asking ? cancel : open).current?.focus({ preventScroll: true });
  }, [asking]);

  const ask = (on: boolean) => {
    moved.current = true;
    setAsking(on);
  };

  if (!asking) {
    return (
      <button ref={open} type="button" className="ul-link wf-open" data-tone="bad" onClick={() => ask(true)}>
        Wrong file
      </button>
    );
  }
  return (
    <div
      className="wf"
      role="group"
      aria-label="Wrong file"
      onKeyDown={(e: KeyboardEvent) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          ask(false);
        }
      }}
    >
      <p className="wf-text">
        This deletes {what} from disk, with its subtitles, and blocks the release it came from when marquee knows it.
        {shared.length > 0 && (
          <>
            {" "}
            <strong>This file also holds {shared.join(", ")}.</strong> They go missing together.
          </>
        )}
      </p>
      <div className="wf-act" aria-live="polite">
        <button ref={cancel} type="button" className="rm-btn" onClick={() => ask(false)}>
          Cancel
        </button>
        <button type="button" className="rm-btn" data-danger="true" aria-disabled={busy} onClick={() => !busy && onChoose(false)}>
          Pick another release
        </button>
        <button type="button" className="rm-btn" data-danger="true" aria-disabled={busy} onClick={() => !busy && onChoose(true)}>
          Search automatically
        </button>
      </div>
    </div>
  );
}
