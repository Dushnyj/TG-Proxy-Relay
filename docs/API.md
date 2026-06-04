# Relay API

Все endpoints требуют:

```text
Authorization: Bearer <raw-token>
```

## WebSocket

```text
GET /apiws?dc=<dc>&media=<0|1>
Upgrade: websocket
Sec-WebSocket-Protocol: binary
```

Relay открывает TCP-соединение к выбранному Telegram DC и прокидывает binary WebSocket frames в TCP socket.

## Health

```text
GET /healthz
```

Ответ:

```text
ok
```

Если Relay опубликован под `/apiws`, reverse proxy должен отдавать:

```text
GET /apiws/healthz
```

## Version

```text
GET /version
```

Ответ:

```json
{
  "name": "tgproxy-relay",
  "version": "1.0.0",
  "protocol": 1,
  "minAppProtocol": 1
}
```

Если Relay опубликован под `/apiws`, reverse proxy должен отдавать:

```text
GET /apiws/version
```

## Route Test

```text
POST /test-routes
Content-Type: application/json
```

Тело запроса:

```json
{
  "dcs": [
    { "dc": 2, "ip": "149.154.167.220" },
    { "dc": 4, "ip": "149.154.167.220" }
  ]
}
```

Ответ plain text:

```text
DC2 main OK
DC2 media OK
DC4 main OK
DC4 media OK
```

Если Relay опубликован под `/apiws`, reverse proxy должен отдавать:

```text
POST /apiws/test-routes
```
