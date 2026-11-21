import { useEffect, useId, useMemo, useRef, useState, type CSSProperties, type PointerEvent } from "react";
import { calm } from "./motion";
import { mostColourful } from "./tint";
import { EDGE_INSET, nearestTile, snapTile, tilesTouching } from "./wall/layout";
import { lockColour, peelOf, planLockWall, underForm } from "./wall/lockwall";
import { useViewport } from "./useViewport";

// ask: waiting for a token. checking: sent, marquee has not answered yet. open: accepted, the screen is handing over.
export type UnlockPhase = "ask" | "checking" | "open";

interface Props {
  rejected: boolean;
  // Counts refusals, so the screen can refocus and announce a second wrong token like the first.
  attempt: number;
  phase: UnlockPhase;
  onSubmit: (token: string) => void;
}

const BUTTON: Record<UnlockPhase, string> = { ask: "Unlock", checking: "Unlocking…", open: "Unlocked" };

const NUDGE_MS = 3000;
const MODIFIERS = new Set(["Shift", "CapsLock", "Control", "Alt", "Meta"]);

export function Unlock({ rejected, attempt, phase, onSubmit }: Props) {
  const fieldId = useId();
  const input = useRef<HTMLInputElement>(null);
  const wall = useRef<HTMLDivElement>(null);
  const [token, setToken] = useState("");
  // The red cue belongs to the token that was refused; typing a new one clears it.
  const [editedFor, setEditedFor] = useState(-1);
  const [need, setNeed] = useState(false);
  const [nudges, setNudges] = useState(0);
  const asking = phase === "ask";
  const empty = token.trim() === "";

  const view = useViewport();
  const bleed = -EDGE_INSET;
  const w = view.width + 2 * bleed;
  const h = view.height + 2 * bleed;
  const { tiles, slot, hole } = useMemo(() => planLockWall(w, h), [w, h]);
  const { left, top, width, height } = snapTile(slot);
  const box = { left: left + EDGE_INSET, top: top + EDGE_INSET, width, height };

  const far = useMemo(() => Math.max(1, ...tiles.map((t) => Math.hypot(t.x + t.w / 2 - slot.x - slot.w / 2, t.y + t.h / 2 - slot.y - slot.h / 2))), [tiles, slot]);
  const colours = useMemo(() => tiles.map((_, i) => lockColour(i)), [tiles]);

  const [hovered, setHovered] = useState<{ i: number; of: typeof tiles } | null>(null);
  const rest = useMemo(() => mostColourful(tilesTouching(tiles, slot, bleed, view.width, view.height).map((i) => colours[i])), [tiles, slot, bleed, view.width, view.height, colours]);
  // A pointer index belongs to the layout it was read from; a resize re-plans the tiles and the pointer must move again.
  const tint = hovered && hovered.of === tiles ? colours[hovered.i] : rest;

  useEffect(() => {
    if (asking) input.current?.focus();
  }, [attempt, asking]);

  useEffect(() => {
    if (!need) return;
    const t = setTimeout(() => setNeed(false), NUDGE_MS);
    return () => clearTimeout(t);
  }, [need, nudges]);

  const nudge = () => {
    input.current?.focus();
    setNudges((n) => n + 1);
    setNeed(true);
  };

  useEffect(() => {
    if (!asking) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey || e.key === "Tab" || e.key === "Escape" || MODIFIERS.has(e.key)) return;
      const a = document.activeElement;
      if (a && a !== document.body) return;
      if (e.key.length === 1) input.current?.focus();
      else nudge();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [asking]);

  const track = (e: PointerEvent<HTMLDivElement>) => {
    if (e.pointerType !== "mouse" || (e.target as HTMLElement).closest(".uslot") || !wall.current) return;
    const r = wall.current.getBoundingClientRect();
    const i = nearestTile(tiles, e.clientX - r.left, e.clientY - r.top);
    setHovered(i >= 0 ? { i, of: tiles } : null);
  };

  return (
    <div
      className="screen"
      data-phase={phase}
      onPointerMove={track}
      onPointerLeave={() => setHovered(null)}
      onClick={(e) => {
        if (asking && !(e.target as HTMLElement).closest(".uslot")) nudge();
      }}
    >
      <div className="uwall" ref={wall} aria-hidden="true" style={{ left: -bleed, top: -bleed, width: w, height: h }}>
        {tiles.map((t, i) => {
          const p = peelOf(t, slot, far);
          const style = {
            ...snapTile(t),
            "--th": colours[i].h,
            "--ts": `${colours[i].s}%`,
            "--px": `${p.dx.toFixed(1)}px`,
            "--py": `${p.dy.toFixed(1)}px`,
            "--dl": `${p.delay.toFixed(3)}s`,
          } as CSSProperties;
          return <span key={i} className="utile" style={style} data-under={underForm(t, hole)} />;
        })}
      </div>
      <form
        className="uslot"
        style={{ ...box, ...(tint ? ({ "--th": tint.h, "--ts": `${tint.s}%` } as CSSProperties) : {}) }}
        onSubmit={(e) => {
          e.preventDefault();
          const t = token.trim();
          if (!t || !asking) return;
          setToken("");
          setNeed(false);
          onSubmit(t);
        }}
      >
        <div className="unlock" data-nudge={nudges === 0 || calm() ? undefined : nudges % 2}>
          <h1 className="unlock-words">
            This marquee is <em>locked</em>
          </h1>
          <div className="unlock-field" data-bad={rejected && editedFor !== attempt && asking}>
            <label className="sr" htmlFor={fieldId}>
              API token
            </label>
            <input
              ref={input}
              id={fieldId}
              type="password"
              placeholder="API token"
              autoComplete="off"
              autoCapitalize="off"
              spellCheck={false}
              autoFocus
              readOnly={!asking}
              value={token}
              onChange={(e) => {
                setToken(e.target.value);
                setEditedFor(attempt);
                setNeed(false);
              }}
            />
            <button type="submit" className="unlock-go" aria-disabled={empty || !asking}>
              {BUTTON[phase]}
            </button>
          </div>
          <div className="unlock-note">
            {rejected && asking && !need ? (
              <p className="unlock-error" role="alert" key={attempt}>
                That token was not accepted. Check it and try again.
              </p>
            ) : (
              <p className="unlock-hint" role="status" data-need={need}>
                {need ? "Unlock to continue." : "Enter its API token. Once per device."}
              </p>
            )}
          </div>
        </div>
      </form>
    </div>
  );
}
