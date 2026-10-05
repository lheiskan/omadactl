# Reading the Omada API documentation

**Read the docs before probing.** TP-Link adds endpoints with every firmware
release, and the published reference is complete, versioned, and states which
parameters are required and which permission each endpoint needs. Guessing paths
is the fallback for when the docs are silent — not the starting point. A session
spent probing `logs/events` produced a confident, *wrong* conclusion that the
endpoint was broken; the docs settle the same question in one page.

## Where the reference lives

<https://omada-northbound-docs.tplinkcloud.com>

Pick the model family, then the version closest to (but not above) your
controller's:

| family | what it covers |
|---|---|
| `oc_series` | **Hardware Controller — the OC300 here** |
| `software` | Omada Software Controller |
| `er7212pc` | ER7212PC built-in controller |
| `cbc_essential`, `cbc_standard` | cloud-based controllers |

`omadactl info` reports the running version. This controller is 6.3.0.45, and
the newest published `oc_series` set is **6.2.14** — the docs lag firmware, so
expect occasional endpoints that exist but are undocumented, and vice versa.
Verify anything version-sensitive against the controller.

An operation page looks like:

```
https://omada-northbound-docs.tplinkcloud.com/models/oc_series/6.2.14/network/getEventLogsForSite
```

and gives the HTTP method, the full path, every parameter with its required
flag, the response schema, and the permission the calling application needs.

## Getting at it

The site is a JavaScript app, which changes how each part is reachable:

**Guides — plain JSON, fetch directly.** The access guides (authentication,
domains, error codes, audit-log and device-statistics appendices) are served as
JSON and need no browser:

```sh
curl -s https://omada-northbound-docs.tplinkcloud.com/api/catalog/menu.json
curl -s https://omada-northbound-docs.tplinkcloud.com/api/markdowns/organization-level-access-guide/content.json
```

The `content.json` payload is `.data.content`, an HTML string — strip the tags
and read it. `organization-level-access-guide` is the one worth knowing: §2.2
covers local controller addressing, §3.3 the client-credentials flow, §4 the
error codes, §5 the appendices.

**Operation pages — browser only.** These are compiled into the JS bundle and
fetch nothing at runtime, so curl and any HTML-to-text fetcher see an empty
shell. Open the URL in a real browser. `_payload.json` is 403, and the chunk
under `/_nuxt/` carries no `/openapi/` strings, so there is no shortcut around
rendering the page.

Two things make this cheap once you are in a browser:

- The page renders *after* navigation settles, so a read taken immediately
  returns nothing. Poll for the marker instead of sleeping a fixed time —
  `while (!document.body.innerText.includes('/openapi/')) await sleep(300)`.
- It is a Nuxt (Vue) SPA, so you can drive its router from the page console and
  read many operations in **one** call instead of one navigation each:

  ```js
  const router = document.querySelector('#__nuxt').__vue_app__
                   .config.globalProperties.$router
  for (const op of ['getRadiosConfig', 'getRFScanResult', /* … */]) {
    await router.push(`/models/oc_series/6.2.14/network/${op}`)
    let t = ''
    for (let i = 0; i < 20 && !t.includes('/openapi/'); i++) {
      await new Promise(r => setTimeout(r, 300))
      t = document.body.innerText
    }
    console.log(op, t.slice(t.indexOf('/openapi/')).split('\n')[0])
  }
  ```

  The sidebar has no `href`s — navigation is all JS handlers — so clicking links
  is not an option; the router is.

Reading the *full* page body can trip the browser tool's content filter on pages
whose examples look like query strings. `get_page_text` returns the same content
and does not.

**Finding an operation.** The sitemap lists every page:

```sh
curl -s https://omada-northbound-docs.tplinkcloud.com/sitemap.xml |
  grep -oE '<loc>[^<]+</loc>' | sed 's|<[^>]*>||g' |
  grep 'oc_series/6.2.14/' | grep -i event
```

`openapi-operations-oc_series-6.2.14.txt` in this directory is that list already
extracted — 1,760 operation IDs. Search it for a noun (`Event`, `Client`,
`Radio`, `Channel`), then open the matching page in a browser for the detail.

## Two things the docs settled that guessing got wrong

**The base URL is per-deployment.** §2.2.2: the REST API domain is the
*Interface Access Address* shown in the application's View dialog in the
controller UI. There is no fixed port. This controller serves the API on
**443**, while the web UI is on 8043; calling the UI port returns an HTTP 500
HTML page regardless of credentials, which reads exactly like an auth failure.

**`logs/events` requires an explicit time window.** `filters.timeStart` and
`filters.timeEnd`, epoch milliseconds, both mandatory. Omit them and the
controller answers `-1 General error` — indistinguishable from a server fault.

## When the docs genuinely do not cover it

The internal web API (`omadactl web`) is undocumented by design; it is what the
UI calls. There, probing is the only option, and the controller's error codes
make the surface enumerable:

- `-1600 Unsupported request path` — no such endpoint
- `-1001 Invalid request parameters` / `-1 General error` — exists, arguments wrong
- `-44118 only supports the authorization code mode` — exists, wrong auth mode

Before concluding from a probe that an endpoint is broken, confirm the request
you think you sent is the one that went out. Cross-check with curl, or reproduce
the exact URL. A failure that looks identical with valid and invalid credentials
is usually a client-side mistake.
