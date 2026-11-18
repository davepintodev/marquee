import { useEffect, useId, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { Art } from "./Art";
import { BadgePill } from "./BadgePill";
import { InteractiveSearch } from "./InteractiveSearch";
import { hrefOf } from "./route";

interface Props {
  api: Api;
  title: TitleSummary;
  onClose: () => void;
  onChanged: (title: TitleSummary) => void;
  onToast: (text: string) => void;
  onDetails: () => void;
}

export function Popup({ api, title, onClose, onChanged, onToast, onDetails }: Props) {
  const headingId = useId();
  const primary = useRef<HTMLButtonElement>(null);
  const working = useRef(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);
  const interactive = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    const opener = document.activeElement;
    primary.current?.focus();
    return () => {
      if (opener instanceof HTMLElement && opener.isConnected) opener.focus();
    };
  }, []);

  const press = async () => {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    setError(null);
    try {
      if (title.inLibrary) {
        onChanged(await api.searchNow(title.id));
        onToast(`Searching for “${title.title}” now.`);
      } else {
        onChanged(await api.addTitle(title.kind, title.tmdbId));
        onToast(`Added “${title.title}”. marquee will keep looking until it has it.`);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Something went wrong.");
    } finally {
      working.current = false;
      setBusy(false);
    }
  };

  return (
    <div className="popup-layer">
      <div className="popup-backdrop" data-testid="popup-backdrop" onClick={onClose} />
      <div className="popup" role="dialog" aria-modal="true" aria-labelledby={headingId}>
        <button type="button" className="popup-close" aria-label="Close" onClick={onClose}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M6 6l12 12M18 6L6 18" />
          </svg>
        </button>
        <span className="popup-poster">
          <Art src={api.imageUrl(title.posterPath)} title={title.title} year={title.year} />
          <BadgePill badge={title.inLibrary ? title.badge : undefined} />
        </span>
        <div className="popup-head">
          <h2 id={headingId}>{title.title}</h2>
          <p className="popup-meta">{`${title.year} · ${title.kind}`}</p>
        </div>
        {searching ? (
          <InteractiveSearch
            api={api}
            title={title}
            onChanged={onChanged}
            onToast={onToast}
            onBack={() => {
              setSearching(false);
              setTimeout(() => interactive.current?.focus());
            }}
          />
        ) : (
          <>
            {title.overview && <p className="popup-overview">{title.overview}</p>}
            <div className="popup-actions">
              {error && (
                <p className="popup-error" role="alert">
                  {error}
                </p>
              )}
              <button ref={primary} type="button" className="btn-primary" aria-disabled={busy} onClick={press}>
                {title.inLibrary ? "Search now" : "Add & monitor"}
              </button>
              {title.inLibrary && (
                <a className="btn-secondary" href={hrefOf({ name: "title", id: title.id })} onClick={onDetails}>
                  Details
                </a>
              )}
              <button ref={interactive} type="button" className="btn-secondary" onClick={() => setSearching(true)}>
                Interactive search
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
