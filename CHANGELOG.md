# Changelog

Все заметные изменения TG Proxy VPS Relay фиксируются в этом файле.

## [1.0.5] - 2026-08-26

### Исправлено

- Heartbeat принимает только Pong с точным 8-байтовым nonce последнего Ping; посторонний или устаревший Pong больше не скрывает зависший канал.
- При штатном EOF Telegram Relay отправляет клиенту WebSocket Close `1000`, а при ошибке чтения — `1011`, прежде чем закрыть TCP.
- Добавлены интеграционные тесты неверного Pong и корректного закрытия Telegram-направления.

## [1.0.4] - 2026-08-26

### Надёжность транспорта

- Добавлен серверный WebSocket ping/pong с отдельными таймаутами liveness и записи.
- Binary messages корректно собираются из continuation frames; control frames обрабатываются между фрагментами.
- Ограничен размер WebSocket message до настраиваемых 16 MiB до выделения памяти.
- Protocol, UTF-8 и size ошибки завершаются корректными WebSocket close codes `1002`, `1007` и `1009`; malformed/non-minimal frames отклоняются.
- Application idle больше не закрывает рабочий туннель по умолчанию; зависший клиент обнаруживает heartbeat.
- Upgrade принимает только согласованный binary subprotocol; HTTP server ограничивает header timeout/size и idle time.
- Telegram TCP-соединения используют keepalive, TCP_NODELAY и увеличенные буферы для длинных media-потоков.
- `/test-routes` возвращает HTTP 502 при любом нерабочем DC, чтобы Android не сохранял частично исправный Relay.
- WebSocket path настраивается через `websocket.path`; при custom path `/apiws` остаётся compatibility alias для уже установленных клиентов.
- Добавлены Telegram test DC 1-3 и query `test=0|1`; production и test адреса берутся из разных проверяемых map.
- `/test-routes` имеет общий 15-секундный deadline и отменяет оставшиеся проверки после timeout.
- Добавлены `-version` и `-check-config` для безопасной установки, проверки и rollback обновлений.
- Документация reverse proxy фиксирует долгие WebSocket timeout и отключение buffering.

## [1.0.3] - 2026-06-30

### Безопасность и стабильность

- `/test-routes` больше не использует IP-адреса из запроса Android-клиента; проверяются только Telegram DC из серверного `dcMap`.
- Для неизвестных DC `/test-routes` возвращает понятный `unknown dc`.
- WebSocket bridge применяет `idleTimeoutSec`, чтобы зависшие соединения закрывались без вечного ожидания.
- Запись WebSocket frames сериализована через один writer, чтобы исключить конкурентные записи в клиентское соединение.
- GitHub Actions теперь проверяют `push main` и `pull_request main`; release jobs запускаются только для tag/manual release и checkout-ят нужный tag.

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
