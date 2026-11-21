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
3. Built-in versioned reference prices automatically match exact model IDs for common OpenAI and Anthropic models. They are estimates with a source URL and date, not vendor debits or certified quota bounds. Aliases, unknown models, long-context rates, nonstandard service tiers, cache-write TTLs and additional tool fees need review or an explicit manual override. Manual prices take precedence and historical price IDs remain unchanged. Create an optional price override for the requested model alias and the billed model returned by CPA. Enter prices in USD per million tokens. A zero Key limit disables that dimension. A price can be marked verified only after confirming the route has a single billed execution, the actual billed model and token limits match the price, and that the provider enforces the request's output cap. CPA retries, fallback models or upstream tools can invalidate a certified bound. Recheck a verified price after updating CPA, route configuration, or upstream credentials.
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

Create and delete client keys and manage upstream credential files in official CPAMC. cpa-ext only owns quota policies, reference pricing, historical usage, spend, and conversation audit. The duplicate credential-state tab has been removed; its shortcut opens the official authentication-files page. The demonstration switch renders isolated browser-only sample data, never ledger entries.
