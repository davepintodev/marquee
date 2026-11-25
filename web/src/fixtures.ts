import type { Download, Releases, Title, TitleAbout } from "./api/types";

export const house = (over: Partial<Title> = {}): Title => ({
  id: "series-2",
  tmdbId: 2,
  kind: "series",
  title: "House",
  year: 2004,
  overview: "A diagnostician.",
  posterPath: "/api/images/poster/series-2",
  backdropPath: "/api/images/backdrop/series-2",
  inLibrary: true,
  monitored: true,
  badge: { state: "partial_season", label: "Season 4 · 2 missing", season: 4, missingCount: 2 },
  profile: null,
  nextSearchAt: "2026-11-18T18:00:00Z",
  historySummary: "Tried 3 releases, 1 stalled, 2 fake, retrying in 6h.",
  seasons: [
    {
      number: 3,
      badge: { state: "downloaded", label: "Downloaded" },
      episodes: [{ id: "s03e01", number: 1, name: "Meaning", airDate: "2006-09-05", monitored: true, state: "downloaded" }],
    },
    {
      number: 4,
      badge: { state: "partial_season", label: "Season 4 · 2 missing", season: 4, missingCount: 2 },
      episodes: [
        { id: "s04e01", number: 1, name: "Alone", airDate: "2007-09-25", monitored: true, state: "downloaded" },
        { id: "s04e02", number: 2, name: "Big Baby", airDate: "2007-10-02", monitored: true, state: "missing", nextSearchAt: "2026-11-18T18:00:00Z" },
        { id: "s04e03", number: 3, name: "You Don't Want to Know", airDate: "2007-10-09", monitored: true, state: "downloading", percent: 40 },
        { id: "s04e04", number: 4, name: "Sex Kills", airDate: "2007-10-16", monitored: false, state: "stalled" },
        { id: "s04e05", number: 5, name: "Next Week", airDate: "2026-12-01", monitored: true, state: "unaired" },
      ],
    },
  ],
  history: [
    { id: "h2", at: "2026-11-18T06:10:00Z", kind: "failed", titleId: "series-2", titleName: "House", episode: "s04e02", message: "House.S04E02.720p.HDTV stalled: no data for 1 minute.", reason: "stalled", retryable: true },
    { id: "h3", at: "2026-11-18T05:00:00Z", kind: "failed", titleId: "series-2", titleName: "House", episode: "s04e03", message: "House.S04E03.1080p was not a video.", reason: "not_video", retryable: false },
    { id: "h1", at: "2026-11-17T21:05:00Z", kind: "imported", titleId: "series-2", titleName: "House", episode: "s04e01", message: "Imported House - S04E01 - Alone.", retryable: false },
  ],
  ...over,
});

export const dune = (over: Partial<Title> = {}): Title => ({
  id: "movie-1",
  tmdbId: 1,
  kind: "movie",
  title: "Dune",
  year: 2021,
  overview: "Paul Atreides leads the Fremen.",
  posterPath: "/api/images/poster/movie-1",
  backdropPath: "/api/images/backdrop/movie-1",
  inLibrary: true,
  monitored: true,
  badge: { state: "missing", label: "Missing" },
  seasons: [],
  historySummary: "Nothing tried yet.",
  history: [],
  ...over,
});

export const release = (id: string, name: string, quality: string, sizeBytes: number, source: string, cached = true) => ({ id, name, quality, sizeBytes, source, cached });

export const rels: Releases = {
  candidates: [
    release("rel-1", "Dune.2021.2160p.WEB-DL", "WEBDL-2160p", 34_359_738_368, "Comet"),
    release("rel-2", "Dune.2021.1080p.BluRay.x264", "Bluray-1080p", 12_884_901_888, "Torrentio"),
    release("rel-3", "Dune.2021.1080p.WEB-DL", "WEBDL-1080p", 6_442_450_944, "Comet"),
    release("rel-4", "Dune.2021.720p.BluRay", "Bluray-720p", 4_294_967_296, "Torrentio"),
    release("rel-5", "Dune.2021.720p.WEB", "WEBDL-720p", 2_147_483_648, "Comet"),
  ],
  dropped: [
    { release: release("rel-6", "Dune.2021.720p.HDTV", "HDTV-720p", 3_221_225_472, "Comet", false), reason: "not_cached", message: "not cached on the debrid service" },
    { release: release("rel-7", "Dune.2021.1080p.exe", "WEBDL-1080p", 4_194_304_000, "Torrentio"), reason: "not_video", message: "a .exe file, not a video" },
    { release: release("rel-8", "Dune.2021.1080p.WEB", "WEBDL-1080p", 1_610_612_736, "Comet"), reason: "blocklisted", message: "failed before (stalled); blocked for 30 days" },
  ],
};

export const queued = (over: Partial<Download> = {}): Download => ({
  id: "dl-1",
  titleId: "movie-1",
  titleName: "Dune",
  releaseName: "Dune.2021.2160p.WEB-DL",
  state: "queued",
  percent: 0,
  bytesDone: 0,
  bytesTotal: 34_359_738_368,
  stalled: false,
  ...over,
});

export const aboutOf = (over: Partial<TitleAbout> = {}): TitleAbout => ({
  ratings: [
    { source: "tmdb", score: 8.2, scale: 10, votes: 2900 },
    { source: "imdb", score: 8.4, scale: 10, votes: 310000 },
    { source: "rotten_tomatoes", score: 95, scale: 100 },
  ],
  genres: ["Drama", "Mystery", "Comedy", "Extra"],
  runtimeMinutes: 44,
  status: "Ended",
  language: "English",
  cast: [
    { personId: 11, name: "Hugh Laurie", character: "Gregory House", profilePath: "/api/people/11/image" },
    { personId: 12, name: "Lisa Edelstein", character: "" },
  ],
  similar: [{ id: "series-9", tmdbId: 9, kind: "series", title: "Scrubs", year: 2001, overview: "", posterPath: "/api/images/poster/series-9", backdropPath: "", inLibrary: false }],
  ...over,
});
