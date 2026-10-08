# Mac console and public tunnel — 2026-10-08

The user authorized the specific Cloudflare hostname `myrcodex.19920621.xyz`
on 2026-10-08, superseding the earlier deferral for this Cloudflare deployment.
Public deployment of the custom Relay remains separate and deferred.

## Public entry point

- URL: <https://myrcodex.19920621.xyz/>
- Origin: the independent Mac gateway on `http://127.0.0.1:18892`.
- Named tunnel: `myrcodex-macmini`, ID `bd4795f1-9de8-4226-9141-9368031822cf`.
- DNS: proxied CNAME in the **19920621.xyz** zone, pointing at this tunnel.
- Connector: official Darwin ARM64 cloudflared **2026.10.0**, verified against
  the GitHub release asset SHA256
  `a2f79ff7b9420aa537d74af239f376da170bbabeb529aec416002adac6a72e70`.
- launchd label: `com.stormlegend.myrcodex-cloudflared`.
- Config and scoped credentials: `~/Library/Application Support/rcodex-go-preview/cloudflared/`.
  Credentials/config have mode `0600`; directory mode is `0700`.
- Protocol: HTTP/2; four registered connections. Local readiness endpoint:
  `http://127.0.0.1:20092/ready`. No inbound Mac port was exposed.
- Logs: `~/Library/Logs/rcodex-go-preview/cloudflared{,.error}.log`.

The public console shell is accessible without credentials. API requests still
require the gateway bearer token. Tests confirmed unauthenticated session access
returns 401 and authenticated responses specify `private, no-store`.
No Cloudflare Access login policy has been added. The account-wide Cloudflare
management token and origin certificate were not copied to the Mac; its connector
only received credentials for this specific tunnel.

The default Linux Cloudflare certificate belonged to another zone. The first CLI
DNS operation appended that zone name; its newly created record and unused tunnel
were removed. Provisioning was then completed through the existing dedicated
19920621.xyz API credential in the correct account. Existing tunnel/DNS records
were left intact.

Configuration follows Cloudflare's
[locally managed tunnel documentation](https://developers.cloudflare.com/tunnel/features/locally-managed-tunnels/configuration-file/).
The connector package is from the
[official cloudflared release](https://github.com/cloudflare/cloudflared/releases/tag/2026.10.0).

## Console changes

The console uses a macOS-inspired layout with a compact session sidebar,
restrained colors, system typography, a fixed composer and modal connection/new
conversation settings. It includes:

- Light/dark appearance, following system preference until explicitly changed.
- Search of loaded conversations and an off-canvas sidebar on narrow screens.
- Safe DOM-based rendering for headings, lists, inline code, code fences and
  HTTP(S) links; model text is never interpreted as HTML.
- Clipboard copy for code blocks; assistant/user messages have distinct layouts.
- Existing authentication, session history, SSE reconnect, cancellation,
  approvals, question answers and idempotent retry behavior retained.
- `/` serves the console directly, so the public hostname needs no path suffix.

The Mac desktop retains its local console launcher and adds **rcodex 远程入口.command**
and **rcodex 复制访问令牌.command**. The launcher supports an explicitly supplied HTTPS
origin; it passes the token-bearing URL to macOS through stdin, not process argv.

## Acceptance

- Go race tests and vet passed locally.
- Five Chromium regression scenarios passed, including auth/HTML injection,
  approvals/questions, cancellation/SSE cleanup, idempotency/path prefixes,
  Markdown/link safety, clipboard, search, themes and mobile navigation.
- A real Chromium browser loaded the public root over Cloudflare HTTPS, checked
  unauthenticated 401, authenticated, created a Codex session, and asked the real
  Mac runtime to read the isolated workspace README.
- The task completed; real SSE events arrived through the tunnel. History
  survived reload. Desktop light/dark and 390-pixel mobile layouts were inspected.
- Existing Mac services remained PID 40441 (18890) and PID 60233 (18891).
- Only the independent preview gateway was upgraded. Its database was backed up
  before the upgrade. Existing long-duration HTTP soaks were preserved.

This validates the requested tunnel and console change. The earlier production
gates for authenticated Claude, dedicated real bots, provider management and
attachment execution remain open; this change does not claim full parity.

## Published version and final deployment

Source commit: `2987334dfca643257c1ad143c13643b7585cd4ae`.
[Linux/macOS/browser CI](https://github.com/StormLegend/rcodex/actions/runs/37749325285)
and [release validation/build](https://github.com/StormLegend/rcodex/actions/runs/37749602066)
passed. [v0.2.11-rc.1](https://github.com/StormLegend/rcodex/releases/tag/v0.2.11-rc.1)
is published with five platform archives and SHA256SUMS.

The published Darwin ARM64 archive was verified against both its manifest and
GitHub asset digest:
`2e47b62cdf3614db213e56c3d231dfbe159dd1ae276a5d1827cc4eb121231e5a`.
The serving binary's SHA256 is
`f785f980b0531fd991eb804bad83a171b1b876b20944354f09962c1e2a01d374`.
It is installed in `~/Applications/rcodex-go-preview/releases/v0.2.11-rc.1-github`.
Before switching, the preview had no queued/running turns and its database was
backed up to `backups/pre-v0.2.11-rc.1-github.db`.

Final checks on the published artifact confirmed public root 200 with Cloudflare
response headers, unauthenticated API 401, authenticated history retained after
restart, SSE replay through the tunnel, and light/dark/mobile rendering with no
browser script errors. The existing Mac services remained PID 40441 and 60233;
the independent preview became PID 88576, and its Cloudflare connector stayed
PID 87917 with a passing readiness response. Both old Linux user services stayed
active. A native Mac Chrome screenshot was also inspected.

Receipts and screenshots are in `~/Applications/rcodex-go-preview/acceptance/`.
Temporary provisioning credentials were removed from the Linux test workspace;
only the intended scoped connector credential remains in the Mac service config.
