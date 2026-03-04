# null-core

null-core is a high-performance gRPC (or rather [connect-go](https://github.com/connectrpc/connect-go)) API built in Go that handles core financial operations including user management, account management, transaction processing, and categorization, among other things.

## ⚙️ config

### environment variables

| variable                  | description                                | default              | required?  |
|---------------------------|--------------------------------------------|----------------------|------------|
| `API_KEY`                 | Authentication key for gRPC API access     |                      | [x]        |
| `DATABASE_URL`            | PostgreSQL connection string               |                      | [x]        |
| `NULL_GATEWAY_URL`        | URL for null-gateway (auth + proxy)        |                      | [x]        |
| `NULL_RECEIPTS_URL`       | gRPC endpoint for receipt parsing service  |                      | [x]        |
| `EXCHANGE_API_URL`        | Exchange rate API endpoint                 |                      | [x]        |
| `LISTEN_ADDRESS`          | Server listen address (port or host:port)  | `127.0.0.1:55555`    | [ ]        |
| `LOG_LEVEL`               | Log level: debug, info, warn, error        | `info`               | [ ]        |
| `LOG_FORMAT`              | Log format: json, text                     | `text`               | [ ]        |

## 🌱 ecosystem

- [null-core](https://github.com/xhos/null-core) - main backend service (this repo)
- [null-web](https://github.com/xhos/null-web) - frontend web application
- [null-mobile](https://github.com/xhos/null-mobile) - mobile appplication
- [null-protos](https://github.com/xhos/null-protos) - shared protobuf definitions
- [null-receipts](https://github.com/xhos/null-receipts) - receipt parsing microservice
- [null-email-parser](https://github.com/xhos/null-email-parser) - email parsing service


null-web is the expected frontend to use, but it is possible to build your own client. The only thing tightly coupled is the Better Auth JWT authentication mechanism, but you can use inter-service API keys to authenticate instead if you prefer.
