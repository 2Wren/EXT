# cpa-ext on Oracle

The checkout lives at `/home/ubuntu/cpa-ext`. Oracle uses Linux ARM64. The Docker Compose project is `cpa-ext`.

| Loopback port | Service |
| --- | --- |
| 8480 | Client API gateway |
| 8481 | cpa-ext admin |
| 8482 | Official CPAMC, CPA management API, and the embedded cpa-ext page |

Create an SSH tunnel using the existing Oracle host and key. Forward ports 8480, 8481 and 8482. Open `http://127.0.0.1:8482/management.html` and sign in with `CPA_MANAGEMENT_KEY` from the server's private `.env`. Select **EXT** in the CPAMC sidebar, then enter `ADMIN_PASSWORD` once in the embedded page. The standalone extension UI is available on `http://127.0.0.1:8481/`. API automation can use HTTP Basic with username `admin` and `ADMIN_PASSWORD`.

The sidebar entry is a native CPA resource plugin, built from `plugins/cpamc/plugin.c` by `deploy/build-panel-plugin.sh`. Its plugin ID stays `cpa-ext` (the file name and the key under `plugins.configs`); CPAMC shows it as EXT. CPA loads plugins only when it starts, so a rebuilt plugin takes effect after `sudo docker compose -p cpa-ext restart cpa`, which interrupts inference for a few seconds. It directs the official CPAMC frame to the same-origin `/cpa-ext/` path. The listener on 8482 serves the management surface and the embedded UI. Use 8480 for inference. The nested dashboard login is scoped to `/cpa-ext/`.

## Configure

1. Run `bash deploy/init.sh` and `sudo docker compose -p cpa-ext up -d --build`. Initialization preserves existing credentials and verifies the official CPAMC asset digest.
2. In CPAMC, add upstream credentials and create a downstream client key. cpa-ext automatically synchronizes the CPA client keys. In Key limits (密钥限制), select a synchronized key and set its optional limits; no secret needs to be copied. The extension stores SHA-256 identities and masked labels, never client secrets. A name given to a key in CPAMC's client API key list is shown for that key everywhere in cpa-ext. CPAMC keeps those names only in the browser (localStorage, per API base, filed under a hash of the base and the key), so the cpa-ext page reads them when it loads and posts them to `POST /api/keys/names`, where they are matched to the keys and saved in the ledger; other browsers then see them too. Names are managed only in CPAMC; a key without one is shown by its masked form. Clearing a name in CPAMC leaves the last saved name in place. Removal in CPA prevents new calls while preserving history; synchronization errors retain the last successful snapshot.
3. Prices need no setup. cpa-ext synchronizes the public list-price catalog at https://models.dev/api.json every `PRICE_SYNC_MINUTES` (default 15) with a conditional request, so an unchanged catalog costs one small 304. First-party provider prices win over resellers publishing the same model ID. Every usage event is priced from the model CPA reports as billed; a provider prefix (`devin/`) and reasoning-effort suffixes (`-high`, `-medium`, …) are ignored when looking it up, and context tiers apply above their prompt size. Lookup order is manual override, synchronized catalog, then the built-in reference rows. Each catalog rate that is used gets a versioned `prices` row (`protocol='*'`); a later price change adds a new row and never rewrites charges already recorded. Amounts are list-price estimates, not the upstream debit. An event whose model has no price stays pending and is charged automatically once a price appears. `GET /api/health` reports the last check, last change and any sync error under `prices`. A manual override is still possible through `POST /api/prices` (USD per million tokens × 1000). The `verified` flag and the token bounds of a manual price are kept for the record only; limits do not depend on them.
4. Call inference using the cpa-ext endpoint on port 8480. Review spend, pending items, and session transcripts in the admin UI.

A key can be limited to certain models in Key limits ("可调用模型"): a comma-separated list, matched without case, where `*` stands for any run of characters and a routing prefix such as `devin/` may be left out; an empty list allows every model. The gateway answers `403 model_not_allowed` for any other model, including each WebSocket generation, and the model list endpoints return only the allowed models for such a key. Like the spending limits, the restriction is enforced by cpa-ext, so it does not apply to requests Caddy sends straight to CPA while cpa-ext is down or restarting.

Request details (`GET /api/events`) lists every usage event newest first with its result, cost and linked turn. `from` and `to` (Unix milliseconds, `to` exclusive) select a period, `page` and `size` (default 50, at most 200) page through it, and the response carries the selection's `total`, `failed` and `cost` next to `rows`.

The overview, the request details and the session audit share one period selection in the UI: today (the default), yesterday, the last 7 or 30 days, everything, or a custom span, all in Beijing time. `GET /api/overview`, `/api/series` and `/api/sessions` take the same `from` and `to`. The series is totalled per Beijing day, or per hour with `unit=hour`, which the UI asks for when the period is two days or shorter. `/api/sessions` is paged like the request details and lists every session that had a request in the period with the figures of the whole session; with `key` and `session` it pages through the turns of that session. The overview table is paged in the browser.

