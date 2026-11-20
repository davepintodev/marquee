import { useEffect, useId, useRef, useState } from "react";
import type { Api } from "./api/client";
import type { TitleSummary } from "./api/types";
import { AddMenu, addChoices, type AddChoice } from "./AddMenu";
import { Art } from "./Art";
import { BadgePill } from "./BadgePill";
import { InteractiveSearch } from "./InteractiveSearch";
import { hrefOf, pushTo } from "./route";

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

  const isSeries = title.kind === "series";

  const press = async (choice: AddChoice = "all") => {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    setError(null);
    try {
      if (title.inLibrary) {
        onChanged(await api.searchNow(title.id));
        onToast(`Searching for “${title.title}” now.`);
      } else {
        // The menu's button unmounts when it closes; focus has to be somewhere that survives the add.
        primary.current?.focus();
        const added = await api.addTitle(title.kind, title.tmdbId, isSeries ? (choice === "select" ? "none" : choice) : undefined);
        onChanged(added);
        if (isSeries) {
          const said = addChoices.find((c) => c.value === choice)?.said;
          onToast(choice === "select" ? `Added “${title.title}”, nothing monitored yet. Tick what you want here.` : `Added “${title.title}”, ${said}.`);
          if (choice === "select") {
            onDetails();
            pushTo({ name: "title", id: added.id }, "page");
          }
        } else {
          onToast(`Added “${title.title}”. marquee will keep looking until it has it.`);
        }
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
              <div className="add-split">
                <button ref={primary} type="button" className="btn-primary" aria-disabled={busy} onClick={() => press()}>
                  {title.inLibrary ? "Search now" : "Add & monitor"}
                </button>
                {isSeries && !title.inLibrary && <AddMenu busy={busy} onChoose={press} />}
              </div>
              {title.inLibrary && (
                <a
                  className="btn-secondary"
                  href={hrefOf({ name: "title", id: title.id })}
                  onClick={(e) => {
                    e.preventDefault();
                    onDetails();
                    pushTo({ name: "title", id: title.id }, "page");
                  }}
                >
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
