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

Ingress examples live in `examples/ingress`. They cover DNS provider configs and nginx ingress declarations; they are not pipeline templates.

Docker is outside this example matrix.

## Code Debug Config Shape

Local command deployments can opt into AI code debug:

```yaml
code_debug:
  enabled: true
  provider: go
  mode: launch
  program: .
  working_dir: .
  stop_on_entry: false
```

Go uses `dlv dap`; Python uses `debugpy.adapter`; Node is experimental and requires `adapter_command` in this release.

Use code debug only after the log-driven path is exhausted. The default AI entry points are the composite tools `debug_capture_at` and `debug_inspect`; low-level stepping tools are escape hatches.

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
