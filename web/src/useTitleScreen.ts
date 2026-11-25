import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, type Api } from "./api/client";
import type { LiveEvent, Title, TitleSummary } from "./api/types";
import { useLive } from "./useLive";

export type Load = { status: "loading" } | { status: "ok"; title: Title } | { status: "error"; message: string };

export const messageOf = (e: unknown) => (e instanceof Error ? e.message : "Something went wrong.");
const isAbort = (e: unknown) => e instanceof DOMException && e.name === "AbortError";
const unadded = (s: TitleSummary): Title => ({ ...s, seasons: [], historySummary: "", history: [] });

function withProgress(t: Title, episode: string | undefined, percent: number): Title {
  if (!episode) return { ...t, badge: t.badge && { ...t.badge, label: `Downloading ${percent}%`, percent } };
  return { ...t, seasons: t.seasons.map((s) => ({ ...s, episodes: s.episodes.map((e) => (e.id === episode ? { ...e, state: "downloading", percent } : e)) })) };
}

interface Opts {
  api: Api;
  id: string;
  seed?: TitleSummary;
  onChanged: (title: TitleSummary) => void;
  onRemoved: (title: Title, deletedFiles: boolean) => void;
  onToast: (text: string) => void;
}

export function useTitleScreen({ api, id, seed, onChanged, onRemoved, onToast }: Opts) {
  const [load, setLoad] = useState<Load>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const working = useRef(false);
  // Read only when the title answers 404; as a dependency it would refetch on every live event, since the caller rebuilds it.
  const seeded = useRef(seed);
  useEffect(() => {
    seeded.current = seed;
  });

  useEffect(() => {
    const ctl = new AbortController();
    api.title(id, ctl.signal).then(
      (title) => !ctl.signal.aborted && setLoad({ status: "ok", title }),
      async (e) => {
        if (ctl.signal.aborted || isAbort(e)) return;
        if (e instanceof ApiError && e.status === 404) {
          let s = seeded.current;
          if (!s) s = (await api.wall(ctl.signal).catch(() => [])).map((i) => i.title).find((t) => t.id === id);
          if (ctl.signal.aborted) return;
          if (s) return setLoad({ status: "ok", title: unadded(s) });
        }
        setLoad({ status: "error", message: messageOf(e) });
      },
    );
    return () => ctl.abort();
  }, [api, id, attempt]);

  const refetch = useCallback(() => {
    api.title(id).then((title) => setLoad({ status: "ok", title }), () => {});
  }, [api, id]);

  useLive(api, (e: LiveEvent) => {
    if (e.type === "reconnected") return refetch();
    if (e.data.titleId !== id) return;
    if (e.type === "title") return refetch();
    setLoad((l) => (l.status === "ok" ? { status: "ok", title: withProgress(l.title, e.data.episode, e.data.percent) } : l));
  });

  // Resolves to whether the action went through; a refusal is left in `error`.
  const run = async (key: string, act: () => Promise<Title>, toast?: string | ((title: Title) => string)): Promise<boolean> => {
    if (working.current) return false;
    working.current = true;
    setBusy(key);
    setError(null);
    try {
      const title = await act();
      setLoad({ status: "ok", title });
      onChanged(title);
      if (toast) onToast(typeof toast === "function" ? toast(title) : toast);
      return true;
    } catch (e) {
      setError(messageOf(e));
      return false;
    } finally {
      working.current = false;
      setBusy(null);
    }
  };

  const remove = async (title: Title, deleteFiles: boolean) => {
    if (working.current) return false;
    working.current = true;
    setBusy("remove");
    setError(null);
    try {
      await api.removeTitle(title.id, deleteFiles);
      onRemoved(title, deleteFiles);
      return true;
    } catch (e) {
      setError(messageOf(e));
      return false;
    } finally {
      working.current = false;
      setBusy(null);
    }
  };

  const reload = () => {
    setLoad({ status: "loading" });
    setAttempt((n) => n + 1);
  };

  return { load, setLoad, busy, error, run, remove, reload };
}
