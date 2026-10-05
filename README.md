# omadactl

Reads a TP-Link Omada controller and prints JSON on stdout, one document per
command. Built so an **LLM agent — or any other automated consumer** — can
extract live network data: per-client RSSI, SNR, band, negotiated rate, AP
association and traffic, device and radio detail, and the site event log.
Without it you are limited to the DHCP lines that survive into a Site Event CSV
export, or to scraping the UI.

Design rules that follow from having a machine as the caller:

- **One JSON document per command** on stdout. Errors go to stderr with a
  non-zero exit, so nothing has to be parsed out of the payload.
- **List commands always emit an array**, even for zero or one row — a consumer
  never needs a special case for "exactly one client is connected".
- **Nothing is interactive.** Credentials come from a config file or the
  environment; paging and token caching are handled internally.
- **It knows nothing about any particular network.** Controller address, site
  and credentials are all inputs.

## Install

```sh
go install github.com/lheiskan/omadactl/cmd/omadactl@latest
```

Developed against controller **6.3.0.45** (an OC300).

> **Before adding an endpoint, read the API reference** —
> [`docs/api-documentation.md`](docs/api-documentation.md) explains where it
> lives, which parts are fetchable and which need a browser, and how to find an
> operation. TP-Link adds endpoints every release; the published spec states
> required parameters and permissions. Probe only where the docs are silent.

## The port gotcha

**The APIs are not on the web UI port.** The UI answers on 8043; both the Open
API *and* the internal web API live on **443**. Hitting
`:8043/openapi/authorize/token` returns an HTTP 500 Tomcat error page — not a
JSON error, and identically whether your credentials are valid, invalid, or the
application does not exist at all. `:8043/{omadacId}/api/v2/login` 500s the same
way. That failure mode is indistinguishable from a broken credential, so state
it plainly: if a call 500s with an HTML body, check the port before anything
else.

The authoritative value is the **Interface Access Address**, shown in the
controller UI in the *view* dialog of the Open API application itself (not in
the page header, and not visible at all until an application exists). Use
exactly what it says.

## One-time setup

1. Find the controller and its id (needs no credentials — any port works here):

   ```sh
   go run ./cmd/omadactl -controller https://192.168.1.10:8043 info
   ```

2. In the controller UI: **Global View → Settings → Platform Integration →
   Open API → Add New App**
   - authorization mode **Client Credentials**
   - a **Viewer** role, so the token cannot change the network
   - scoped to the site you want to query

   Then open the app's *view* dialog and note the **Interface Access Address**.

3. Put it all in `~/.config/omada/config.json` (mode 0600):

   ```json
   {
     "controller": "https://192.168.1.10:443",
     "omadac_id": "…from step 1…",
     "client_id": "…",
     "client_secret": "…",
     "username": "…",
     "password": "…",
     "insecure": true
   }
   ```

   `username`/`password` are a controller account, used only by the web-API
   commands below. A **Viewer**-role account is enough for everything omadactl
   reads.

   `insecure` defaults to true because controllers ship a self-signed
   certificate. Every field can be overridden by `OMADA_CONTROLLER`,
   `OMADA_ID`, `OMADA_CLIENT_ID`, `OMADA_CLIENT_SECRET`, `OMADA_USERNAME`,
   `OMADA_PASSWORD`, `OMADA_SITE`, `OMADA_INSECURE`, or by `-controller` /
   `-site` / `-insecure`.

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
to change. Prefer Open API where an equivalent exists.

Both return the same JSON envelope and distinguish their failure modes
usefully, which makes the surface enumerable by probing:

- `-1600 Unsupported request path` — no such endpoint
- `-1001 Invalid request parameters` / `-1 General error` — endpoint exists, arguments are wrong
- `-44118 This interface only supports the authorization code mode` — exists, but not reachable with client-credentials auth

Confirmed on Open API: `sites`, `sites/{site}/clients`,
`sites/{site}/clients/{mac}`, `sites/{site}/devices`, `sites/{site}/aps/{mac}`,
`sites/{site}/insight/clients`, `sites/{site}/logs/events`,
`sites/{site}/logs/alerts`, `roles`.

Also confirmed, for radio and RF work:

