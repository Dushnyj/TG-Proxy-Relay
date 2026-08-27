# Выпуск TG Proxy VPS Relay

## Перед тегом

1. Обновите `VERSION`.
2. Добавьте секцию той же версии в `CHANGELOG.md`.
3. Обновите `relay.Version` contract и Android compatibility docs, если менялись protocol или
   capabilities.
4. Запустите:

   ```bash
   test -z "$(gofmt -l .)"
   go vet ./...
   go test ./...
   go test -race ./...
   go build -trimpath ./cmd/tgproxy-relay
   go build -trimpath ./cmd/tgproxy-topology-sign
   ```

5. Убедитесь, что CI и secret scan зелёные.

## Создать релиз

```bash
git tag -a v1.2.0 -m "TG Proxy VPS Relay v1.2.0"
git push origin main
git push origin v1.2.0
```

`.github/workflows/release.yml` сверяет тег с `VERSION`, запускает тесты, а затем для каждой
Linux-архитектуры собирает static binary с:

```text
CGO_ENABLED=0
-trimpath
-ldflags="-s -w -X .../internal/relay.Version=<version>"
```

Каждый archive содержит binary, README, LICENSE, пример конфигурации, документацию и service
template. Publish job создаёт `SHA256SUMS.txt` и GitHub Release.

Workflow можно повторно запустить вручную для неизменённого существующего тега через
`workflow_dispatch`.

## Проверить assets

- опубликованы все 15 Linux archives;
- `SHA256SUMS.txt` содержит каждый archive;
- `linux-amd64` и `linux-arm64` распаковываются;
- `tgproxy-relay -version` выводит версию тега;
- `-config config.example.json -check-config` проходит;
- documentation/service template присутствуют;
- Android совместимой версии проходит health/version/capabilities/test-routes и MTProto proof.

## Не перемещать опубликованные теги

После публикации исправление выпускается новой patch-версией. Не заменяйте binary или commit под
старым тегом: это разрушает воспроизводимость SHA-256 и доверие auto-updater Android.