A key can carry a weekly limit and a total limit in USD; zero means none, and the weekly window restarts at the key's configured reset time in Beijing time: `week_reset` is `w HH:MM` where `w` is 0 (Sunday) through 6 (Saturday), and an empty value is the default Monday 00:00. Before forwarding a request, and before each WebSocket generation, the gateway compares the spend already recorded for the key with both limits and answers `429 limit_reached` as soon as either is used up; the message says which one. Listing models stays possible. Spend is the list-price estimate of the usage CPA reported, so it trails a request by a few seconds: requests in flight when a limit is reached complete, the limit can be overshot by their cost, and usage of a model that has no price yet does not count until it is priced. There is no reservation and no per-key switch; to stop a key, delete it in CPAMC. The collector is the sole consumer of the official CPA management usage queue. The queue is held in memory by CPA and its read endpoint removes the returned events, so usage CPA drops while the collector is down for more than an hour is never charged.

## Data and maintenance

- The active CPA configuration, credential files, CPAMC asset, SQLite ledger, and compressed bodies live under `state/`. Secrets also live in `.env`.
- Back up `.env` and `state/` as a unit. Stop just the two cpa-ext project services before copying for a consistent full backup. Store the backup privately because it contains credentials and conversations. Restore with both services stopped, preserve ownership, then start the Compose project.
- Archive retention defaults to 14 days. Compressed storage defaults to 20 GiB and free disk must stay above 10 GiB. Existing turns use their reserved capture slots when the service refuses new work at the storage threshold. The gateway forwards complete request bodies and caps captured responses at 4 MiB. A larger response continues downstream and the capture is marked truncated. Archives over the 32 MiB inline preview limit show the turn summary and offer an authenticated gzip download.
- Build with the pinned Go image. Run `go test ./...` and `go vet ./...`. With the local image tagged `cpa-ext:dev`, start `sudo docker compose -p cpa-ext-test -f tests/e2e/compose.yaml up -d`, run `python3 tests/e2e/test.py`, then run the same Compose command with `down`. The fixture uses independent services and temporary data.


The isolated test project also supports `python3 tests/e2e/protocols.py` and `python3 tests/bench/compare.py` after its initial test. The benchmark excludes live vendor latency and reports the measured differences on the Oracle host.

## CPA ownership

Create and delete client keys and manage upstream credential files in official CPAMC. cpa-ext only owns key limits, reference pricing, historical usage, spend, and conversation audit. The duplicate credential-state tab, its shortcut button and the demonstration data switch have been removed; the page shows live ledger data only.

## Compact interface

The normal interface contains only Overview, Key limits, Request details and Session audit. Key synchronization runs in the background and the limits list refreshes quietly while no editor is open. Sync status cards and duplicate policy/status columns are not shown. Reference prices and versioned manual overrides remain internal billing data; the standalone price configuration tab is removed. Existing admin pricing APIs remain available for deployment-specific overrides, including zero rates for usage included in a subscription. Review such overrides when the subscription changes.

## Public domain

Production domain: https://sub.meetran.com. The root redirects to official CPAMC at /management.html; the extension is available in the CPAMC plugin sidebar. OpenAI-compatible clients use https://sub.meetran.com/v1. No SSH tunnel is required.

Caddy terminates HTTPS on loopback 8443 behind the existing sing-box Reality fallback on public 443. Caddy owns automatic Let’s Encrypt certificate renewal. Management, extension resources, and OAuth callbacks are routed to the panel listener on 8482; all other paths are routed to the inference gateway on 8480 so clients cannot bypass limits and audit. Each route lists CPA itself (loopback 8317) as a second upstream with `lb_policy first`: Caddy uses it only while cpa-ext refuses connections. Public sessions receive Secure cookies and HSTS. Existing CPA management and extension administrator credentials still apply.

The Caddy configuration is /etc/caddy/Caddyfile.

## Maintenance without interruption

cpa-ext can be rebuilt and restarted while clients keep working:

- On SIGTERM the listeners close immediately. Caddy then sends new requests straight to CPA until cpa-ext is back; streams already in flight get `SHUTDOWN_GRACE_SECONDS` (default 120, with `stop_grace_period: 150s`) to finish, and idle WebSocket sessions end between generations.
- Requests that went straight to CPA have no limit or model check and no transcript. Their usage is still read from the CPA queue afterwards and priced; such events simply have no linked turn. CPA keeps usage for at most an hour, so cpa-ext should not stay down longer than that.
- Deploy with `sudo docker compose -p cpa-ext up -d --build --no-deps cpa-ext`. Recreating the `cpa` service itself still interrupts inference for a few seconds.
- `tests/e2e/drain.py` covers this path in the isolated test project (start it with the `drain.compose.yaml` overlay): it stops the container during an eight-second stream and checks that the listener closes at once, the stream completes, and the turn is archived and settled after the restart.