| path | what it gives |
|---|---|
| `sites/{site}/band-steering` | `mode`: 0 disable, 1 prefer 5/6 GHz, 2 balance |
| `sites/{site}/channel-limit` | site-wide channel restriction on/off |
| `sites/{site}/rfPlanning` | auto channel/width/band/power deploy flags, schedule |
| `sites/{site}/rfPlanning/result` | last RF-planning run (`status` only when none) |
| `sites/{site}/aps/{mac}/radio-config` | per-radio `channel` (an *index*), `channelWidth`, `txPower` |
| `sites/{site}/aps/{mac}/available-channel` | the index → channel/freq/width table |
| `sites/{site}/aps/{mac}/load-balance` | per-AP max-clients and min-RSSI flags |
| `sites/{site}/aps/{mac}/rf-scan-result` | last RF scan (`status` only when none) |

`radio-config`'s `channel` is a **1-based index into `available-channel`**, not a
channel number, and `channelWidth` is an enum (`2`=20, `3`=40, `4`=2.4 GHz auto,
`5`=80, `6`=5 GHz auto) — so a config of `channel: "2"`, `channelWidth: "5"` means
*channel 40 at a fixed 80 MHz*. Read the two together or the answer is wrong. Confirmed absent there:
`sites/{site}/events`, `sites/{site}/dashboard`, `sites/{site}/networks`,
`sites/{site}/wlans`, `sites/{site}/statistics`, and the plural `sites/{site}/aps`
list form (use `devices`).

`sites/{site}/logs/events` **requires** `filters.timeStart` and
`filters.timeEnd` in epoch milliseconds and returns `-1 General error` without
them, which reads like a server fault rather than a missing argument. Optional
`filters.module` is one of `System`, `Device`, `Client`. This is the endpoint
the Site Event CSV export comes from, so it replaces the manual export:

```sh
go run ./cmd/omadactl events -since 168h    # 3278 events over the last week
```

Confirmed on the web API: `sites`, `sites/{site}/devices`,
`sites/{site}/eaps/{mac}`, `sites/{site}/clients/{mac}`,
`sites/{site}/insight/clients`, `sites/{site}/events`, `sites/{site}/alerts`,
`sites/{site}/dashboard/overviewDiagram`,
`sites/{site}/dashboard/clientsDistribution`, `sites/{site}/setting/wlans`,
`sites/{site}/setting/lan/networks`, `users/current`.

This controller serves no local `/openapi/doc.html` — with a valid access token
it returns a clean 404. Use the published reference instead; see
[`docs/api-documentation.md`](docs/api-documentation.md) for how to reach it and
`docs/openapi-operations-oc_series-6.2.14.txt` for the full operation list.

Not every Open API endpoint is under `/openapi/v1/` — the log-notification
settings are v2, for instance. A `get` path may name its own version, or be
given fully qualified:

```sh
go run ./cmd/omadactl get 'sites/{site}/clients' -query 'filters.connectType=1'
go run ./cmd/omadactl get 'v2/sites/{site}/site/log-notification'
go run ./cmd/omadactl web 'sites/{site}/dashboard/clientsDistribution'
```

Beware that the published docs lag firmware: 6.2.14 documents that response as
`logNotifications`, while 6.3.0.45 returns `alertNotifications` and
`eventNotifications`. Read the docs for the path and the required parameters,
then confirm the response shape against the controller.

## Notes

Paging is handled automatically on both surfaces (they disagree on the
parameter names — `page`/`pageSize` versus `currentPage`/`currentPageSize`).
Flags may appear before or after the command; `omadactl get <path> -query k=v`
works. (Go's `flag` package would otherwise stop at the first positional and
silently drop those flags — a failure that surfaces as a confusing server-side
error, not a usage error.)

List commands always emit a JSON array, even for zero or one row, so a consumer
never needs a special case for "exactly one client is connected"; `client`/`ap`
emit the object itself. MACs may be written with `:` or `-`.

The Open API access token is cached under `~/.cache/omada/` (0600). The web API
session is not cached — it costs one login request per invocation.

## Licence

GPL-3.0-or-later. See [LICENSE](LICENSE).

Copyright (C) 2026 Lauri Heiskanen

This program is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later
version. It is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A
PARTICULAR PURPOSE. See the GNU General Public License for more details.
