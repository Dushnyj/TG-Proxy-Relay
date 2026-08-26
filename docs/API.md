# Relay API

Все endpoints требуют:

```text
Authorization: Bearer <raw-token>
```

## WebSocket

```text
GET <websocket.path>?dc=<dc>&media=<0|1>&test=<0|1>
Upgrade: websocket
Sec-WebSocket-Protocol: binary
```

Relay открывает TCP-соединение к выбранному Telegram DC и прокидывает binary WebSocket messages в TCP socket. `test=0` выбирает `telegram.dcMap`, `test=1` — `telegram.testDcMap`. Для test DC допустимы 1-3. Параметр `test` необязателен для обратной совместимости, по умолчанию равен `0` и при наличии принимает только `0` или `1`.

`websocket.path` по умолчанию равен `/apiws`. Если настроен другой path, сервер также принимает `/apiws` как compatibility alias. Управляющие пути `/healthz`, `/version` и `/test-routes` зарезервированы.

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
  "version": "1.0.5",
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
    { "dc": 1, "ip": "149.154.175.50" },
    { "dc": 2, "ip": "149.154.167.51" },
    { "dc": 3, "ip": "149.154.175.100" },
    { "dc": 4, "ip": "149.154.167.91" },
    { "dc": 5, "ip": "149.154.171.5" },
    { "dc": 203, "ip": "91.105.192.100" }
  ]
}
```

Ответ plain text:

```text
DC1 main OK
DC1 media OK
DC2 main OK
DC2 media OK
DC3 main OK
DC3 media OK
DC4 main OK
DC4 media OK
DC5 main OK
DC5 media OK
DC203 main OK
DC203 media OK
```

Если Relay опубликован под `/apiws`, reverse proxy должен отдавать:

```text
POST /apiws/test-routes
```
