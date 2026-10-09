# omadactl

Reads a TP-Link Omada controller and prints JSON on stdout, one document per
command. Built so an LLM agent can extract live network data: per-client RSSI,
SNR, band, negotiated rate, AP association and traffic, device and radio detail,
and the site event log.

Developed for controller version **6.3.0.45** (an OC300). Uses some
internal/unstable APIs so when using against other versions some features
might be missing or broken.

Can be used to snapshot network states to understand, troubleshoot and improve
network performance based on live data.

## Install

```sh
go install github.com/lheiskan/omadactl/cmd/omadactl@latest
```
## One-time setup

1. In the controller UI: **Global View → Settings → Platform Integration →
   Open API → Add New App**
   - authorization mode **Client Credentials** a **Viewer** role

   Then open the app's **View** dialog and note three things: the **client id**,
   the **client secret**, and the **Interface Access Address**. 

2. Get the `omadacId`, using the **Interface Access Address** from above (https://192.168.1.10:443)

   ```sh
   omadactl -api-url https://192.168.1.10:443 info
   ```

3. Put it all in `~/.config/omada/config.json` (mode 0600):

   ```json
   {
     "api_url": "https://192.168.1.10:443", // this is the 'interface access address from step 1 above'
     "omadac_id": "…from step 2…",
     "client_id": "…from step 1…",
     "client_secret": "…from step 1…",
     "username": "…",
     "password": "…",
     "insecure": true
   }
   ```

   `username`/`password` are a controller account, used only by the web-API
   commands below. A **Viewer**-role account is enough for everything omadactl
   reads.

   `insecure` defaults to true because controllers ship a self-signed
   certificate.

   Every field can be overridden by the environment — `OMADA_API_URL`,
   `OMADA_ID`, `OMADA_CLIENT_ID`, `OMADA_CLIENT_SECRET`, `OMADA_USERNAME`,
   `OMADA_PASSWORD`, `OMADA_SITE`, `OMADA_INSECURE` — or by the `-api-url`,
   `-site` and `-insecure` flags. `OMADA_CONFIG` moves the config file itself.

   > The address was previously called `controller`, with the flag `-controller`
   > and the variable `OMADA_CONTROLLER`. All three are still accepted, so old
   > configs keep working; `api_url` wins if both are present.

4. Check it:

   ```sh
   go run ./cmd/omadactl token
   go run ./cmd/omadactl sites
   ```

## Commands

| command | output |
|---|---|
| `info` | controller version and `omadacId`; the only call needing no credentials |
| `token` | token expiry, for a connectivity check (the token itself is not printed) |
| `sites` | sites visible to the application |
| `clients` | connected clients: `rssi`, `snr`, `signalLevel`, `healthScore`, `channel`, `radioId`, `wifiMode`, `txRate`/`rxRate`, `apName`/`apMac`, `ssid`, `trafficUp`/`trafficDown`, `uptime` |
| `client <mac>` | full detail for one client |
| `devices` | adopted devices: model, firmware, ip, uptime, `cpuUtil`, `memUtil` |
| `ap <mac>` | full detail for one access point, including radio settings |
| `insight` | historically known clients: last seen, session duration, traffic totals |
| `events` | site event log for the last `-since` (default 24h) |
| `get <path>` | any Open API path under `/openapi/v1/{omadacId}`; `{site}` is substituted |

These use the internal web API (username/password):

| command | output |
|---|---|
| `alerts` | site alert log for the last `-since` |
| `dashboard` | site overview counters: device counts, CPU/memory, client totals |
| `web <path>` | any internal path under `/{omadacId}/api/v2/`; `{site}` is substituted |

## Two API surfaces

The **Open API** (`get`) is versioned and survives firmware upgrades, but its
endpoint set is small. The **internal web API** (`web`) is what the controller's
own UI calls — much richer, but unversioned, so treat any `web` path as liable
to change. Prefer the Open API where an equivalent exists.

[`docs/endpoints.md`](docs/endpoints.md) lists what has actually been reached on
a controller, on both surfaces, and the error codes that let you probe for more.

## Notes

The Open API access token is cached under `~/.cache/omada/` (0600). The web API
session is not cached, which costs one login per invocation — see
[`AGENTS.md`](AGENTS.md) for why that is deliberate.

Design rules that follow from having a machine as the caller:

- **One JSON document per command** on stdout. Errors go to stderr with a
  non-zero exit, so nothing has to be parsed out of the payload.
- **List commands always emit an array**, even for zero or one row — a consumer
  never needs a special case for "exactly one client is connected".
- **Nothing is interactive.** Credentials come from a config file or the
  environment; paging and token caching are handled internally.

Flags may appear before or after the command, and MACs may be written with `:`
or `-`.

Changing the code? [`AGENTS.md`](AGENTS.md) has the invariants and the traps.
