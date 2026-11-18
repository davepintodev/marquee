import createClient from "openapi-fetch";
import type { paths } from "./schema";
import type { Download, HistoryEntry, LiveEvent, ProgressEvent, Title, TitleEvent, TitleSummary, Wall } from "./types";

export class ApiError extends Error {
  readonly status?: number;
  constructor(message: string, status?: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export interface Api {
  wall(signal?: AbortSignal): Promise<Wall["items"]>;
  search(q: string, signal?: AbortSignal): Promise<TitleSummary[]>;
  // Adding a title that is already in the library changes nothing and still answers with it.
  addTitle(kind: TitleSummary["kind"], tmdbId: number, signal?: AbortSignal): Promise<Title>;
  searchNow(id: string, signal?: AbortSignal): Promise<Title>;
  title(id: string, signal?: AbortSignal): Promise<Title>;
  library(signal?: AbortSignal): Promise<TitleSummary[]>;
  setMonitored(id: string, monitored: boolean, signal?: AbortSignal): Promise<Title>;
  // Files on disk are never deleted.
  removeTitle(id: string, signal?: AbortSignal): Promise<void>;
  setEpisodeMonitored(id: string, episodeId: string, monitored: boolean, signal?: AbortSignal): Promise<Title>;
  searchEpisodeNow(id: string, episodeId: string, signal?: AbortSignal): Promise<Title>;
  downloads(signal?: AbortSignal): Promise<Download[]>;
  history(signal?: AbortSignal): Promise<HistoryEntry[]>;
  retry(historyId: string, signal?: AbortSignal): Promise<Title>;
  // Returns the function that closes the stream. Without EventSource (a test, an old browser) nothing is delivered.
  events(on: (e: LiveEvent) => void): () => void;
  imageUrl(path: string): string;
}

export interface ApiOptions {
  // Empty means same origin, which is how the embedded UI runs.
  baseUrl?: string;
  fetch?: typeof fetch;
  eventSource?: (url: string) => EventSource;
}

function isAbort(e: unknown) {
  return e instanceof DOMException && e.name === "AbortError";
}

async function call<T>(run: () => Promise<{ data?: T; error?: unknown; response: Response }>, valid: (d: T) => boolean): Promise<T> {
  let res;
  try {
    res = await run();
  } catch (e) {
    if (isAbort(e)) throw e;
    throw new ApiError("Can't reach marquee. Check that it is running.");
  }
  if (res.error !== undefined || res.data === undefined) {
    const body = res.error as { error?: unknown } | string | undefined;
    const msg = typeof body === "object" && body && typeof body.error === "string" ? body.error : "";
    throw new ApiError(msg || `marquee answered with an error (${res.response.status}).`, res.response.status);
  }
  if (!valid(res.data)) throw new ApiError("marquee sent an answer this page does not understand.", res.response.status);
  return res.data;
}

async function callVoid(run: () => Promise<{ error?: unknown; response: Response }>): Promise<void> {
  let res;
  try {
    res = await run();
  } catch (e) {
    if (isAbort(e)) throw e;
    throw new ApiError("Can't reach marquee. Check that it is running.");
  }
  if (res.error !== undefined || !res.response.ok) {
    const body = res.error as { error?: unknown } | string | undefined;
    const msg = typeof body === "object" && body && typeof body.error === "string" ? body.error : "";
    throw new ApiError(msg || `marquee answered with an error (${res.response.status}).`, res.response.status);
  }
}

function parse<T>(e: Event): T | null {
  try {
    return JSON.parse((e as MessageEvent<string>).data) as T;
  } catch {
    return null;
  }
}

const isTitle = (d: Title) => typeof d.id === "string";

export function createApi({ baseUrl = "", fetch: f, eventSource }: ApiOptions): Api {
  const base = baseUrl.replace(/\/+$/, "");
  const client = createClient<paths>({ baseUrl: base, ...(f ? { fetch: f } : {}) });
  return {
    async wall(signal) {
      const data = await call(() => client.GET("/api/wall", { signal }), (d) => Array.isArray(d.items));
      return data.items;
    },
    async search(q, signal) {
      const data = await call(() => client.GET("/api/search", { params: { query: { q } }, signal }), (d) => Array.isArray(d.results));
      return data.results;
    },
    addTitle(kind, tmdbId, signal) {
      return call(() => client.POST("/api/titles", { body: { kind, tmdbId }, signal }), (d) => typeof d.id === "string");
    },
    searchNow(id, signal) {
      return call(() => client.POST("/api/titles/{id}/search", { params: { path: { id } }, signal }), (d) => typeof d.id === "string");
    },
    title(id, signal) {
      return call(() => client.GET("/api/titles/{id}", { params: { path: { id } }, signal }), isTitle);
    },
    async library(signal) {
      const data = await call(() => client.GET("/api/titles", { signal }), (d) => Array.isArray(d.titles));
      return data.titles;
    },
    setMonitored(id, monitored, signal) {
      return call(() => client.PATCH("/api/titles/{id}", { params: { path: { id } }, body: { monitored }, signal }), isTitle);
    },
    removeTitle(id, signal) {
      return callVoid(() => client.DELETE("/api/titles/{id}", { params: { path: { id } }, signal }));
    },
    setEpisodeMonitored(id, episodeId, monitored, signal) {
      return call(() => client.PATCH("/api/titles/{id}/episodes/{episodeId}", { params: { path: { id, episodeId } }, body: { monitored }, signal }), isTitle);
    },
    searchEpisodeNow(id, episodeId, signal) {
      return call(() => client.POST("/api/titles/{id}/episodes/{episodeId}/search", { params: { path: { id, episodeId } }, signal }), isTitle);
    },
    async downloads(signal) {
      const data = await call(() => client.GET("/api/downloads", { signal }), (d) => Array.isArray(d.downloads));
      return data.downloads;
    },
    async history(signal) {
      const data = await call(() => client.GET("/api/history", { signal }), (d) => Array.isArray(d.history));
      return data.history;
    },
    retry(historyId, signal) {
      return call(() => client.POST("/api/history/{historyId}/retry", { params: { path: { historyId } }, signal }), isTitle);
    },
    events(on) {
      const make = eventSource ?? (typeof EventSource === "undefined" ? undefined : (url: string) => new EventSource(url));
      if (!make) return () => {};
      const es = make(base + "/api/events");
      let opened = false;
      es.addEventListener("open", () => {
        if (opened) on({ type: "reconnected" });
        opened = true;
      });
      es.addEventListener("progress", (e) => {
        const data = parse<ProgressEvent>(e);
        if (data) on({ type: "progress", data });
      });
      es.addEventListener("title", (e) => {
        const data = parse<TitleEvent>(e);
        if (data) on({ type: "title", data });
      });
      return () => es.close();
    },
    imageUrl: (path) => base + path,
  };
}
