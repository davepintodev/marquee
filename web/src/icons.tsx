import type { ButtonHTMLAttributes, ReactNode } from "react";

const base = { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 2, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true } as const;

export const IconSearch = () => (
  <svg {...base}>
    <circle cx="11" cy="11" r="6.5" />
    <path d="M20 20l-4.2-4.2" />
  </svg>
);
export const IconRetry = () => (
  <svg {...base}>
    <path d="M20 12a8 8 0 1 1-2.6-5.9" />
    <path d="M20 4v5h-5" />
  </svg>
);
export const IconTrash = () => (
  <svg {...base}>
    <path d="M5 7h14M10 7V5h4v2M7 7l1 12h8l1-12M10.5 11v5M13.5 11v5" />
  </svg>
);
export const IconBookmark = ({ on }: { on: boolean }) => (
  <svg {...base} fill={on ? "currentColor" : "none"}>
    <path d="M7 4h10a1 1 0 0 1 1 1v15l-6-4-6 4V5a1 1 0 0 1 1-1z" />
  </svg>
);
export const IconBack = () => (
  <svg {...base}>
    <path d="M15 5l-7 7 7 7" />
  </svg>
);
export const IconCheck = () => (
  <svg {...base} strokeWidth={2.4}>
    <path d="M5 12.5l4.5 4.5L19 7.5" />
  </svg>
);
export const IconSpinner = () => (
  <svg {...base} className="spin">
    <path d="M12 3a9 9 0 1 0 9 9" />
  </svg>
);

interface IbProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children" | "title"> {
  label: string;
  name?: string;
  children: ReactNode;
  pressed?: boolean;
  quiet?: boolean;
}


export function IconButton({ label, name, children, pressed, quiet, className = "", ...rest }: IbProps) {
  return (
    <button type="button" className={`ib${quiet ? " quiet" : ""} ${className}`} aria-label={name ?? label} title={label} aria-pressed={pressed} {...rest}>
      {children}
    </button>
  );
}
