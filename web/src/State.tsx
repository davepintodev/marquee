import { useId, type ReactNode } from "react";
import type { NoticeAction } from "./Notice";
import "./state.css";

type Fit = "area" | "page" | "inline";

interface Props {
  fit?: Fit;
  action?: NoticeAction;
  children: ReactNode;
}

function Strip({ hollow }: { hollow?: boolean }) {
  const holes = Array.from({ length: 8 }, (_, i) => 17 + i * 18);
  return (
    <>
      <rect className="st-body" x="6" y="18" width="156" height="60" rx="8" />
      {holes.map((x) => (
        <g key={x}>
          <rect x={x} y="24" width="8" height="5" rx="1.5" />
          <rect x={x} y="67" width="8" height="5" rx="1.5" />
        </g>
      ))}
      {[14, 62, 110].map((x) => (
        <rect key={x} className={hollow ? "st-slot" : undefined} x={x} y="36" width="40" height="24" rx="4" />
      ))}
    </>
  );
}

const EDGE = "M58 18 L54 34 L62 46 L55 62 L59 78";

function Art({ torn }: { torn: boolean }) {
  // The library and the results are both mounted, so two torn strips can exist at once; each needs its own clip ids.
  const id = `st${useId().replace(/\W/g, "")}`;
  return (
    <svg className="state-art" viewBox="-10 0 188 100" aria-hidden="true" focusable="false">
      {torn ? (
        <>
          <defs>
            <clipPath id={`${id}l`}>
              <path d="M-10 0 H58 V18 L54 34 L62 46 L55 62 L59 78 V100 H-10 Z" />
            </clipPath>
            <clipPath id={`${id}r`}>
              <path d="M178 0 H58 V18 L54 34 L62 46 L55 62 L59 78 V100 H178 Z" />
            </clipPath>
          </defs>
          <g transform="translate(-9 -1) rotate(-3 30 48)">
            <g clipPath={`url(#${id}l)`}>
              <Strip />
            </g>
            <path className="st-edge" d={EDGE} />
          </g>
          <g transform="translate(9 4) rotate(3 110 48)">
            <g clipPath={`url(#${id}r)`}>
              <Strip />
            </g>
            <path className="st-edge" d={EDGE} />
          </g>
        </>
      ) : (
        <Strip hollow />
      )}
    </svg>
  );
}

function State({ kind, tone, fit = "area", action, children }: Props & { kind: "error" | "empty"; tone: "bad" | "mute" | "info" }) {
  return (
    <div className="state" data-kind={kind} data-tone={tone} data-fit={fit} role={kind === "error" ? "alert" : undefined}>
      <Art torn={kind === "error"} />
      <p className="state-msg">
        <i aria-hidden="true" />
        {children}
      </p>
      {action && (
        <button type="button" className="link-btn state-act" onClick={action.onClick}>
          {action.label}
        </button>
      )}
    </div>
  );
}

export function ErrorState(props: Props) {
  return <State {...props} kind="error" tone="bad" />;
}

export function EmptyState({ tone = "mute", ...props }: Props & { tone?: "mute" | "info" }) {
  return <State {...props} kind="empty" tone={tone} />;
}
