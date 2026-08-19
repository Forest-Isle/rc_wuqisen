# Reliable Notification Service

一个面向企业内部业务系统的可靠 HTTP(S) 通知 MVP。API 在 PostgreSQL 完成持久化后立即返回，后台 Worker 使用租约、指数退避和死信状态执行至少一次投递。

本作业提交仓库名为 `rc_wuqisen`。

问题理解、系统边界、架构和技术取舍见 [设计文档](docs/DESIGN.md)，AI 参与边界见 [AI 使用说明](docs/AI_USAGE.md)。

## 架构

```text
Business API -> POST /v1/notifications -> PostgreSQL durable queue
                                                |
                                         SKIP LOCKED + lease
                                                v
                                           Worker pool -> Supplier API
```

PostgreSQL 是唯一事实源，避免数据库与消息队列双写。进程若在供应商成功后、写入成功状态前崩溃，通知会重复发送；因此语义是至少一次，供应商应按 `X-Notification-ID` 去重。

## 一条命令启动

需要 Docker Compose：

```bash
docker compose up -d --build --wait
```

Compose 同时启动 PostgreSQL、服务和仅用于本地验证的 mock vendor。开发 Token 是 `dev-only-token`。

提交成功通知：

```bash
curl -i http://localhost:8080/v1/notifications \
  -H 'Authorization: Bearer dev-only-token' \
  -H 'Idempotency-Key: signup-1001' \
  -H 'Content-Type: application/json' \
  -d '{"url":"http://mockvendor:8081/success","method":"POST","headers":{"Content-Type":"application/json"},"body":{"event":"user.registered","user_id":"1001"}}'
```

响应中的 `id` 可查询：

```bash
curl http://localhost:8080/v1/notifications/REPLACE_ID \
  -H 'Authorization: Bearer dev-only-token'
```

同一 `Idempotency-Key` 与相同请求返回同一 ID 和 `Idempotent-Replayed: true`；改变请求则返回 `409`。`/flaky` 首次返回 500 后成功，`/permanent` 返回 400 并进入 `dead`。

运维端点：`/healthz`、`/readyz`、`/metrics`。停止并清理本项目资源：

```bash
docker compose down -v
```

## API

- `POST /v1/notifications`：要求 Bearer Token、`Idempotency-Key`，支持 POST/PUT/PATCH 目标和任意 JSON Body，返回 `202`。
- `GET /v1/notifications/{id}`：返回脱敏状态，不返回目标 Header 或 Body。
- 状态：`pending`、`processing`、`delivered`、`dead`。

网络错误、超时、408、425、429 和 5xx 重试；其他 3xx/4xx 直接 dead。默认最多 8 次，5 秒指数退避，最大 15 分钟，并支持有上限的 `Retry-After`。

## 本地开发与测试

```bash
gofmt -w cmd internal test
go test ./...
go test -race ./...
go vet ./...
```

PostgreSQL 集成/E2E 测试要求 `TEST_DATABASE_URL`；未设置时会明确 skip：

```bash
docker compose up -d postgres
TEST_DATABASE_URL='postgres://notify:notify@localhost:5432/notify?sslmode=disable' go test -race ./...
```

## 关键配置

见 [.env.example](.env.example)。生产必须设置 `DATABASE_URL`、高熵 `API_TOKEN` 和合适的 `DESTINATION_ALLOWLIST`。`ALLOW_HTTP=true`、`ALLOW_PRIVATE_TARGETS=true` 仅用于本地 Compose；生产默认只允许 HTTPS 和公网地址。数据库包含投递所需的供应商 Header，应启用最小权限、磁盘和备份加密。

当前 MVP 不包含管理后台、自动死信重放、动态供应商模板、二进制/表单负载、全局顺序或跨地域容灾。理由及演进条件记录在设计文档中。
