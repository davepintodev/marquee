import { expect, test, vi } from "vitest";
import { ApiError, createApi } from "./client";

const ok = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

test("requests and image URLs go under the configured base path", async () => {
  const urls: string[] = [];
  const fetchStub = vi.fn(async (r: Request) => {
    urls.push(r.url);
    return ok({ items: [] });
  });
  const api = createApi({ baseUrl: "http://host.test/marquee/", fetch: fetchStub as unknown as typeof fetch });

  await api.wall();

  expect(urls).toEqual(["http://host.test/marquee/api/wall"]);
  expect(api.imageUrl("/api/images/poster/movie-1")).toBe("http://host.test/marquee/api/images/poster/movie-1");
});

test("same-origin by default: image URLs stay relative", () => {
  expect(createApi({}).imageUrl("/api/images/poster/movie-1")).toBe("/api/images/poster/movie-1");
});

test("search sends the query URL-encoded", async () => {
  const urls: string[] = [];
  const fetchStub = vi.fn(async (r: Request) => {
    urls.push(r.url);
    return ok({ results: [] });
  });
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await api.search("dune & más");

  expect(new URL(urls[0]).searchParams.get("q")).toBe("dune & más");
});

test("an error body's message becomes the ApiError message, whatever the status", async () => {
  const fetchStub = vi.fn(async () => new Response(JSON.stringify({ error: "TMDB did not answer" }), { status: 502, headers: { "Content-Type": "application/json" } }));
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.search("x")).rejects.toThrow(new ApiError("TMDB did not answer", 502));
});

test("a 200 with a body that is not the contract's shape is an ApiError, not a crash later", async () => {
  const fetchStub = vi.fn(async () => ok({ wrong: true }));
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.wall()).rejects.toBeInstanceOf(ApiError);
});

test("an aborted request rethrows the abort so callers can ignore it", async () => {
  const ctl = new AbortController();
  const fetchStub = vi.fn(async (r: Request) => {
    await new Promise((_, rej) => {
      // Like fetch, which rejects at once for a signal that is already aborted.
      if (r.signal.aborted) rej(r.signal.reason);
      r.signal.addEventListener("abort", () => rej(r.signal.reason));
    });
    return ok({});
  });
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  const p = api.wall(ctl.signal);
  ctl.abort();

  await expect(p).rejects.toMatchObject({ name: "AbortError" });
});

test("every request carries the token the caller holds, and none when there is none", async () => {
  const seen: (string | null)[] = [];
  const fetchStub = vi.fn(async (r: Request) => {
    seen.push(r.headers.get("Authorization"));
    return ok({ items: [] });
  });
  let token: string | null = null;
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch, getToken: () => token });

  await api.wall();
  token = "t0ken";
  await api.wall();

  expect(seen).toEqual([null, "Bearer t0ken"]);
});

test("a 401 tells the caller the token is needed and still fails the call", async () => {
  const fetchStub = vi.fn(async () => new Response(JSON.stringify({ error: "this marquee needs its API token" }), { status: 401, headers: { "Content-Type": "application/json" } }));
  const onUnauthorized = vi.fn();
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch, onUnauthorized });

  await expect(api.wall()).rejects.toMatchObject({ status: 401 });
  expect(onUnauthorized).toHaveBeenCalledTimes(1);
});

test("health reads the contract's shape", async () => {
  const health = { status: "ok", dryRun: false, checks: [], libraryProblems: [] };
  const fetchStub = vi.fn(async (r: Request) => (new URL(r.url).pathname === "/api/health" ? ok(health) : ok({})));
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.health()).resolves.toEqual(health);
});
