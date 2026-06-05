# Changelog

Все заметные изменения TG Proxy VPS Relay фиксируются в этом файле.

## [1.0.2] - 2026-06-05

### Исправлено

- В default `dcMap`, примеры конфигурации и документацию добавлен Telegram DC203 `91.105.192.100` для медиа/CDN-трафика.
- Android auto-setup может обновлять существующий Relay config до карты `1,2,3,4,5,203` без пересоздания токенов.

## [1.0.1] - 2026-06-05

### Исправлено

- Default `dcMap` переведен на raw Telegram MTProto DC IP для VPS Relay.
- `/test-routes` и примеры конфигурации теперь проверяют реальные Telegram DC, а не WebSocket endpoint для Direct WS.
- Документация ручной установки обновлена под актуальную карту DC.

## [1.0.0] - 2026-06-04

### Добавлено

- Первый отдельный релиз TG Proxy VPS Relay.
- Авторизованный WebSocket endpoint `/apiws` для TG Proxy Android.
- Авторизованные служебные endpoints: `/healthz`, `/version`, `/test-routes`.
- Хранение токенов как SHA-256 hash вместо raw-token.
- Пример JSON-конфига и hardened systemd unit.
- GitHub Actions release workflow с linux `amd64` и `arm64` tar.gz assets.
