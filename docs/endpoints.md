# Confirmed endpoints

What has actually been reached on a controller, as opposed to what the vendor
reference lists. Useful when you need a value and want to know whether a path
exists before probing for it.

Developed against controller 6.3.0.45. The vendor's newest published set is
6.2.14, so treat anything here as "seen working on 6.3.x", not as a contract.

## Probing: the error codes tell you which kind of wrong you are

Both surfaces return the same envelope and distinguish their failure modes, so
candidate paths can be probed and the *code* read rather than the payload:

| code | means |
|---|---|
| `-1600 Unsupported request path` | no such endpoint |
| `-1001 Invalid request parameters`, `-1 General error` | endpoint exists, arguments are wrong |
| `-44118 This interface only supports the authorization code mode` | exists, but not reachable with client-credentials auth |

Before concluding that an endpoint is broken, confirm the request you think you
sent is the one that went out.

## Open API (`get`)

`sites`, `sites/{site}/clients`, `sites/{site}/clients/{mac}`,
`sites/{site}/devices`, `sites/{site}/aps/{mac}`,
`sites/{site}/insight/clients`, `sites/{site}/logs/events`,
`sites/{site}/logs/alerts`, `roles`.

Radio and RF:

| path | what it gives |
|---|---|
| `sites/{site}/band-steering` | `mode`: 0 disable, 1 prefer 5/6 GHz, 2 balance |
| `sites/{site}/channel-limit` | site-wide channel restriction on/off |
| `sites/{site}/rfPlanning` | auto channel/width/band/power deploy flags, schedule |
| `sites/{site}/rfPlanning/result` | last RF-planning run (`status` only when none) |
| `sites/{site}/roaming` | `fastRoamingEnable`, `aiRoamingEnable`, `dualBand11kReportEnable` |
| `sites/{site}/aps/{mac}/radio-config` | per-radio `channel` (an *index*), `channelWidth`, `txPower` |
| `sites/{site}/aps/{mac}/available-channel` | the index → channel/freq/width table |
| `sites/{site}/aps/{mac}/radios` | per-radio detail |
| `sites/{site}/aps/{mac}/load-balance` | per-AP max-clients and min-RSSI flags |
| `sites/{site}/aps/{mac}/rf-scan-result` | last RF scan (`status` only when none) |

**`radio-config` cannot be read on its own.** Its `channel` is a **1-based index
into `available-channel`**, not a channel number, and `channelWidth` is an enum
(`2`=20, `3`=40, `4`=2.4 GHz auto, `5`=80, `6`=5 GHz auto). So `channel: "2"`
with `channelWidth: "5"` means *channel 40 at a fixed 80 MHz*. Read the two
together, or the answer is wrong — and on 2.4 GHz index and channel number
coincide, which makes the mistake easy to miss.

Confirmed **absent**: `sites/{site}/events`, `sites/{site}/dashboard`,
`sites/{site}/networks`, `sites/{site}/wlans`, `sites/{site}/statistics`, and the
plural `sites/{site}/aps` list form (use `devices`).

Not every Open API endpoint is under `/openapi/v1/` — the log-notification
settings are v2. A `get` path may name its own version:

```sh
omadactl get 'sites/{site}/clients' -query 'filters.connectType=1'
omadactl get 'v2/sites/{site}/site/log-notification'
```

`sites/{site}/logs/events` **requires** `filters.timeStart` and
`filters.timeEnd` in epoch milliseconds, and answers `-1 General error` without
them — which reads like a server fault rather than a missing argument. Optional
`filters.module` is one of `System`, `Device`, `Client`. This is the endpoint the
Site Event CSV export comes from, so it replaces the manual export.

## Internal web API (`web`)

Unversioned: TP-Link can change any of this in a firmware release.

`sites`, `sites/{site}/devices`, `sites/{site}/eaps/{mac}`,
`sites/{site}/switches/{mac}`, `sites/{site}/gateways/{mac}`,
`sites/{site}/clients/{mac}`, `sites/{site}/insight/clients`,
`sites/{site}/events`, `sites/{site}/alerts`,
`sites/{site}/dashboard/overviewDiagram`,
`sites/{site}/dashboard/clientsDistribution`, `sites/{site}/setting/wlans`,
`sites/{site}/setting/wlans/{id}/ssids`, `sites/{site}/setting/lan/networks`,
`users/current`.

## Finding more

[`api-documentation.md`](api-documentation.md) explains where the vendor
reference lives and how to read it. `openapi-operations-oc_series-6.2.14.txt`
is the full list of 1,760 operation ids, extracted from the vendor sitemap —
search it for a noun, then open that operation's page.
