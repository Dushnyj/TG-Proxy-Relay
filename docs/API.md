# Relay API

Клиентские WebSocket и служебные endpoints требуют:

```text
Authorization: Bearer <raw-client-token>
```

Owner endpoints требуют отдельный `Authorization: Bearer <owner-token>`. Owner-token
не принимается как клиентский, а клиентский token не принимается Owner API.

## WebSocket

```text
GET <websocket.path>?dc=<dc>&media=<0|1>&test=<0|1>
Upgrade: websocket
Sec-WebSocket-Protocol: binary
```

Relay открывает TCP-соединение к выбранному Telegram DC и прокидывает binary WebSocket
messages в TCP socket. `test=0` выбирает `telegram.dcMap`, `test=1` —
`telegram.testDcMap`. Для test DC допустимы 1–3. Параметр `test` необязателен для
обратной совместимости, по умолчанию равен `0` и при наличии принимает только `0` или `1`.

`websocket.path` по умолчанию равен `/apiws`. Если настроен другой path, сервер также
принимает `/apiws` как compatibility alias. Пути `/healthz`, `/version`, `/test-routes`,
`/connect` и `/admin/*` зарезервированы.

Android передаёт идентификатор устройства, производителя, модель, версии приложения и
Android в заголовках `X-TGProxy-*`. Эти поля используются только owner-overview.

## Health

```text
GET /healthz
```

Ответ: `ok`. Если Relay опубликован под `/apiws`, reverse proxy должен отдавать
`GET /apiws/healthz`.

## Version

```text
GET /version
```

Ответ:

```json
{
  "name": "tgproxy-relay",
  "version": "1.1.0",
  "protocol": 1,
  "minAppProtocol": 1,
  "ownerProtocol": 1
}
```

Prefixed public endpoint: `GET /apiws/version`.

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
...
DC203 main OK
DC203 media OK
```

Prefixed public endpoint: `POST /apiws/test-routes`.

`/test-routes` выполняет coarse TCP-проверку production DC из серверной карты. Android
дополнительно выполняет настоящий MTProto `req_pq/resPQ` для production main/media scopes;
это разные уровни проверки.

## Owner overview

```text
GET /admin/v1/overview
Authorization: Bearer <owner-token>
```

Тот же endpoint доступен под WebSocket prefix, например
`GET /apiws/admin/v1/overview`. Ответ не содержит raw-токены:

```json
{
  "tokens": [
    {
      "id": "primary",
      "name": "Основной",
      "createdAt": "2026-08-27T12:00:00Z",
      "activeDevices": 1,
      "knownDevices": 2
    }
  ],
  "clients": [
    {
      "tokenId": "primary",
      "deviceId": "device_...",
      "manufacturer": "Xiaomi",
      "model": "Redmi Note 8 Pro",
      "appVersion": "1.1.0",
      "appCode": "10100",
      "android": "11",
      "country": "Россия",
      "city": "Москва",
      "remoteIp": "203.0.113.10",
      "firstSeen": "2026-08-27T12:00:00Z",
      "lastSeen": "2026-08-27T12:05:00Z",
      "activeSessions": 1
    }
  ]
}
```

`country` и `city` отсутствуют, если GeoIP отключён, адрес непубличный или lookup не удался.

## Создать клиентский токен

```text
POST /admin/v1/tokens
Authorization: Bearer <owner-token>
Content-Type: application/json

{"name":"Телефон семьи"}
```

Ответ `201 Created`:

```json
{
  "token": {
    "id": "tok_...",
    "name": "Телефон семьи",
    "createdAt": "2026-08-27T12:00:00Z",
    "activeDevices": 0,
    "knownDevices": 0
  },
  "secret": "tgpr_..."
}
```

Поле `secret` возвращается только при создании. Relay сохраняет только SHA-256 hash.

## Отозвать клиентский токен

```text
DELETE /admin/v1/tokens/<token-id>
Authorization: Bearer <owner-token>
```

Успех: `204 No Content`. Отзыв сначала атомарно сохраняется в state, затем Relay закрывает
все активные сессии токена. Prefixed вариант: `/apiws/admin/v1/tokens/<token-id>`.

## Landing page подключения

```text
GET /connect
GET /apiws/connect
```

Авторизация не требуется. Рекомендуемая ссылка:

```text
https://relay.example.com/apiws/connect#data=<url-safe-base64-payload>
```

Payload находится после `#`, поэтому браузер не отправляет его Relay, nginx или журналам
доступа. Страница проверяет алфавит и длину, затем открывает `tgproxy://import?data=...`.
Query `?data=` принимается только для совместимости со старыми ссылками.
