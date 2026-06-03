# SuperDev Sample

This project is copied into your local SuperDev data directory during first startup. It contains one local managed service named `sample-api`.

The service emits structured logs every second and occasionally writes WARN and ERROR lines so SuperDev MCP can demonstrate log inspection and diagnosis.

The `demo` environment is intentionally not marked as a dev environment. Restarting the deployment through MCP triggers a SuperDev safety approval before the AI can operate it.
