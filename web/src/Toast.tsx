import { useEffect, useState } from "react";
import type { NoticeAction } from "./Notice";
import "./toast.css";

export interface ToastMsg {
  n: number;
  text: string;
  kind: "ok" | "info" | "warn" | "error";
  action?: NoticeAction;
}

export type ToastRaise = (text: string, more?: Pick<ToastMsg, "kind" | "action">) => void;

const OK_MS = 5000;
const LONG_MS = 8000;
const LEAVE_MS = 180;

function ToastItem({ toast: t, onDismiss }: { toast: ToastMsg; onDismiss: () => void }) {
  const [held, setHeld] = useState(false);
  const [leaving, setLeaving] = useState(false);

  useEffect(() => {
    if (held) return;
    const timer = setTimeout(() => setLeaving(true), t.kind === "error" || t.kind === "warn" ? LONG_MS : OK_MS);
    return () => clearTimeout(timer);
  }, [t.kind, held]);

  useEffect(() => {
    if (!leaving) return;
    const timer = setTimeout(onDismiss, LEAVE_MS);
    return () => clearTimeout(timer);
  }, [leaving, onDismiss]);

  return (
    <div
      className="toast"
      data-kind={t.kind}
      data-leaving={leaving || undefined}
      role={t.kind === "error" ? "alert" : "status"}
      onPointerEnter={() => setHeld(true)}
      onPointerLeave={() => setHeld(false)}
      onFocus={() => setHeld(true)}
      onBlur={() => setHeld(false)}
      onKeyDown={(e) => {
        if (e.key !== "Escape") return;
        e.stopPropagation();
        setLeaving(true);
      }}
    >
      <span className="toast-text">{t.text}</span>
      {t.action && (
        <button
          type="button"
          className="link-btn toast-act"
          onClick={() => {
            t.action?.onClick();
            setLeaving(true);
          }}
        >
          {t.action.label}
        </button>
      )}
      <button type="button" className="toast-x" aria-label="Dismiss" title="Dismiss" onClick={() => setLeaving(true)}>
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M7 7l10 10M17 7L7 17" />
        </svg>
      </button>
    </div>
  );
}

export function Toaster({ toast, onDismiss }: { toast: ToastMsg | null; onDismiss: () => void }) {
  if (!toast) return null;
  return (
    <div className="toast-dock">
      <ToastItem key={toast.n} toast={toast} onDismiss={onDismiss} />
    </div>
  );
}
