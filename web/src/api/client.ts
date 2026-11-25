import createClient from "openapi-fetch";
import type { paths } from "./schema";
import type { Download, Health, HistoryEntry, LiveEvent, MonitorChoice, ProgressEvent, Profiles, Releases, Title, TitleAbout, TitleEvent, TitleSummary, Wall } from "./types";

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
  // A missing profile means the default of the title's kind.
  addTitle(kind: TitleSummary["kind"], tmdbId: number, choice?: { monitor?: MonitorChoice; profile?: string }, signal?: AbortSignal): Promise<Title>;
  profiles(signal?: AbortSignal): Promise<Profiles>;
  // Null puts the title back on the default of its kind.
  setProfile(id: string, profile: string | null, signal?: AbortSignal): Promise<Title>;
  searchNow(id: string, signal?: AbortSignal): Promise<Title>;
  title(id: string, signal?: AbortSignal): Promise<Title>;
  // Scores, cast and similar titles: extras, so a caller treats any failure as "nothing to show".
  about(id: string, signal?: AbortSignal): Promise<TitleAbout>;
  library(signal?: AbortSignal): Promise<TitleSummary[]>;
  setMonitored(id: string, monitored: boolean, signal?: AbortSignal): Promise<Title>;
  removeTitle(id: string, signal?: AbortSignal): Promise<void>;
  setEpisodeMonitored(id: string, episodeId: string, monitored: boolean, signal?: AbortSignal): Promise<Title>;
  setSeasonMonitored(id: string, season: number, monitored: boolean, signal?: AbortSignal): Promise<Title>;
  searchEpisodeNow(id: string, episodeId: string, signal?: AbortSignal): Promise<Title>;
  // A series needs the episode; a movie takes none.
  releases(id: string, episode?: string, signal?: AbortSignal): Promise<Releases>;
  // Picking by hand overrides the blocklist. A title that is not in the library is added first.
  grab(id: string, releaseId: string, episode?: string, signal?: AbortSignal): Promise<Download>;
  downloads(signal?: AbortSignal): Promise<Download[]>;
  // One page of the feed, newest first. `next` is the cursor to pass for the page after it, absent on the last.
  history(page: { limit?: number; cursor?: string }, signal?: AbortSignal): Promise<{ entries: HistoryEntry[]; next?: string }>;
  retry(historyId: string, signal?: AbortSignal): Promise<Title>;
  // Returns the function that closes the stream. Without EventSource (a test, an old browser) nothing is delivered.
  events(on: (e: LiveEvent) => void): () => void;
  health(signal?: AbortSignal): Promise<Health>;
  imageUrl(path: string): string;
}

