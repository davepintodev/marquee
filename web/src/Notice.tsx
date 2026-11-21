import type { ReactNode } from "react";
import "./notice.css";

export interface NoticeAction {
  label: string;
  onClick: () => void;
}

export type Tone = "error" | "warn" | "info" | "quiet";

interface Props {
  tone: Tone;
  action?: NoticeAction;
  children: ReactNode;
}

export function Notice({ tone, action, children }: Props) {
  return (
    <div className="notice" data-tone={tone} role={tone === "error" ? "alert" : undefined}>
      <i aria-hidden="true" />
      <p>{children}</p>
      {action && (
        <button type="button" className="link-btn notice-act" onClick={action.onClick}>
          {action.label}
        </button>
      )}
    </div>
  );
}
