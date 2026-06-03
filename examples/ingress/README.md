# Ingress Examples

These JSON files can be posted to the agent Ingress API or used as UI reference values.

Replace hosts, domains, DNS provider IDs, public IPs, and secrets before applying them.

Suggested order:

```sh
# Optional: create a DNS provider for automated DNS and ACME DNS-01.
curl -X POST http://127.0.0.1:57017/api/ingress/providers/dns \
  -H 'Content-Type: application/json' \
  -d @examples/ingress/dns-provider-cloudflare.json

# Create a managed certificate first, then issue it from Settings -> SSL Certificates
# or by POSTing /api/ingress/certs/{id}/issue.
curl -X POST http://127.0.0.1:57017/api/ingress/certs \
  -H 'Content-Type: application/json' \
  -d '{"domains":["go-http.example.com"],"issuer":"acme","dns_provider":"cloudflare-prod","auto_renew":true}'

# Create an ingress declaration after replacing tls.cert_id with the returned certificate ID.
curl -X POST http://127.0.0.1:57017/api/ingress \
  -H 'Content-Type: application/json' \
  -d @examples/ingress/cloudflare-nginx-tls.json
```

Manual DNS works without provider setup. HTTPS ingress declarations reference a managed certificate with `tls.cert_id`.
