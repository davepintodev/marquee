import { useEffect, useId, useRef, type ReactNode } from "react";

interface Props {
  heading: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
  // A full-bleed board on the plain background instead of the glass sheet.
  board?: boolean;
}

export function Page({ heading, onClose, children, wide, board }: Props) {
  const id = useId();
  const close = useRef<HTMLButtonElement>(null);
  useEffect(() => close.current?.focus(), []);
  return (
    <div className={board ? "page-layer page-layer-board" : "page-layer"}>
      <main className={board ? "page page-board" : wide ? "page page-wide" : "page"} aria-labelledby={id}>
        <button ref={close} type="button" className="popup-close" aria-label="Close" onClick={onClose}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
        <h1 id={id} className="page-title">
          {heading}
        </h1>
        {children}
      </main>
    </div>
  );
}
