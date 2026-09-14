# AGENTS.md

## Build & Run
- Build: `bash build.sh` (produces `output/bin/psych.post` and `output/bootstrap.sh`)
- Config: set `CONFIG_PATH` env or defaults to `etc/config.yaml`; test config at `etc/[test]config.yaml`; local overrides at `etc/[local]config.txt`
- Docker build: CGO_ENABLED=0 static build (no compiler toolchain needed)
- Stopwords path: `STOPWORDS_PATH` env or defaults to `etc/stopwords_full.txt`
- No Makefile, no lint/typecheck commands defined in the repo

## Relationship with psych-core-api
- **This service does NOT call psych-core-api via HTTP or RPC.** Communication is entirely asynchronous.
- psych-core-api is a WebSocket chat server. When a user's conversation session ends (or at end-of-day), psych-core-api publishes a `PostNotify` JSON message to RabbitMQ.
- psych-post consumes `PostNotify` from RabbitMQ, fetches the full conversation history from the **shared MongoDB database**, generates a psychological report via Coze LLM, and writes results back to MongoDB.
- **Shared MongoDB database**: both services use the same database (`psych_core_api_test` in config). Collections shared: `user`, `message`, `conversation`, `config`, `prompt`. Collections written by psych-post: `report`, `alarm`.
- The `PostNotify` struct is defined locally in `pkg/core/post.go` (the contract between the two services). `pkg/core/protocol.go` and `pkg/core/engine.go` contain WebSocket protocol types that psych-post inherited from the shared codebase but does not use at runtime.
- `psych-idl` is in `go.mod` as a dependency but is not imported by any Go source file yet (protobuf definitions shared between services).

## Message flow
```
psych-core-api (WebSocket chat)  →  RabbitMQ exchange: psych_his_test  →  queue: psych-his-test  →  psych-post consumes  →  Coze LLM  →  MongoDB (report+alarm)
```
- Queue config in `etc/config.yaml` under `RabbitMQ` (URL, Queue, Exchange, RoutingKey)
- Consumer prefetch=1, auto-ack=false, nack-requeue=true — messages are NOT lost on failure

## Architecture
- This is **not an HTTP server** — it's a RabbitMQ consumer that generates psychological reports via Coze LLM. Port 8080 only serves `/healthz`.
- Entrypoint: `main.go`. `Init()` panics if RabbitMQ connection fails. `application.Init()` wires Redis, MongoDB mappers, history manager, word cloud extractor, and RabbitMQ connection manager.
- Message format: `core.PostNotify` (JSON via `bytedance/sonic`, not `encoding/json`)

## Key package layout
- `biz/domain/report/report.go` — core report generation pipeline (message → config lookup → LLM call → MongoDB write). Single file.
- `biz/domain/prompt/mgr.go` — prompt template manager (singleton `prompt.Mgr`); fetches Go-template prompts from MongoDB `prompt` collection with Redis cache. Mirrors psych-core-api's prompt domain.
- `biz/domain/his/` — daily message history with Redis cache (6h TTL, per-user per-day); global singleton `his.Mgr`
- `biz/infra/mapper/` — MongoDB data access (generic `IMongoMapper[T]` using go-zero `monc`). Includes `prompt` mapper for the `prompt` collection.
- `biz/application/base.go` — manual DI wiring (wire is a dep but not codegen'd)
- `pkg/app/` — chat app abstraction with provider registry pattern (`init()` registers factories)
- `pkg/mq/` — RabbitMQ connection manager with auto-reconnect (exponential backoff, 180s sleep after 5 failures)
- `pkg/core/` — shared types: `PostNotify` (the RabbitMQ message contract), WebSocket protocol types, engine interfaces

## Report generation pipeline (ordered)
The full flow in `report.DoConsume`:
1. Parse `PostNotify` from AMQP delivery
2. Extract userId/unitId/session from message; convert hex strings to ObjectIDs
3. Fetch daily messages from Redis cache (fallback: MongoDB conversations→messages)
4. Count conversation rounds
5. Insert initial Report with status `Processing`
6. Fetch unit config from MongoDB (contains LLM provider + appId)
7. Build Coze ChatApp via provider factory
8. Build prompt: user info (name, grade, class, gender) + full message transcript; also attempts to load Go-template prompts from `prompt` collection (`Report` type, post-stage) as system message
9. Call Coze LLM synchronously (`Generate`, not stream)
10. Parse model output (JSON, possibly wrapped in ``` fences)
11. Update report with generated fields + status `Success`
12. Update conversation title in MongoDB
13. If `needAlarm`, create alarm record (status `Pending`)

## Important conventions
- JSON: always use `github.com/bytedance/sonic` (not `encoding/json`)
- MongoDB: uses driver `v2` (`go.mongodb.org/mongo-driver/v2`), not v1
- ObjectID: `bson.ObjectIDFromHex()` from mongo-driver v2
- LLM providers register via `init()` — check `biz/infra/llm/chat_model.go` and the `_` import in `biz/domain/report/report.go`
- Logging: use `pkg/logs` package (wraps `hlog` from Hertz, not stdlib `log` or `slog`)
- Errors: use `pkg/errorx.New(code)` with predefined error codes from `type/errno/`
- Message reversal: consumer code reverses message order (stored newest-first, needs chronological for LLM). Coze client also calls `reverse()` internally in `chat_model.go`.

## Config selection
- `etc/config.yaml` — production-like default
- `etc/[test]config.yaml` — test env (Redis DB:1 vs DB:0)
- `etc/[local]config.txt` — local development overrides
- Set `CONFIG_PATH` to the desired file before starting

## Dependencies
- Redis (message cache), MongoDB (persistence, shared with psych-core-api), RabbitMQ (message bus), Coze API (LLM)
- OpenTelemetry with B3 propagation + Jaeger exporter

## CI/CD
- GitHub Actions: `.github/workflows/upgrade.yml`
- On tag push (`v*.*.*`) or push to `main`: builds Docker image, pushes to `xhpolaris/psych-post`, auto-deploys to K8s
- On PR to `main`: builds Docker image only (no push, no deploy)
- K8s deploy: patches the existing deployment image tag via `kubectl`

## Testing
- No test files exist in the repository (`*_test.go` returns empty)
