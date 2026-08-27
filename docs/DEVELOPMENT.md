# Разработка TG Proxy VPS Relay

## Требования

- Go 1.22+;
- Git;
- Linux или другая ОС для unit tests; production artifacts предназначены для Linux.

## Получить код и проверить

```bash
git clone https://github.com/Dushnyj/TG-Proxy-Relay.git
cd TG-Proxy-Relay
go test ./...
go vet ./...
test -z "$(gofmt -l .)"
go build -trimpath -o tgproxy-relay ./cmd/tgproxy-relay
```

Проверить пример конфигурации:

```bash
./tgproxy-relay -config config.example.json -check-config
```

## Структура

| Путь | Назначение |
| --- | --- |
| `cmd/tgproxy-relay` | CLI и запуск сервера |
| `cmd/tgproxy-topology-sign` | offline генерация ключей и подпись topology bundle |
| `internal/config` | конфигурация, defaults и validation |
| `internal/relay` | HTTP/WebSocket, bridge, owner control plane, topology и health |
| `packaging` | service templates |
| `docs` | эксплуатационные контракты |

## Инварианты безопасности

- raw client/owner tokens не записываются в config/state/log;
- client и owner roles не могут использовать один secret;
- destination выбирается только из server-owned topology;
- private, loopback, link-local, CGNAT и reserved topology endpoints отклоняются;
- dynamic topology применяется только после Ed25519 validation и сохраняет atomic LKG;
- критичное изменение token state записывается durable до успешного ответа;
- блокировка/revoke закрывает активные сессии;
- limits применяются до создания неограниченного числа upstream connections;
- HTTP служебная проверка не называется MTProto proof.

## Тесты изменения

Добавляйте unit/integration test для:

- конфигурации и migration;
- auth role и token lifecycle;
- WebSocket framing/close/ping/pong;
- regular и media endpoint selection;
- cooldown, race и cancellation;
- owner device state;
- signed topology replay/downgrade/expiry;
- HTTP prefix и reverse-proxy contract.

Race detector рекомендуется для изменений concurrency:

```bash
go test -race ./...
```

## Тестовые данные

Используйте только `relay.example.com` и RFC 5737 IP. Не добавляйте production config,
`state.json`, реальные domains/IP, token hashes, raw secrets, SSH/TLS/signing keys или логи
пользователей.

## Перед pull request

1. `gofmt`, `go vet`, `go test` и build проходят.
2. Обновлены API/config/docs.
3. `git diff --check` чист.
4. Secret scan не нашёл данных.
5. Проверена совместимость с TG Proxy Android для protocol/capabilities.

См. [CONTRIBUTING.md](../CONTRIBUTING.md).
