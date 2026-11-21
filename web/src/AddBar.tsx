import { useRef, useState } from "react";
import type { Api } from "./api/client";
import type { Title, TitleSummary } from "./api/types";
import { AddMenu, addChoices, type AddChoice } from "./AddMenu";
import { Notice } from "./Notice";

interface Props {
  api: Api;
  title: TitleSummary;
  onChanged: (title: TitleSummary) => void;
  onToast: (text: string) => void;
  onAdded: (title: Title) => void;
}

export function AddBar({ api, title, onChanged, onToast, onAdded }: Props) {
  const working = useRef(false);
  const primary = useRef<HTMLButtonElement>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isSeries = title.kind === "series";

  const press = async (choice: AddChoice = "all") => {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    setError(null);
    try {
      // The menu's button unmounts when it closes; focus has to be somewhere that survives the add.
      primary.current?.focus();
      const added = await api.addTitle(title.kind, title.tmdbId, isSeries ? (choice === "select" ? "none" : choice) : undefined);
      onChanged(added);
      onAdded(added);
      if (isSeries) {
        const said = addChoices.find((c) => c.value === choice)?.said;
        onToast(choice === "select" ? `Added “${title.title}”, nothing monitored yet. Tick what you want here.` : `Added “${title.title}”, ${said}.`);
      } else {
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
    <>
      {error && (
        <Notice tone="error" action={{ label: "Try again", onClick: () => press() }}>
          {error}
        </Notice>
      )}
      <div className="add-split">
        <button ref={primary} type="button" className="btn-primary" aria-disabled={busy} onClick={() => press()}>
          Add & monitor
        </button>
        {isSeries && <AddMenu busy={busy} onChoose={press} />}
      </div>
    </>
  );
}
