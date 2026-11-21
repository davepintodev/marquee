import { useEffect, useRef, useState } from "react";

// Forgetting the token signs this device out, so it asks first. The question opens on "Keep it" and Esc answers it
// without closing the page behind it.
export function ForgetToken({ onForget }: { onForget: () => void }) {
  const [asking, setAsking] = useState(false);
  const ask = useRef<HTMLButtonElement>(null);
  const keep = useRef<HTMLButtonElement>(null);
  const wasAsking = useRef(false);

  useEffect(() => {
    if (asking) keep.current?.focus();
    else if (wasAsking.current) ask.current?.focus();
    wasAsking.current = asking;
  }, [asking]);

  if (!asking) {
    return (
      <button ref={ask} type="button" className="forget" onClick={() => setAsking(true)}>
        Forget on this device
      </button>
    );
  }
  return (
    <span
      className="forget-ask"
      role="group"
      aria-label="Forget the saved token?"
      onKeyDown={(e) => {
        if (e.key !== "Escape") return;
        e.stopPropagation();
        setAsking(false);
      }}
    >
      <span className="forget-q">Forget it?</span>
      <button ref={keep} type="button" className="forget" onClick={() => setAsking(false)}>
        Keep it
      </button>
      <button type="button" className="forget forget-yes" data-danger="true" onClick={onForget}>
        Forget
      </button>
    </span>
  );
}
