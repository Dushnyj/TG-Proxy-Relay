# Участие в разработке TG Proxy VPS Relay

## Проверка

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go build -trimpath ./cmd/tgproxy-relay
go build -trimpath ./cmd/tgproxy-topology-sign
```

Для изменений concurrency дополнительно запускайте `go test -race ./...`.

## Требования

- один PR решает одну связанную проблему;
- новый API/config field документирован и проверен;
- migration обратно совместима;
- auth, topology, resource limits и durable state не ослаблены;
- Android compatibility явно проверена при изменении protocol/capabilities;
- примеры используют только `example.com` и RFC 5737 IP;
- в истории нет raw tokens, hashes production, real VPS, configs, state, logs или private keys.

## Коммиты

Примеры:

```text
fix: preserve token state before session close
docs: explain media route diagnostics
```

Подробности окружения: [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md).
