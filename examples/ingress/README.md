# Ingress Examples

These JSON files can be posted to the agent Ingress API or used as UI reference values.

Replace hosts, domains, DNS provider IDs, public IPs, and secrets before applying them.

Suggested order:

```sh
# Optional: create a DNS provider for automated DNS and ACME DNS-01.
curl -X POST http://127.0.0.1:57017/api/ingress/providers/dns \
  -H 'Content-Type: application/json' \
  -d @examples/ingress/dns-provider-cloudflare.json

# Create an ingress declaration.
curl -X POST http://127.0.0.1:57017/api/ingress \
  -H 'Content-Type: application/json' \
  -d @examples/ingress/cloudflare-nginx-tls.json
```

Manual DNS works without provider setup, but it cannot automate ACME DNS-01 certificates.
