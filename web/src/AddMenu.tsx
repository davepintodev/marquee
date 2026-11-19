import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import type { MonitorChoice } from "./api/types";

export type AddChoice = MonitorChoice | "select";

export const addChoices: { value: AddChoice; label: string; said: string }[] = [
  { value: "all", label: "All episodes", said: "all episodes monitored" },
  { value: "future", label: "Future episodes", said: "future episodes monitored" },
  { value: "first_season", label: "First season", said: "first season monitored" },
  { value: "latest_season", label: "Latest season", said: "latest season monitored" },
  { value: "none", label: "None", said: "nothing monitored yet" },
  { value: "select", label: "Select seasons and episodes…", said: "nothing monitored yet" },
];

interface Props {
  busy: boolean;
  onChoose: (choice: AddChoice) => void;
}

export function AddMenu({ busy, onChoose }: Props) {
  const menuId = useId();
  const root = useRef<HTMLSpanElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const items = useRef<(HTMLButtonElement | null)[]>([]);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (open) items.current[0]?.focus();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const away = (e: PointerEvent) => {
      if (e.target instanceof Node && !root.current?.contains(e.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", away);
    return () => document.removeEventListener("pointerdown", away);
  }, [open]);

  const move = (to: number) => items.current[(to + addChoices.length) % addChoices.length]?.focus();

  const onToggleKey = (e: KeyboardEvent) => {
    if (e.key !== "ArrowDown" || busy) return;
    e.preventDefault();
    setOpen(true);
  };

  const onMenuKey = (e: KeyboardEvent) => {
    const at = items.current.indexOf(document.activeElement as HTMLButtonElement);
    switch (e.key) {
      case "ArrowDown":
        move(at + 1);
        break;
      case "ArrowUp":
        move(at - 1);
        break;
      case "Home":
        move(0);
        break;
      case "End":
        move(addChoices.length - 1);
        break;
      case "Escape":
        // The popup's own Esc handler listens on window and must not see this one.
        e.stopPropagation();
        setOpen(false);
        toggle.current?.focus();
        return;
      default:
        return;
    }
    e.preventDefault();
  };

  return (
    <span
      ref={root}
      className="add-menu-root"
      onBlur={(e) => {
        // A click on a button Safari does not focus blurs to null; only focus landing elsewhere closes.
        if (open && e.relatedTarget instanceof Node && !root.current?.contains(e.relatedTarget)) setOpen(false);
      }}
    >
      <button
        ref={toggle}
        type="button"
        className="btn-primary add-toggle"
        aria-label="Choose what to monitor"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        aria-disabled={busy}
        onClick={() => !busy && setOpen((o) => !o)}
        onKeyDown={onToggleKey}
      >
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M6 9l6 6 6-6" />
        </svg>
      </button>
      {open && (
        <div id={menuId} className="add-menu" role="menu" aria-label="Choose what to monitor" onKeyDown={onMenuKey}>
          {addChoices.map((c, i) => (
            <button
              key={c.value}
              ref={(el) => {
                items.current[i] = el;
              }}
              type="button"
              role="menuitem"
              tabIndex={-1}
              onClick={() => {
                setOpen(false);
                onChoose(c.value);
              }}
            >
              {c.label}
            </button>
          ))}
        </div>
      )}
    </span>
  );
}
