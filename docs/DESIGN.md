# API 通知系统：设计与工程决策

## 1. 问题理解与成功标准

业务系统希望在本地关键事件完成后尽快返回，不同步等待外部供应商。通知服务负责接收一个目标 URL、HTTP 方法、Header 和 JSON Body，将请求持久化后异步投递，并在瞬时故障下自动重试。

MVP 的成功标准：

- 接口只有在通知已持久化后才返回 `202 Accepted`。
- 进程崩溃或重启不会丢失已受理通知。
- 网络错误、超时、`408`、`429`、`5xx` 能自动重试。
- Worker 崩溃造成的不确定结果会再次投递，语义明确为至少一次。
- 相同 `Idempotency-Key` 和相同请求返回同一通知；相同 key 不同请求返回冲突。
- 达到重试上限或遇到明确不可重试响应后保留失败记录，便于人工处理。
- 可以通过状态接口、日志和指标判断积压、成功与失败。

## 2. 系统边界

### 2.1 本系统解决

- HTTP(S) JSON 通知的可靠接收、持久化、调度和投递。
- 业务调用鉴权、输入限制、幂等受理。
- 有限并发、请求超时、退避重试、租约恢复和死信状态。
- 目标地址的协议、主机白名单和私网地址校验，降低 SSRF 风险。
- 不泄露供应商认证 Header 的状态查询和结构化日志。
- 健康检查、就绪检查和 Prometheus 指标。

### 2.2 本系统明确不解决

- **供应商侧业务幂等**：至少一次可能重复发送；接收方应使用 `X-Notification-ID` 去重。服务无法替供应商保证副作用只发生一次。
- **业务流程编排**：不处理通知间依赖、补偿事务、审批或工作流，这些属于业务系统。
- **任意二进制/表单负载**：MVP 接收任意 JSON 值并原样保存 JSON 语义；文件、流式数据和表单编码留待真实需求出现。
- **动态模板和供应商适配器**：调用方已经知道目标 Header/Body；提前抽象适配器会增加发布耦合。
- **管理后台和死信自动重放**：MVP 提供可查询状态和数据库留存，人工修复凭据或供应商故障后可在后续版本提供审计化重放 API。
- **跨地域容灾和严格顺序**：题目没有吞吐、顺序或 RPO/RTO 约束；第一版优先保证单区域内行为清晰。

## 3. 技术方案比较

### 3.1 采用：Go + PostgreSQL 数据库队列

Go 的 HTTP、并发、超时和优雅停机能力适合 I/O 密集 Worker，产物是单二进制，部署和资源成本低。PostgreSQL 同时保存幂等键、任务状态和调度时间；事务与 `FOR UPDATE SKIP LOCKED` 足以支撑 MVP 的多 Worker 竞争领取，不需要维护第二套基础设施。

代价是需要自行实现领取、租约、退避和指标，但这些正是本题需要说明和验证的可靠性核心。规模增长前数据库轮询会产生额外查询负载，因此通过索引、批量领取和可配置轮询控制成本。

### 3.2 未采用：NestJS + Redis/BullMQ

BullMQ 提供成熟的延迟队列和重试，开发速度快；但还需要决定 Redis AOF、故障恢复和幂等记录的持久化位置。若再加入 PostgreSQL，会引入跨存储一致性问题；只用 Redis 又弱化可查询历史与约束。对当前 MVP 是不必要的运维面。

### 3.3 未采用：Spring Boot + Quartz/PostgreSQL

企业生态、配置和可观测性成熟，但框架体量、启动成本和样板代码高于这个边界清晰的小服务。若团队以 Java 为统一技术栈，这会是合理选择；当前没有该组织约束。

### 3.4 未采用：Kafka/RabbitMQ

专业消息队列能提高吞吐、削峰和消费者扩展能力，但本系统仍需保存请求状态、幂等键和长期失败信息。第一版同时引入 Broker 和数据库，会把核心问题变成双写一致性与中间件运维，属于过度设计。

## 4. 总体架构

```text
Business System
      |
      | POST /v1/notifications + Idempotency-Key
      v
HTTP API --validate/authenticate--> PostgreSQL
                                      |
                                      | claim due rows with lease
                                      v
                                  Worker Pool
                                      |
                                      | bounded HTTP request
                                      v
                               External Supplier API

GET /v1/notifications/{id} <---- sanitized status
/healthz, /readyz, /metrics  <---- operations
```

API 与 Worker 位于同一进程但通过存储接口解耦。单实例部署最简单；多副本时 PostgreSQL 行锁保证同一时刻只有一个 Worker 领取任务。API 不在请求协程中直接调用供应商。

## 5. API 契约

所有 `/v1` 接口要求 `Authorization: Bearer <API_TOKEN>`。写接口还要求 1-128 字节可打印 ASCII 的 `Idempotency-Key`。

```http
POST /v1/notifications
Content-Type: application/json

{
  "url": "https://supplier.example/events",
  "method": "POST",
  "headers": {"Content-Type": "application/json", "Authorization": "Bearer secret"},
  "body": {"event": "subscription.paid", "contact_id": "c_123"}
}
```

只允许 `POST`、`PUT`、`PATCH`。成功返回 `202` 和通知 `id`、`status`、时间戳；幂等重放仍返回同一资源并设置 `Idempotent-Replayed: true`。相同 key 对应不同规范化请求返回 `409`。格式错误为 `400`，未认证为 `401`，过大为 `413`，不允许的目标为 `422`。

