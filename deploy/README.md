# cpa-ext on Oracle

The checkout lives at `/home/ubuntu/cpa-ext`. Oracle uses Linux ARM64. The Docker Compose project is `cpa-ext`.

| Loopback port | Service |
| --- | --- |
| 8480 | Client API gateway |
| 8481 | cpa-ext admin |
| 8482 | Official CPAMC, CPA management API, and the embedded cpa-ext page |

Create an SSH tunnel using the existing Oracle host and key. Forward ports 8480, 8481 and 8482. Open `http://127.0.0.1:8482/management.html` and sign in with `CPA_MANAGEMENT_KEY` from the server's private `.env`. Select **cpa-ext** in the CPAMC sidebar, then enter `ADMIN_PASSWORD` once in the embedded page. The standalone extension UI is available on `http://127.0.0.1:8481/`. API automation can use HTTP Basic with username `admin` and `ADMIN_PASSWORD`.

The sidebar entry is a native CPA resource plugin, built from `plugins/cpamc/plugin.c` by `deploy/build-panel-plugin.sh`. It directs the official CPAMC frame to the same-origin `/cpa-ext/` path. The listener on 8482 serves the management surface and the embedded UI. Use 8480 for inference. The nested dashboard login is scoped to `/cpa-ext/`.

## Configure

1. Run `bash deploy/init.sh` and `sudo docker compose -p cpa-ext up -d --build`. Initialization preserves existing credentials and verifies the official CPAMC asset digest.
2. In CPAMC, add upstream credentials and create a downstream client key. cpa-ext automatically synchronizes the CPA client keys. In Key quotas, select a synchronized key and set its optional policy; no secret needs to be copied. The extension stores SHA-256 identities and masked labels, never client secrets. Removal in CPA prevents new calls while preserving history; synchronization errors retain the last successful snapshot.
3. Prices need no setup. cpa-ext synchronizes the public list-price catalog at https://models.dev/api.json every `PRICE_SYNC_MINUTES` (default 15) with a conditional request, so an unchanged catalog costs one small 304. First-party provider prices win over resellers publishing the same model ID. Every usage event is priced from the model CPA reports as billed; a provider prefix (`devin/`) and reasoning-effort suffixes (`-high`, `-medium`, …) are ignored when looking it up, and context tiers apply above their prompt size. Lookup order is manual override, synchronized catalog, then the built-in reference rows. Each catalog rate that is used gets a versioned `prices` row (`protocol='*'`); a later price change adds a new row and never rewrites charges already recorded. Amounts are list-price estimates, not the upstream debit. An event whose model has no price stays pending and is charged automatically once a price appears. `GET /api/health` reports the last check, last change and any sync error under `prices`. A manual override is still possible through `POST /api/prices` (USD per million tokens × 1000). A price can be marked verified, which quota keys require, only after confirming the route has a single billed execution, the actual billed model and token limits match the price, and that the provider enforces the request's output cap. CPA retries, fallback models or upstream tools can invalidate a certified bound. Recheck a verified price after updating CPA, route configuration, or upstream credentials.
4. Call inference using the cpa-ext endpoint on port 8480. Review spend, pending items, and session transcripts in the admin UI.

Strict keys fail closed without established bounds. Current WebSocket forwarding and transcripts are enabled for unlimited keys; limited keys are refused per generation while a stable official per-turn accounting identifier remains unverified. A missing or ambiguous event stays pending. The collector is the sole consumer of the official CPA management usage queue. The queue is held in memory by CPA and its read endpoint removes the returned events, so loss during upstream or collector failure can prevent automatic settlement.

## Data and maintenance

- The active CPA configuration, credential files, CPAMC asset, SQLite ledger, and compressed bodies live under `state/`. Secrets also live in `.env`.
- Back up `.env` and `state/` as a unit. Stop just the two cpa-ext project services before copying for a consistent full backup. Store the backup privately because it contains credentials and conversations. Restore with both services stopped, preserve ownership, then start the Compose project.
- Archive retention defaults to 14 days. Compressed storage defaults to 20 GiB and free disk must stay above 10 GiB. Existing turns use their reserved capture slots when the service refuses new work at the storage threshold. Requests are capped at 1 MiB, captured responses at 4 MiB. A larger response continues downstream and is marked truncated.
- A clean shutdown returns unassigned balance. An abrupt stop preserves outstanding leases durably. Use the admin view to check unsettled amounts and compare against the usage ledger before any manual correction.
- Build with the pinned Go image. Run `go test ./...` and `go vet ./...`. With the local image tagged `cpa-ext:dev`, start `sudo docker compose -p cpa-ext-test -f tests/e2e/compose.yaml up -d`, run `python3 tests/e2e/test.py`, then run the same Compose command with `down`. The fixture uses independent services and temporary data.


The isolated test project also supports `python3 tests/e2e/protocols.py` and `python3 tests/bench/compare.py` after its initial test. The benchmark excludes live vendor latency and reports the measured differences on the Oracle host.

## CPA ownership

Create and delete client keys and manage upstream credential files in official CPAMC. cpa-ext only owns quota policies, reference pricing, historical usage, spend, and conversation audit. The duplicate credential-state tab, its shortcut button and the demonstration data switch have been removed; the page shows live ledger data only.

## Compact interface

The normal interface contains only Overview, Key quotas, Spend ledger and Session audit. Key synchronization runs in the background and the quota list refreshes quietly while no editor is open. Sync status cards and duplicate policy/status columns are not shown. Reference prices and versioned manual overrides remain internal billing data; the standalone price configuration tab is removed. Existing admin pricing APIs remain available for advanced configuration.

## Public domain

Production domain: https://cpa.meetran.com. The root redirects to official CPAMC at /management.html; the extension is available in the CPAMC plugin sidebar. OpenAI-compatible clients use https://cpa.meetran.com/v1. No SSH tunnel is required.

Caddy terminates HTTPS on loopback 8443 behind the existing sing-box Reality fallback on public 443. Caddy owns automatic Let’s Encrypt certificate renewal. Management, extension resources, and OAuth callbacks are routed to the panel listener on 8482; all other paths are routed to the inference gateway on 8480 so clients cannot bypass quota and audit. Each route lists CPA itself (loopback 8317) as a second upstream with `lb_policy first`: Caddy uses it only while cpa-ext refuses connections. Public sessions receive Secure cookies and HSTS. Existing CPA management and extension administrator credentials still apply.

The Caddy configuration is /etc/caddy/Caddyfile; the pre-domain backup is /etc/caddy/Caddyfile.before-cpa-20261121. No sing-box, CPA, Magpie, or firewall changes were needed.

## Maintenance without interruption

cpa-ext can be rebuilt and restarted while clients keep working:

- On SIGTERM the listeners close immediately. Caddy then sends new requests straight to CPA until cpa-ext is back; streams already in flight get `SHUTDOWN_GRACE_SECONDS` (default 120, with `stop_grace_period: 150s`) to finish, and idle WebSocket sessions end between generations.
- Requests that went straight to CPA have no quota check and no transcript. Their usage is still read from the CPA queue afterwards and priced; such events simply have no linked turn. CPA keeps usage for at most an hour, so cpa-ext should not stay down longer than that.
- Deploy with `sudo docker compose -p cpa-ext up -d --build --no-deps cpa-ext`. Recreating the `cpa` service itself still interrupts inference for a few seconds.
- `tests/e2e/drain.py` covers this path in the isolated test project (start it with the `drain.compose.yaml` overlay): it stops the container during an eight-second stream and checks that the listener closes at once, the stream completes, and the turn is archived and settled after the restart.
