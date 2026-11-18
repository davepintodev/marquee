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

test("removing a title accepts the 204 with no body and turns an error body into its message", async () => {
  const answers = [new Response(null, { status: 204 }), new Response(JSON.stringify({ error: "that title is not in the library" }), { status: 404, headers: { "Content-Type": "application/json" } })];
  const fetchStub = vi.fn(async () => answers.shift()!);
  const api = createApi({ baseUrl: "http://host.test", fetch: fetchStub as unknown as typeof fetch });

  await expect(api.removeTitle("series-1")).resolves.toBeUndefined();
  await expect(api.removeTitle("series-1")).rejects.toThrow("that title is not in the library");
});

class Stream {
  closed = false;
  private fns = new Map<string, (e: Event) => void>();
  addEventListener(name: string, fn: (e: Event) => void) {
    this.fns.set(name, fn);
  }
  close() {
    this.closed = true;
  }
  send(name: string, data = "") {
    this.fns.get(name)?.(new MessageEvent(name, { data }));
  }
}

test("events delivers progress and title messages, skips garbage and says when the stream came back", () => {
  const stream = new Stream();
  const urls: string[] = [];
  const api = createApi({ baseUrl: "http://host.test/", eventSource: (u) => (urls.push(u), stream as unknown as EventSource) });
  const got: unknown[] = [];

  const stop = api.events((e) => got.push(e));
  stream.send("open");
  stream.send("progress", JSON.stringify({ downloadId: "d", percent: 5 }));
  stream.send("progress", "not json");
  stream.send("title", JSON.stringify({ titleId: "movie-1", badge: null }));
  stream.send("open");
  stop();

  expect(urls).toEqual(["http://host.test/api/events"]);
  expect(got).toEqual([
    { type: "progress", data: { downloadId: "d", percent: 5 } },
    { type: "title", data: { titleId: "movie-1", badge: null } },
    { type: "reconnected" },
  ]);
  expect(stream.closed).toBe(true);
});

test("every listener shares one stream, which closes with the last of them", () => {
  const streams: Stream[] = [];
  const api = createApi({ baseUrl: "http://host.test", eventSource: () => (streams.push(new Stream()), streams.at(-1) as unknown as EventSource) });
  const a: unknown[] = [];
  const b: unknown[] = [];

  const stopA = api.events((e) => a.push(e));
  const stopB = api.events((e) => b.push(e));
  expect(streams).toHaveLength(1);
  streams[0].send("title", JSON.stringify({ titleId: "movie-1", badge: null }));
  expect(a).toHaveLength(1);
  expect(b).toHaveLength(1);

  stopA();
  stopA();
  expect(streams[0].closed).toBe(false);
  streams[0].send("title", JSON.stringify({ titleId: "movie-2", badge: null }));
  expect(a).toHaveLength(1);
  expect(b).toHaveLength(2);

  stopB();
  expect(streams[0].closed).toBe(true);
  api.events(() => {})();
  expect(streams).toHaveLength(2);
});

test("events does nothing where there is no EventSource", () => {
  const api = createApi({});
  expect(() => api.events(() => {})()).not.toThrow();
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