`GET /v1/notifications/{id}` 返回状态、尝试次数、下次尝试、最后一次脱敏错误、外部状态码和时间戳；不返回目标 Header 或 Body。未知 ID 返回 `404`。

每次外部请求添加稳定的 `X-Notification-ID`，供应商可据此去重。调用方不能覆盖该 Header、`Host`、`Content-Length`、`Connection` 等逐跳或传输控制 Header。

## 6. 数据模型与状态机

`notifications` 保存 UUID、幂等键、规范化请求 SHA-256、目标 URL/method、JSONB headers/body、状态、尝试次数、下次执行时间、租约、最后错误/状态码和各时间戳。幂等键唯一；`(status, next_attempt_at)` 建部分索引。

```text
pending --claim--> processing --2xx--> delivered
   ^                    |
   |                    +--retryable/max not reached--> pending
   |                    +--permanent/max reached------> dead
   +------ lease expired after worker crash ------------+
```

领取在一个短事务内使用 `FOR UPDATE SKIP LOCKED` 选择到期的 `pending` 行，或租约已过期的 `processing` 行，然后写入 `processing`、`lease_until` 并增加 `attempt_count`。外部网络调用绝不持有数据库事务。

若 Worker 在供应商完成副作用之后、写入 `delivered` 之前崩溃，租约到期后会重复发送。这是至少一次语义的必然窗口，也是要求供应商按通知 ID 幂等的原因。

## 7. 失败分类与重试

- 成功：任意 `2xx`。
- 可重试：DNS/连接/TLS/读取错误、客户端超时、`408`、`425`、`429`、`5xx`。
- 不可重试：其余 `3xx`/`4xx`；禁用自动重定向，避免绕过目标校验。
- 默认最多 8 次；退避为 `min(5s * 2^(attempt-1), 15m)` 加 0-25% 抖动。
- 合法 `Retry-After` 比本地退避更晚时采用它，但最大不超过 24 小时。
- 单次请求默认 10 秒；响应体只做有界读取后丢弃，仅保存分类错误和状态码，避免供应商响应泄密。
- 达到上限进入 `dead`，不静默丢弃；状态和指标可用于告警及后续人工重放。

关停时停止领取新任务，等待在途请求到配置的优雅停机期限。若超时退出，租约最终使任务重新可见。

## 8. 安全设计

- 入口使用固定时间比较 Bearer Token；生产环境缺少 Token 时启动失败。
- JSON 请求体和 Header 数量/长度有上限；拒绝未知字段，避免无意接受拼写错误。
- 仅允许 HTTP(S)，默认要求 HTTPS；开发模式可显式允许 HTTP。
- 可配置精确域名或 `*.example.com` 白名单；每次投递都解析 DNS。
- 默认拒绝 loopback、私网、链路本地、组播、未指定及保留 IP；自定义 Dialer 只连接本次校验过的地址，降低 DNS rebinding。
- 禁止重定向，防止合法公网地址跳转至内网。
- 供应商密钥因投递需要会存于数据库；MVP 依赖数据库访问控制、磁盘加密和备份加密。应用层密钥管理在多租户或合规要求出现时演进。
- 日志、状态接口和指标不记录 Header/Body 或 URL 查询参数。

测试可显式设置 `ALLOW_PRIVATE_TARGETS=true` 访问本地 `httptest`；该配置在文档中标为仅开发使用。

## 9. 可观测性与运维

- JSON 日志包含 notification_id、attempt、outcome、duration 和外部状态码，不包含机密负载。
- `/healthz` 只表示进程存活；`/readyz` 在数据库不可用时失败。
- `/metrics` 暴露受理数、投递结果、尝试耗时、当前 pending/processing/dead 数量。
- 数据库迁移在启动时执行受版本控制的 SQL；多个副本使用 advisory lock 串行迁移。

## 10. 测试策略

- 纯单元测试：请求校验、目标策略、失败分类、退避和 `Retry-After`。
- HTTP Handler 测试：鉴权、幂等重放/冲突、错误映射和响应脱敏。
- PostgreSQL 集成测试：迁移、并发领取互斥、租约恢复、状态迁移。
- 端到端测试：真实 PostgreSQL + 本地供应商服务器，验证受理后异步成功、瞬时 `500` 后重试、永久 `400` 进入 dead。
- `go test -race ./...` 检查并发问题；Docker Compose smoke 验证打包、迁移、API 和 Worker 接线。

## 11. 演进路线

1. 首先通过索引、批量领取、连接池、Worker 并发与分表/归档扩大单库容量。
2. 若数据库调度查询成为瓶颈，引入 PostgreSQL `LISTEN/NOTIFY` 只做唤醒，数据库仍是事实源。
3. 需要更高吞吐时采用 transactional outbox + Kafka/RabbitMQ；投递状态仍落库，避免数据库与 Broker 裸双写。
4. 增加租户、目标配置/密钥引用、速率限制、熔断、审计化死信重放和供应商健康隔离。
5. 有明确顺序要求时按业务聚合键分区串行消费，而不是全局串行。

这些能力只有在指标证明瓶颈或业务提出约束后加入，避免第一版提前承担分布式系统复杂度。
