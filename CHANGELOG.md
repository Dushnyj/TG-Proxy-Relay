# Changelog

Все заметные изменения TG Proxy VPS Relay фиксируются в этом файле.

## [1.0.0] - 2026-06-04

### Добавлено

- Первый отдельный релиз TG Proxy VPS Relay.
- Авторизованный WebSocket endpoint `/apiws` для TG Proxy Android.
- Авторизованные служебные endpoints: `/healthz`, `/version`, `/test-routes`.
- Хранение токенов как SHA-256 hash вместо raw-token.
- Пример JSON-конфига и hardened systemd unit.
- GitHub Actions release workflow с linux `amd64` и `arm64` tar.gz assets.