export interface ApiOptions {
  // Empty means same origin, which is how the embedded UI runs.
  baseUrl?: string;
  fetch?: typeof fetch;
  eventSource?: (url: string) => EventSource;
  getToken?: () => string | null;
  onUnauthorized?: (sentToken: string | null) => void;
  // A request that carried a token was accepted.
  onAuthorized?: () => void;
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

export function createApi({ baseUrl = "", fetch: f, eventSource, getToken, onUnauthorized, onAuthorized }: ApiOptions): Api {
  const base = baseUrl.replace(/\/+$/, "");
  const client = createClient<paths>({ baseUrl: base, ...(f ? { fetch: f } : {}) });

  // Every screen and the wall listen to the one stream, which lives while any of them does.
  const make = eventSource ?? (typeof EventSource === "undefined" ? undefined : (url: string) => new EventSource(url));
  const listeners = new Set<(e: LiveEvent) => void>();
  let shared: EventSource | undefined;
  // With a token the stream waits until a request carrying it has been answered: that answer sets the
  // session cookie, and an EventSource refused with a 401 never retries.
  let unlocked = false;
  const mayOpen = () => unlocked || !getToken?.();
  const tell = (e: LiveEvent) => [...listeners].forEach((fn) => fn(e));
  function open(): EventSource {
    const es = make!(base + "/api/events");
    let opened = false;
    es.addEventListener("open", () => {
      if (opened) tell({ type: "reconnected" });
      opened = true;
    });
    es.addEventListener("progress", (e) => {
      const data = parse<ProgressEvent>(e);
      if (data) tell({ type: "progress", data });
    });
    es.addEventListener("title", (e) => {
      const data = parse<TitleEvent>(e);
      if (data) tell({ type: "title", data });
    });
    return es;
  }
  client.use({
    onRequest({ request }) {
      const token = getToken?.();
      if (token) request.headers.set("Authorization", `Bearer ${token}`);
      return request;
    },
    onResponse({ request, response }) {
      const sent = request.headers.get("Authorization")?.replace(/^Bearer /, "") ?? null;
      if (response.ok && sent !== null && sent === (getToken?.() ?? null)) {
        unlocked = true;
        onAuthorized?.();
        if (listeners.size > 0) shared ??= open();
      }
      if (response.status === 401) {
        // An answer to a token no longer held says nothing about the session of the new one.
        if (sent === (getToken?.() ?? null)) {
          unlocked = false;
          shared?.close();
          shared = undefined;
        }
        onUnauthorized?.(sent);
      }
    },
  });
  return {
    async wall(signal) {
      const data = await call(() => client.GET("/api/wall", { signal }), (d) => Array.isArray(d.items));
      return data.items;
    },
    async search(q, signal) {
      const data = await call(() => client.GET("/api/search", { params: { query: { q } }, signal }), (d) => Array.isArray(d.results));
      return data.results;
    },
    addTitle(kind, tmdbId, { monitor, profile } = {}, signal) {
      return call(() => client.POST("/api/titles", { body: { kind, tmdbId, monitor, profile }, signal }), (d) => typeof d.id === "string");
    },
    profiles(signal) {
      return call(
        () => client.GET("/api/profiles", { signal }),
        (d) => Array.isArray(d.profiles) && typeof d.defaultMovieProfile === "string" && typeof d.defaultSeriesProfile === "string",
      );
    },
    setProfile(id, profile, signal) {
      return call(() => client.PATCH("/api/titles/{id}", { params: { path: { id } }, body: { profile }, signal }), isTitle);
    },
    searchNow(id, signal) {
      return call(() => client.POST("/api/titles/{id}/search", { params: { path: { id } }, signal }), (d) => typeof d.id === "string");
    },
    title(id, signal) {
      return call(() => client.GET("/api/titles/{id}", { params: { path: { id } }, signal }), isTitle);
    },
    about(id, signal) {
      return call(() => client.GET("/api/titles/{id}/about", { params: { path: { id } }, signal }), (d) => Array.isArray(d.cast) && Array.isArray(d.ratings));
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
    setSeasonMonitored(id, season, monitored, signal) {
      return call(() => client.PUT("/api/titles/{id}/seasons/{number}", { params: { path: { id, number: season } }, body: { monitored }, signal }), isTitle);
    },
    searchEpisodeNow(id, episodeId, signal) {
      return call(() => client.POST("/api/titles/{id}/episodes/{episodeId}/search", { params: { path: { id, episodeId } }, signal }), isTitle);
    },
    releases(id, episode, signal) {
      return call(
        () => client.GET("/api/titles/{id}/releases", { params: { path: { id }, query: episode ? { episode } : {} }, signal }),
        (d) => Array.isArray(d.candidates) && Array.isArray(d.dropped),
      );
    },
    grab(id, releaseId, episode, signal) {
      return call(
        () => client.POST("/api/titles/{id}/releases/{releaseId}/grab", { params: { path: { id, releaseId } }, ...(episode ? { body: { episode } } : {}), signal }),
        (d) => typeof d.id === "string",
      );
    },
    async downloads(signal) {
      const data = await call(() => client.GET("/api/downloads", { signal }), (d) => Array.isArray(d.downloads));
      return data.downloads;
    },
    async history({ limit, cursor }, signal) {
      const query = { ...(limit ? { limit } : {}), ...(cursor ? { cursor } : {}) };
      const data = await call(() => client.GET("/api/history", { params: { query }, signal }), (d) => Array.isArray(d.history));
      return { entries: data.history, next: data.nextCursor };
    },
    retry(historyId, signal) {
      return call(() => client.POST("/api/history/{historyId}/retry", { params: { path: { historyId } }, signal }), isTitle);
    },
    events(on) {
      if (!make) return () => {};
      listeners.add(on);
      if (mayOpen()) shared ??= open();
      return () => {
        if (!listeners.delete(on) || listeners.size > 0) return;
        shared?.close();
        shared = undefined;
      };
    },
    health(signal) {
      return call(() => client.GET("/api/health", { signal }), (d) => Array.isArray(d.checks) && Array.isArray(d.libraryProblems) && typeof d.libraryTotals?.movies === "number" && typeof d.libraryTotals?.episodes === "number");
    },
    imageUrl: (path) => base + path,
  };
}
