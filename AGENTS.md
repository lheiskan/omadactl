# Notes for agents working on omadactl

The README is for people using the tool. This file is for whoever *changes* it,
and holds only things that are not discoverable by reading the code. Don't
restate the README here; link to it.

## Invariants — breaking these breaks callers

The caller is a program, usually an LLM. The output contract is the product:

- **One JSON document per command** on stdout, nothing else. No progress text,
  no banners.
- **List commands always emit an array**, even for zero or one row. A real bug
  once shipped here: `sites` returned a bare object because the network had one
  site, while `clients` returned an array — same code path, shape decided by
  runtime data. A consumer that worked all week then broke.
- **Errors go to stderr with a non-zero exit.** Never an error object on stdout
  that a caller has to inspect.
- **Nothing interactive.** No prompts, no TTY assumptions.
- **No knowledge of any particular network.** Address, site and credentials are
  inputs. If you find yourself hardcoding a MAC or a site name, it belongs in a
  consumer, not here.

## Read the vendor docs before probing

`docs/api-documentation.md` explains where the reference lives and how to find
an operation. A long session was once spent concluding an endpoint was broken
when the docs answered it on one page. The failure mode is confident and wrong.

Reading them is awkward and the awkwardness is documented there: operation pages
are a client-rendered SPA, `curl` sees an empty shell, `_payload.json` is 403.
The trick that makes it cheap — drive the site's own router from the page
console to read many operations in one call — is written up in that file.

**The published docs lag firmware.** The newest `oc_series` set is 6.2.14 while
controllers run 6.3.x, so expect endpoints that exist but are undocumented, and
documented response shapes that differ. Example: 6.2.14 documents
`logNotifications`; 6.3.0.45 returns `alertNotifications` and
`eventNotifications`. Read the docs for the path and required parameters, then
confirm the shape against a real controller.

## The error codes make an undocumented surface enumerable

The internal API is undocumented by design, but the controller distinguishes its
failure modes, so candidate paths can be probed and the *code* read rather than
the payload:

| code | means |
|---|---|
| `-1600` | no such endpoint |
| `-1001`, `-1` | endpoint exists, arguments are wrong |
| `-44118` | endpoint exists, but not reachable with client-credentials auth |

Before concluding from a probe that an endpoint is broken, confirm the request
you think you sent is the one that went out.

## Traps that have already cost time

- **`logs/events` requires `filters.timeStart` and `filters.timeEnd`** in epoch
  milliseconds. Without them it answers `-1 General error`, which reads like a
  server fault rather than a missing argument.
- **An HTML body on a non-2xx response means the wrong address** — the web UI
  port, not the Interface Access Address. The UI answers every credentialed call
  with a 500 HTML page, identically for valid, wrong and absent credentials, so
  it looks exactly like an auth failure. `send` detects this and says so; keep
  that check if you refactor it.
- **Go's `flag` package stops at the first positional argument**, so
  `tool get <path> -query k=v` would parse *zero* flags. The parse-permute loop
  in `main.go` exists for that reason — it is not redundant. Removing it makes
  flags silently vanish after a subcommand, and the resulting server-side error
  sends you debugging the wrong machine.
- **The two surfaces disagree on paging parameters**: `page`/`pageSize` on the
  Open API, `currentPage`/`currentPageSize` on the internal one. `getAllPaged`
  takes them as arguments for that reason.
- **Not every Open API endpoint is under `/openapi/v1/`.** Some are v2. A path
  may name its own version.
- **`-compact` already exists.** Do not add a JSON-compaction dependency to
  anything that shells out to this tool; a consumer script once used `python3`
  for it needlessly.

## Why the web session is not cached, deliberately

The Open API token *is* cached to disk, because the token response carries
`expiresIn` — so a known expiry can be stored and checked before use.

The web login response carries **no expiry at all**: verified, it is only
`{errorCode, msg, result:{omadacId, token}}`. Caching it would mean guessing a
lifetime and adding stale-session detection plus a transparent re-login, which
is new code and a new failure mode to save one request. Measured cost of not
caching: **~130 ms per invocation**, and it is one login per *process*, not per
request — the session is reused within a run.

Revisit it for login *volume*, not latency: controllers have session limits and
may rate-limit logins. The missing piece for an implementation is the error code
a stale session returns, which is not known and cannot be guessed.

Also note the asymmetry in blast radius: the Open API token is scoped to an
application with a role, while the web session is a full user session.

## Verifying a change

There is no test suite. Verification is manual against a live controller, so
state plainly in a change description what was and was not exercised.

```sh
go vet ./... && go build -o /dev/null ./...
gofmt -l .
```

On **macOS 15+**, a binary that reaches a LAN address needs Local Network
permission, and Go's linker signs ad-hoc with the generic identifier `a.out`,
which TCC cannot hold a grant against. Re-sign with a stable identifier if you
run it from anything other than an interactive shell:

```sh
codesign --force --sign - --identifier com.example.omadactl ./omadactl
```

That is necessary but **not sufficient** for a scheduled job: a cron-launched
process still gets `connect: no route to host` because macOS will not register
the grant in that context. Run long-lived collectors from an interactive session
or on a non-macOS host.
