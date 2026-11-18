import createClient from "openapi-fetch";
import type { paths } from "./schema";
import type { Title, TitleSummary, Wall } from "./types";

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
  imageUrl(path: string): string;
}

export interface ApiOptions {
  // Empty means same origin, which is how the embedded UI runs.
  baseUrl?: string;
  fetch?: typeof fetch;
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

export function createApi({ baseUrl = "", fetch: f }: ApiOptions): Api {
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
    imageUrl: (path) => base + path,
  };
}
