# marquee

marquee is a small self-hosted service with one screen. You type a movie or series name over a wall of posters, press **Add & monitor**, and marquee does the rest: it searches [AIOStreams](https://github.com/Viren070/AIOStreams) for releases, picks a good one, downloads the file over HTTP into your media library with Sonarr and Radarr's folder and file names, and refreshes Jellyfin. It keeps retrying, with backoff, until the title is complete.

It is one process in one container on one port, and idles under 80 MB of memory.

## What you need

- Docker with Compose.
- An AIOStreams manifest URL.
- A free [TMDB API key](https://www.themoviedb.org/settings/api) (posters, titles and episode lists).
- A media folder with a movies folder and a series folder inside it, for example `movies/` and `tv/`. Jellyfin (or any player) reads the same folders. If they already hold titles, marquee adopts them without renaming anything.
- Optional: a Jellyfin URL and API key, so marquee refreshes the library after each import.
- Optional: a [TorBox](https://torbox.app) API key, so marquee can also fetch releases that are not cached yet and whole season packs.

## Install

1. Clone this repository and copy the example:

   ```sh
   git clone https://github.com/davepintodev/marquee.git
   cd marquee
   cp compose.example.yaml compose.yaml
   ```

2. Edit `compose.yaml` and replace every `<placeholder>`:

   | Placeholder | What to put there |
   | --- | --- |
   | `<uid>:<gid>` | The user and group that may write to your media folder (`id -u` and `id -g`). |
   | `<media-dir>` | Absolute path of your media folder on the host. It must contain `movies/` and `tv/`. |
   | `<data-dir>` | Absolute path of an empty folder for marquee's database. It must be writable by the user above. |
   | `<aiostreams-manifest-url>` | Your AIOStreams manifest URL. |
   | `<tmdb-api-key>` | Your TMDB API key. |

3. Start it and read the log:

   ```sh
   docker compose up -d --build
   docker compose logs marquee
   ```

   If a setting is missing or a folder is wrong, marquee stops at startup with a message naming the setting.

4. Open `http://<host>:8080` in a browser and press **Health** at the top of the wall. It lists each check (AIOStreams, TMDB, library folders, temp folder, Jellyfin) as ok, warning or error with a plain-words message. The same data is at `/api/health`:

   ```sh
   curl http://<host>:8080/api/health
   ```

   With dry-run on (the example's default) the overall status is a warning, which is expected.

## Settings

Settings come from environment variables, or from a JSON file named by `MARQUEE_CONFIG`. A variable overrides the file. In the file, the key is the variable's name without `MARQUEE_`, lowercase (`tmdb_key`).

| Variable | Required | Meaning |
| --- | --- | --- |
| `MARQUEE_AIOSTREAMS_MANIFEST_URL` | yes | AIOStreams manifest URL. Treated as a secret. |
| `MARQUEE_TMDB_KEY` | yes | TMDB API key. Treated as a secret. |
| `MARQUEE_MOVIES_PATH` | yes | Movies folder. It must already exist. |
| `MARQUEE_SERIES_PATH` | yes | Series folder. It must already exist. |
| `MARQUEE_TEMP_PATH` | yes | Where downloads land before the move into the library. Created if missing. |
| `MARQUEE_DATA_DIR` | yes | Where the database lives. Created if missing. |
| `MARQUEE_DRY_RUN` | no | `true` searches and chooses but never downloads, never writes to the library or temp folder and never calls Jellyfin. Activity shows "would grab" for each decision. Default `false`; the example sets it to `true`. |
| `MARQUEE_JELLYFIN_URL`, `MARQUEE_JELLYFIN_KEY` | no | Refresh Jellyfin after an import. |
| `MARQUEE_SUBFIT_URL` | no | The address of a [subfit](#subfit) service, such as `http://<subfit-host>:<port>`. Treated as a secret. |
| `MARQUEE_RESCAN_INTERVAL` | no | How often marquee reads the library folders again, as a duration such as `30m` or `2h`; `0` turns the timer off. Default `30m`. See [Your library](#your-library). |
| `MARQUEE_TORBOX_API_KEY` | no | TorBox API key. Turns on the [TorBox fallback](#torbox). Treated as a secret. |
| `MARQUEE_TORBOX_MAX_ACTIVE` | no | How many torrents TorBox may be fetching for marquee at once. Default `2`. |
| `MARQUEE_TORBOX_STALL_TIMEOUT` | no | Give up on a torrent whose progress has not moved for this long. Default `30m`. |
| `MARQUEE_TORBOX_MAX_LIFETIME` | no | Give up on a torrent that is still not ready after this long. Default `4h`. |
| `MARQUEE_TORBOX_DELETE_ON_COMPLETE` | no | Remove the torrent from your TorBox account once its file is imported. Default `true`. |
| `MARQUEE_DEFAULT_MOVIE_PROFILE`, `MARQUEE_DEFAULT_SERIES_PROFILE` | no | The [quality profile](#quality-profiles) a movie or series gets when it names none. Default `default`. |
| `MARQUEE_OMDB_KEY` | no | A free [OMDb API key](https://www.omdbapi.com/apikey.aspx). With it a title page shows IMDb and Rotten Tomatoes marks beside TMDB's; without it only TMDB's. Treated as a secret. |
| `MARQUEE_API_TOKEN` | no | Require this bearer token on the API; each browser asks for it once. Set it if anyone else can reach the port. Treated as a secret. |
| `MARQUEE_SUBFIT_TOKEN` | no | A bearer token that opens only `GET /api/files/identity?library=<series\|movies>&path=<path-relative-to-that-library>`, which tells a subtitle tool such as subfit what a file is (ids, title, year, episode, original language, alternative titles). The `MARQUEE_API_TOKEN` also opens it; with neither set the lookup answers 401. Treated as a secret. |
| `MARQUEE_LISTEN` | no | Address to listen on. Default `:8080`. |

Quality profiles can only be set in the JSON file. Secrets are redacted in logs, errors and the API.

The temp folder must be on the same filesystem as both library folders, so the final move into the library is an atomic rename. The example mounts one folder as `/media` and keeps the library and the temp folder inside it for that reason; marquee refuses to start if they are on different filesystems, and Health shows an error if it later finds them split.

## Quality profiles

A profile says which releases marquee may download for a title. Out of the box there is one, `default`, made for players that cannot decode everything (an Android TV, for instance): WEB, HDTV and Blu-ray releases from 2160p down to 720p, never a Remux, and never a release tagged TrueHD, DTS-HD, DTS:X or Dolby Vision Profile 7.

To define your own, write a JSON config file, add `profiles` to it and point `MARQUEE_CONFIG` at it (in `compose.yaml`, uncomment the `<config-file>` mount and the `MARQUEE_CONFIG` line):

```json
{
  "profiles": {
    "tv-safe": {
      "qualities": ["WEBDL-1080p", "WEBRip-1080p", "Bluray-1080p", "WEBDL-720p"],
      "reject": ["truehd", "dts-hd", "dts-x", "dv-p7"],
      "prefer": ["dd+-atmos", "dd+", "hdr10+"],
      "size_limits": { "min_mb": 200, "max_mb": 30000 }
    },
    "anything-1080p": {
      "qualities": ["Remux-1080p", "Bluray-1080p", "WEBDL-1080p"]
    }
  },
  "default_movie_profile": "tv-safe",
  "default_series_profile": "tv-safe"
}
```

| Field | Meaning |
| --- | --- |
| `qualities` | Required. The qualities marquee accepts, best first. Anything else is never downloaded on its own. |
| `reject` | Format tags that rule a release out, however good its quality. |
| `prefer` | Format tags that rank a release higher among those of the same quality, first tag first. |
| `size_limits` | `min_mb` and `max_mb`, in MiB. `0` or missing means no limit. |
| `default_movie_profile`, `default_series_profile` | The profile a title gets when it names none. Each must be one of `profiles`. Defining any `profiles` replaces the built-in `default`, so name your own profile here (or keep one called `default`). |

Qualities: `Remux-2160p`, `Bluray-2160p`, `WEBDL-2160p`, `WEBRip-2160p`, `HDTV-2160p`, `Remux-1080p`, `Bluray-1080p`, `WEBDL-1080p`, `WEBRip-1080p`, `HDTV-1080p`, `Bluray-720p`, `WEBDL-720p`, `WEBRip-720p`, `HDTV-720p`, `Bluray-576p`, `Bluray-480p`, `WEBDL-480p`, `WEBRip-480p`, `DVD`, `SDTV`, `CAM`, `TELESYNC`, `TELECINE`, `DVDSCR`, `Unknown`.

Tags: `truehd`, `dts-hd`, `dts-x`, `dd+-atmos`, `dd+`, `dts`, `dd`, `aac`, `flac`, `opus`, `dv-p7`, `dv`, `hdr10+`, `hdr10`, `hdr`, `av1`, `hevc`, `avc`.

A name or tag marquee does not know stops startup with a message naming it. marquee reads tags from the release's file name, since a web release does not announce its Dolby Vision profile; a Dolby Vision Remux is always assumed to be Profile 7, and a Dolby Vision Blu-ray encode with no web marker is assumed to be Profile 7 unless it says Profile 8.

Each title picks its profile on its page or when you add it (the **Profile** menu); a title that picks none follows the default of its kind. marquee never swaps a file just because a better release appeared; to change one, use **Replace file** (below). In **Search releases**, a release the profile rules out is listed with the reason and a **Pick anyway** button.

## TorBox

Without a TorBox key marquee downloads only releases your debrid service already has cached. With `MARQUEE_TORBOX_API_KEY`:

- A cached release always wins. When there is none, marquee asks TorBox to fetch the best uncached release, and its poster shows **Caching** with a percentage until TorBox has it. The file then downloads and imports like any other. In **Search releases** such a release is badged **Uncached** and can be picked by hand.
- At most `MARQUEE_TORBOX_MAX_ACTIVE` torrents are fetched at a time; the others wait. A torrent that does not move for `MARQUEE_TORBOX_STALL_TIMEOUT`, or is still not ready after `MARQUEE_TORBOX_MAX_LIFETIME`, is removed from TorBox and the download fails (Activity says why), so marquee tries again later.
- When two or more episodes of one season are missing and a season pack exists, marquee fetches the pack once and imports each missing episode from it, instead of searching for every episode.
- After the import the torrent is deleted from your TorBox account, unless `MARQUEE_TORBOX_DELETE_ON_COMPLETE` is `false`.

Health shows a TorBox check once a key is set.

## Your library

marquee reads your library folders again every `MARQUEE_RESCAN_INTERVAL` (30 minutes unless you change it, and the first time one interval after start) and when you press **Rescan library** on the Health screen or **Rescan** on a title's page. A scan:

- records files it finds on disk;
- marks a movie or episode missing, and stops monitoring it, when its file was deleted outside marquee, so it does not download the same thing again;
- adopts folders it did not know, matching them against TMDB, without renaming anything.

On a title's page:

- **Remove…** stops marquee searching for the title. The files stay unless you tick **Also delete files from disk**, which deletes the title's files and the subtitles beside them.
- **Wrong file** deletes the title's file with its subtitles, blocks the release it came from and either searches again or lets you pick another release.
- **Replace file**, in Search releases, downloads the release you pick in place of the current file; the old file is deleted once the new one is in the library.
- One file can hold several episodes (`S01E01E02`). It is named and tracked as all of them, and **Wrong file** warns that they go missing together.

marquee also refuses a download whose size differs from what the release was listed with, and an episode file that is a byte-for-byte copy of another episode it already has.

## subfit

If you run subfit, the service that manages subtitles for your library, set `MARQUEE_SUBFIT_URL` and marquee tells it when files change, so it does not have to wait for its own scan. marquee posts a small JSON message to `<MARQUEE_SUBFIT_URL>/hook/marquee`:

- `import` when a download has been moved into the library;
- `replace` when **Replace file** swaps a file, naming the new and the old file;
- `delete` when **Wrong file** deletes a file, and once per deleted video when you remove a title with **Also delete files from disk**.

Each message names the library (`movies` or `series`) and the file's path inside that library's folder. A message is sent once, in the background, and never delays the import or the delete. If subfit is down, slow (marquee waits five seconds) or answers an error, marquee logs one warning with the event and the path, and subfit catches up at its next scan. Nothing is sent in dry-run mode, or while `MARQUEE_SUBFIT_URL` is unset.

## Trying it next to Sonarr and Radarr

Start with `MARQUEE_DRY_RUN` on. marquee adopts your existing folders by name and searches for what is missing, and Activity shows what it would have grabbed, while Sonarr and Radarr go on working. When its choices look right, set `MARQUEE_DRY_RUN` to `false`, run `docker compose up -d`, and stop the other tools so two programs do not fill the same library.

Adopted titles keep their file names. marquee reads episode lists from TMDB while Sonarr may have numbered some shows by TVDB, so check a few series in dry-run first. Anime and specials are not handled yet.

## Build from source

You need Go (the version in `go.mod`), Node 24 and `golangci-lint` v2.15 or newer.

```sh
make gate    # vet, lint, tests, web checks, build
bin/marquee --fake    # serves fixture data on :8080, no settings needed
```

`make build` writes `bin/marquee`; it needs the same settings as the container.

## License

GPL-3.0, see `LICENSE`.
