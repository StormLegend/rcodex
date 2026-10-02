# Cloudflare Tunnel adapter

Cloudflare Tunnel is an optional external HTTPS ingress. It does not replace
Relay: Relay is the private gateway/client transport, while Cloudflare Tunnel
is useful for a normal HTTPS hostname, browser access, webhooks and Cloudflare
Access policies. Both can run at the same time against the loopback gateway.

The tunnel should target the gateway's loopback listener, for example
`http://127.0.0.1:18890` on the Mac mini. Keep the rcodex Bearer token enabled;
Cloudflare Access provides an additional edge identity layer and does not
replace the gateway's application authorization.

1. Create a named tunnel and DNS hostname in Cloudflare Zero Trust.
2. Copy `config.yml.example` to the platform path and replace the tunnel UUID,
   credentials path and hostname.
3. Validate the ingress configuration:

   ```bash
   cloudflared tunnel ingress validate \
     --config "$HOME/Library/Application Support/rcodex-go/cloudflared/config.yml"
   ```

4. Install the platform service template in this directory. macOS uses
   `launchctl`; Linux uses systemd. Do not run a second copy under the rcodex
   process.
5. Verify the tunnel metrics and Cloudflare dashboard, then test the public
   URL with the rcodex Bearer token.

For production, prefer a named tunnel with `no-autoupdate: true` and upgrade
`cloudflared` through the normal release process. Do not use `cloudflared
tunnel --url` quick tunnels for the production hostname.
