# SuperDev Pipeline Examples

These examples validate builtin pipeline templates for build plus systemd deployment.

| Example | Template | Port | Runtime |
| --- | --- | --- | --- |
| `go-http` | `go-binary-build` | 18080 | Go binary |
| `node-http` | `node-standard-build` | 18081 | Node |
| `python-http` | `python-standard-build` | 18082 | Python |
| `java-springboot` | `java-maven-build` | 18083 | Java Spring Boot |
| `rust-http` | `rust-cargo-build` | 18084 | Rust binary |
| `php-http` | `php-standard-build` | 18085 | PHP built-in server |
| `vue-go-combined` | `vue-go-combined-build` | 18086 | Go serving Vue dist |
| `mcp-log-lab` | MCP runtime/log diagnostics fixture | 18190 | Go command services |

Nginx, DNS, and Docker are outside this example matrix.

Run unit validation:

```sh
cd agent
go test ./...
```

Run real local-02 E2E:

```sh
export SUPERDEV_E2E_LOCAL02_HOST=100.90.99.61
export SUPERDEV_E2E_LOCAL02_USER=root
export SUPERDEV_E2E_LOCAL02_KEY="$HOME/.ssh/id_ed25519"
./agent/scripts/e2e-local02.sh
```

Cleanup is explicit:

```sh
./agent/scripts/e2e-local02-cleanup.sh
```
