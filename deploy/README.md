# cpa-ext on Oracle

The checkout lives at `/home/ubuntu/cpa-ext`. Oracle uses Linux ARM64. The Docker Compose project is `cpa-ext`.

| Loopback port | Service |
| --- | --- |
| 8480 | Client API gateway |
| 8481 | cpa-ext admin |
| 8482 | Official CPA and CPAMC |

Create an SSH tunnel using the existing Oracle host and key. Forward ports 8480, 8481 and 8482. Open `http://127.0.0.1:8481/` to administer the extension or `http://127.0.0.1:8482/management.html` for CPAMC. Use username `admin` with `ADMIN_PASSWORD` from the server's private `.env`. CPAMC uses `CPA_MANAGEMENT_KEY` from the same file.

## Configure

1. Run `bash deploy/init.sh` and `sudo docker compose -p cpa-ext up -d --build`. Initialization preserves existing credentials and verifies the official CPAMC asset digest.
2. In CPAMC, add upstream credentials and create a downstream client key. Register that same key in cpa-ext. cpa-ext stores its SHA-256 identity and forwards the original request credentials to CPA.
3. Create a price for the requested model alias and the billed model returned by CPA. Enter prices in USD per million tokens. A zero Key limit disables that dimension. A price can be marked verified only after confirming the route has a single billed execution, the actual billed model and token limits match the price, and that the provider enforces the request's output cap. CPA retries, fallback models or upstream tools can invalidate a certified bound. Recheck a verified price after updating CPA, route configuration, or upstream credentials.
4. Call inference using the cpa-ext endpoint on port 8480. Review spend, pending items, and session transcripts in the admin UI.

Strict keys fail closed without established bounds. Current WebSocket forwarding and transcripts are enabled for unlimited keys; limited keys are refused per generation while a stable official per-turn accounting identifier remains unverified. A missing or ambiguous event stays pending. The collector is the sole consumer of the official CPA management usage queue. The queue is held in memory by CPA and its read endpoint removes the returned events, so loss during upstream or collector failure can prevent automatic settlement.

## Data and maintenance

- The active CPA configuration, credential files, CPAMC asset, SQLite ledger, and compressed bodies live under `state/`. Secrets also live in `.env`.
- Back up `.env` and `state/` as a unit. Stop just the two cpa-ext project services before copying for a consistent full backup. Store the backup privately because it contains credentials and conversations. Restore with both services stopped, preserve ownership, then start the Compose project.
- Archive retention defaults to 14 days. Compressed storage defaults to 20 GiB and free disk must stay above 10 GiB. Existing turns use their reserved capture slots when the service refuses new work at the storage threshold. Requests are capped at 1 MiB, captured responses at 4 MiB. A larger response continues downstream and is marked truncated.
- A clean shutdown returns unassigned balance. An abrupt stop preserves outstanding leases durably. Use the admin view to check unsettled amounts and compare against the usage ledger before any manual correction.
- Build with the pinned Go image. Run `go test ./...` and `go vet ./...`. With the local image tagged `cpa-ext:dev`, start `sudo docker compose -p cpa-ext-test -f tests/e2e/compose.yaml up -d`, run `python3 tests/e2e/test.py`, then run the same Compose command with `down`. The fixture uses independent services and temporary data.


The isolated test project also supports `python3 tests/e2e/protocols.py` and `python3 tests/bench/compare.py` after its initial test. The benchmark excludes live vendor latency and reports the measured differences on the Oracle host.
