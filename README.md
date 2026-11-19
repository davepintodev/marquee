# marquee

marquee is a small self-hosted service with one screen. You type a movie or series name over a wall of posters, press **Add & monitor**, and marquee does the rest: it searches [AIOStreams](https://github.com/Viren070/AIOStreams) for releases, picks a good one, downloads the file over HTTP into your media library with Sonarr and Radarr's folder and file names, and refreshes Jellyfin. It keeps retrying, with backoff, until the title is complete.

It is one process in one container on one port, and idles under 80 MB of memory.

## What you need

- Docker with Compose.
- An AIOStreams manifest URL.
- A free [TMDB API key](https://www.themoviedb.org/settings/api) (posters, titles and episode lists).
- A media folder with a movies folder and a series folder inside it, for example `movies/` and `tv/`. Jellyfin (or any player) reads the same folders. If they already hold titles, marquee adopts them without renaming anything.
- Optional: a Jellyfin URL and API key, so marquee refreshes the library after each import.

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
| `MARQUEE_API_TOKEN` | no | Require this bearer token on the API; each browser asks for it once. Set it if anyone else can reach the port. Treated as a secret. |
| `MARQUEE_LISTEN` | no | Address to listen on. Default `:8080`. |

Secrets are redacted in logs, errors and the API.

The temp folder must be on the same filesystem as both library folders, so the final move into the library is an atomic rename. The example mounts one folder as `/media` and keeps the library and the temp folder inside it for that reason; marquee refuses to start if they are on different filesystems, and Health shows an error if it later finds them split.

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
