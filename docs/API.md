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
Sec-WebSocket-Protocol: tgproxy-relay.v2, binary
```

Relay выбирает один из server-side endpoints DC и прокидывает binary WebSocket messages в
TCP socket. `test=0` выбирает production topology, `test=1` — test topology. Параметр
`test` необязателен для обратной совместимости. Неизвестные и дублирующиеся query-параметры
отклоняются; передать `dst`/IP из клиента нельзя. Сервер предпочитает subprotocol
`tgproxy-relay.v2`, но принимает старый `binary`.

`websocket.path` по умолчанию равен `/apiws`. Если настроен другой path, сервер также
принимает `/apiws` как compatibility alias. Пути `/healthz`, `/version`, `/capabilities`, `/test-routes`,
`/connect` и `/admin/*` зарезервированы.

Android передаёт pseudonymous device ID, migration alias, identity version, manufacturer,
brand, canonical brand, raw model, marketing name, device/product codes, версии приложения и
Android в заголовках `X-TGProxy-*`. Эти поля используются только owner-overview. Relay не
принимает GPS-координаты.

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
  "version": "1.3.0",
  "protocol": 1,
  "minAppProtocol": 1,
  "ownerProtocol": 2,
  "instanceId": "ri_0123456789abcdef0123456789abcdef",
  "identityPersistent": true
}
```

Prefixed public endpoint: `GET /apiws/version`.

## Identity

```text
GET /identity
GET /apiws/identity
Authorization: Bearer <raw-client-token>
```

Ответ связывает проверенный client token со стабильной установкой Relay:

```json
{
  "instanceId": "ri_0123456789abcdef0123456789abcdef",
  "identityPersistent": true,
  "authenticatedTokenId": "tok_...",
  "name": "tgproxy-relay",
  "version": "1.3.0",
  "protocol": 1,
  "ownerProtocol": 2
}
```

`instanceId` не кодирует IP/домен и сохраняется в owner state. Если durability недоступна,
`identityPersistent=false`; Android в этом случае может использовать endpoint fallback, но не
должен считать временный ID доказательством общей установки.

## Capabilities

```text
GET /capabilities
```

Ответ содержит диапазон совместимых protocol, subprotocols, реально поддержанные features,
уровень диагностики и current topology revision/DC set. Пример и конфигурация signed
topology: [TOPOLOGY.md](TOPOLOGY.md).

## Route Test

```text
POST /test-routes
Content-Type: application/json
```

Тело запроса:

```json
{
  "dcs": [
    { "dc": 1 },
    { "dc": 2 },
    { "dc": 3 },
    { "dc": 4 },
    { "dc": 5 },
    { "dc": 203 }
  ]
}
```

Ответ plain text:

```text
DC1 main OK TCP_ONLY
DC1 media OK TCP_ONLY
...
DC203 main OK TCP_ONLY
DC203 media OK TCP_ONLY
```

Prefixed public endpoint: `POST /apiws/test-routes`.

Поле legacy `ip`, если его прислал старый Android, игнорируется. `/test-routes` выполняет
coarse TCP-проверку main и media endpoint pools из server-side topology. Android
дополнительно выполняет настоящий MTProto `req_pq/resPQ` для production main/media scopes;
это разные уровни проверки.

## Owner info и overview

```text
GET /admin/v1/info
Authorization: Bearer <owner-token>
```

`info` возвращает `instanceId`, `identityPersistent`, server version, `ownerProtocol` и
`publicUrl`. Prefixed вариант: `/apiws/admin/v1/info`.

Overview:

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
      "deviceId": "dev2_...",
      "identityVersion": "2",
      "manufacturer": "Xiaomi",
      "brand": "Xiaomi",
      "canonicalBrand": "Xiaomi",
      "model": "2407FPN8EG",
      "marketingName": "Xiaomi 14T Pro",
      "appVersion": "1.3.0",
      "appCode": "10300",
      "android": "11",
      "country": "Россия",
      "city": "Москва",
      "remoteIp": "203.0.113.10",
      "firstSeen": "2026-08-27T12:00:00Z",
      "lastSeen": "2026-08-27T12:05:00Z",
      "activeSessions": 1,
      "blocked": false
    }
  ]
}
```

`country` и `city` отсутствуют, если GeoIP отключён, адрес непубличный или lookup не удался.
Заблокированное устройство дополнительно получает `blockedAt`.

## Создать клиентский токен

```text
POST /admin/v1/tokens
Authorization: Bearer <owner-token>
Content-Type: application/json

{
  "name": "Телефон семьи",
  "secret": "tgpr_<client-generated-secret>",
  "idempotencyKey": "req_<stable-request-id>"
}
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
`secret` и `idempotencyKey` обязательны для идемпотентного protocol 2 запроса. Точный повтор
возвращает тот же token/secret и заголовок `Idempotency-Replayed: true`. Повтор с другим
secret/name получает conflict, а удалённый idempotent token не создаётся заново. Запрос только
с `name` остаётся совместимым с owner protocol 1, но не защищает от дубля после потерянного
ответа.

## Отозвать клиентский токен

```text
DELETE /admin/v1/tokens/<token-id>
Authorization: Bearer <owner-token>
```

Успех: `204 No Content`. Отзыв сначала атомарно сохраняется в state, затем Relay закрывает
все активные сессии токена. Prefixed вариант: `/apiws/admin/v1/tokens/<token-id>`.

## Управление устройством

```text
POST   /admin/v1/tokens/<token-id>/devices/<device-id>/disconnect
PUT    /admin/v1/tokens/<token-id>/devices/<device-id>/block
DELETE /admin/v1/tokens/<token-id>/devices/<device-id>/block
Authorization: Bearer <owner-token>
```

`disconnect` закрывает только текущие сессии устройства. `block` сначала атомарно сохраняет
запрет, затем закрывает его сессии; повторное подключение получает `403`. `unblock` не
отзывает token и разрешает устройству подключиться снова.

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
