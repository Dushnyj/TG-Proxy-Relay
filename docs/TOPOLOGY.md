# Telegram endpoint topology

Relay 1.2.0 поддерживает legacy `dcMap`, несколько endpoints на DC и опциональный
подписанный remote manifest. Android не передаёт произвольный destination: только
`dc/media/test`; адрес всегда выбирает Relay из проверенной topology.

## Статическая конфигурация

Legacy map остаётся bootstrap и migration path:

```json
"telegram": {
  "connectTimeoutMs": 7000,
  "idleTimeoutSec": 0,
  "dcMap": {
    "1": "149.154.175.50",
    "2": "149.154.167.51"
  }
}
```

Голый IPv4 получает port 443. Новая модель сохраняет несколько ports/address families и
роль endpoint:

```json
"telegram": {
  "connectTimeoutMs": 7000,
  "idleTimeoutSec": 0,
  "dcOptions": {
    "2": [
      {"address":"149.154.167.51:443","role":"regular","static":true},
      {"address":"[2001:67c:4e8:f002::a]:443","role":"regular"},
      {"address":"149.154.167.220:443","role":"media","tcpoOnly":true}
    ]
  },
  "testDcOptions": {
    "2": [
      {"address":"149.154.167.40:443","role":"regular"}
    ]
  }
}
```

Допустимые roles: `regular`, `media`, `cdn`. Максимум 32 DC и 32 endpoints на DC. Адрес
обязан быть public IP literal и иметь port 1–65535. Hostname не принимается, чтобы DNS
rebinding не превращал Relay в SSRF. Endpoint с `secret` отклоняется: raw Relay не реализует
этот Telegram transport и не должен молча игнорировать его.

## Подписанный manifest

### 1. Создать offline signing key

```bash
go run ./cmd/tgproxy-topology-sign \
  -generate-key-prefix ./tgproxy-topology
```

Создаются:

```text
tgproxy-topology.private   # хранить offline, mode 0600
tgproxy-topology.public    # base64 public key для Relay
```

### 2. Подготовить unsigned payload

```json
{
  "schema": 1,
  "generation": 42,
  "notBefore": "2026-08-27T00:00:00Z",
  "expiresAt": "2026-09-26T00:00:00Z",
  "production": {
    "1": [
      {"address":"149.154.175.50:443","role":"regular"}
    ],
    "2": [
      {"address":"149.154.167.51:443","role":"regular"},
      {"address":"149.154.167.220:443","role":"media"}
    ]
  },
  "test": {
    "2": [
      {"address":"149.154.167.40:443","role":"regular"}
    ]
  }
}
```

`generation` обязан строго возрастать. Время — RFC3339 UTC; окно действия не более 90 дней.

### 3. Подписать

```bash
go run ./cmd/tgproxy-topology-sign \
  -in topology-payload.json \
  -out topology-bundle.json \
  -key tgproxy-topology.private
```

### 4. Опубликовать bundle по HTTPS

URL должен возвращать сам JSON без redirect и иметь размер не больше 1 MiB. Не размещайте
private key на web/Relay server.

### 5. Настроить Relay

```json
"telegram": {
  "connectTimeoutMs": 7000,
  "idleTimeoutSec": 0,
  "dcMap": {
    "1": "149.154.175.50",
    "2": "149.154.167.51",
    "3": "149.154.175.100",
    "4": "149.154.167.91",
    "5": "149.154.171.5",
    "203": "91.105.192.100"
  },
  "topology": {
    "url": "https://updates.example.net/tgproxy/topology-bundle.json",
    "publicKey": "BASE64_ED25519_PUBLIC_KEY",
    "statePath": "/var/lib/tgproxy-relay/topology.json",
    "refreshIntervalSec": 900
  }
}
```

Проверьте:

```bash
tgproxy-relay -config /etc/tgproxy-relay/config.json -check-config
systemctl restart tgproxy-relay
journalctl -u tgproxy-relay -n 100 --no-pager
```

## Update semantics

```text
HTTPS fetch (no redirects, no environment proxy)
  -> DNS: every resolved address must be public
  -> JSON exact schema and size limits
  -> Ed25519 signature
  -> generation > current generation
  -> notBefore/expiry/validity window
  -> DC/endpoint/IP/port/role limits
  -> fsync temp 0600
  -> atomic rename + directory fsync
  -> in-memory publish
```

Corrupt, malicious, expired или replayed bundle не заменяет current snapshot. При outage
Relay продолжает использовать подписанный LKG, включая истёкший LKG как аварийный источник,
и пишет это в journal. Если LKG ещё не было, используется embedded owner bootstrap.

Конфигурация Relay обязана содержать хотя бы один production bootstrap endpoint: cold start
не должен зависеть только от доступности update source. Явный пустой `dcMap` допустим при
миграции на непустой `dcOptions`, но пустые одновременно `dcMap` и `dcOptions` отклоняются.

Нормальный refresh получает jitter; failure — exponential backoff. Запрос неизвестного DC
может инициировать один немедленный refresh, ограниченный общим lock и cooldown 30 секунд.

## Capability API

`GET /capabilities` возвращает protocol range, WebSocket subprotocols, feature list,
diagnostic level и текущие production/test DC:

```json
{
  "name": "tgproxy-relay",
  "protocol": {"min":1,"max":2},
  "websocketSubprotocols": ["tgproxy-relay.v2","binary"],
  "features": ["endpoint-fallback","media-endpoints","ipv4-ipv6","signed-topology-lkg"],
  "routeDiagnostics": "tcp-preflight-only-client-must-prove-mtproto",
  "topology": {
    "source": "signed-bundle:42",
    "dynamic": true,
    "revision": 42,
    "productionDcs": [1,2,3,4,5,203],
    "testDcs": [1,2,3]
  }
}
```

`dynamic=true` означает, что Relay сам обновляет signed topology. Android может передать
новый DC без APK static map; Relay делает on-demand refresh и либо находит endpoint, либо
возвращает `unknown dc`, после чего Android применяет обычный route fallback/cooldown.

## Security boundary

Отклоняются:

- localhost/unspecified/private/link-local/multicast;
- CGNAT `100.64.0.0/10`;
- documentation и benchmark networks;
- IPv6 documentation/local ranges;
- hostname endpoints и DNS rebinding;
- invalid/duplicate/oversized routes;
- unknown WebSocket query parameters и duplicate `dc/media/test`;
- lower/equal generation, неверная signature, слишком длинный validity window.

Таким образом подписанный источник может менять только ограниченный набор public Telegram
TCP destinations, а клиент не может использовать Relay как arbitrary TCP proxy.
