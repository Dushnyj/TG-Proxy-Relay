# Changelog

Все заметные изменения TG Proxy VPS Relay фиксируются в этом файле.

## [1.3.0] - 2026-08-28

### Стабильная установка Relay

- Каждая установка получает стабильный непубличный `instanceId`. Он хранится в owner state,
  не зависит от IP, домена, сертификата или WebSocket path и возвращается через `/version`,
  `/capabilities`, `/identity` и `/admin/v1/info`. Android использует его, чтобы объединять
  разные токены и адреса одного VPS, не смешивая независимые серверы.
- State schema обновлена до v3 с проверяемой миграцией старых файлов. Если owner state нельзя
  надёжно записать, Relay не начинает слушать сеть с временной identity.

### Идемпотентные токены владельца

- Owner protocol 2 принимает client-generated secret и `idempotencyKey`. Повтор одного запроса
  возвращает тот же token/secret, поэтому потерянный HTTP-ответ не создаёт второй неизвестный
  токен. Удалённые idempotent token IDs сохраняются как tombstone и не воскресают повтором.
- Добавлен `/admin/v1/info`; `/capabilities` объявляет `instance-identity` и
  `idempotent-owner-token-create`. Старый protocol 1 остаётся совместимым для обновления
  существующих установок.

### Идентификация устройств

- Owner overview принимает раздельные manufacturer, brand, canonical brand, raw model,
  marketing name, device/product code и identity version. Android может показать нормальное
  коммерческое название телефона, сохранив исходные значения для диагностики.
- Переход с прежнего случайного device ID на стабильный identity v2 объединяет историю,
  активные sessions и блокировку только по узкому подтверждённому alias; произвольное
  объединение чужих записей заголовком запрещено.

## [1.2.0] - 2026-08-27

### Telegram topology и маршруты

- DC теперь хранит до 32 IPv4/IPv6 endpoints с точным port и ролями regular/media/CDN;
  legacy `dcMap` остаётся bootstrap/migration fallback.
- Добавлены endpoint-level health, exponential cooldown, half-open probe и bounded race
  альтернатив, поэтому мёртвый первый IP не задерживает весь media transfer.
- Signed Ed25519 topology bundle поддерживает schema/generation/notBefore/expiry, replay и
  downgrade protection, limits, atomic LKG persistence, jitter/backoff и on-demand refresh
  неизвестного DC.
- Public IP allow-list закрывает SSRF, localhost/private/link-local/CGNAT/reserved endpoints,
  DNS rebinding source и client-supplied destination.
- `/capabilities` согласует protocol/features/current DC/revision с Android; WebSocket
  предпочитает `tgproxy-relay.v2`, сохраняя legacy `binary`.
- `/test-routes` отдельно проверяет main/media server-side pools и честно маркирует результат
  `TCP_ONLY`; реальный MTProto proof выполняет Android.

### Надёжность и владелец

- Глобальные/per-token/pending session limits ограничивают reconnect и resource storms.
- Владелец может disconnect выбранного устройства и durable block/unblock без отзыва общего
  client token; block закрывает активные сессии и запрещает reconnect.
- Owner overview публикует block state/time, а миграция state v1 → v2 сохраняет существующие
  токены, firstSeen/lastSeen и устройства.
- Строгая WebSocket query validation отклоняет unknown/duplicate parameters.

### Linux-совместимость автонастройки

- Release workflow публикует Linux assets не только для amd64/arm64, но также для 386,
  armv5/armv6/armv7, riscv64, ppc64/ppc64le, s390x, loong64 и MIPS-вариантов.
- Android-установщик определяет package manager и init-систему удалённого Linux VPS, создаёт
  службу для systemd/OpenRC/runit/SysV либо переносимый init-script и настраивает renewal через
  systemd timer или cron.

### Публичный репозиторий и поддержка

- Добавлены единый индекс документации, требования к VPS, troubleshooting, development и
  release runbook, а также связанные Android-инструкции для DuckDNS, QR и диагностики.
- Issue Forms разделяют server bug, Android auto-setup и feature request и требуют удалить
  tokens, production config/state, SSH/TLS keys и частные endpoints.
- Добавлены CI, Dependabot и full-history Gitleaks workflow; примеры используют только
  `example.com` и RFC 5737 адреса.

## [1.1.0] - 2026-08-27

### Добавлено

- Отдельная owner-аутентификация: ключ владельца не является клиентским токеном и не даёт доступ к WebSocket-туннелю.
- Owner API для просмотра токенов и устройств, создания нового клиентского токена и немедленного отзыва токена вместе с активными сессиями.
- Постоянное состояние динамических токенов, отзывов и метаданных клиентов в `/var/lib/tgproxy-relay/state.json`; raw-токены в state не записываются.
- Учёт производителя, модели, версии приложения и Android, первого/последнего подключения и количества активных сессий.
- Опциональное определение страны и города по публичному IP; `admin.geoIpUrl: ""` полностью отключает внешние GeoIP-запросы.
- HTTPS landing page `/connect` и `<websocket.path>/connect` для кликабельных ссылок импорта Android. Новый формат держит payload в URL fragment, который не отправляется Relay или reverse proxy.

### Надёжность и безопасность

- Критические изменения токенов сохраняются синхронно до успешного ответа API; частые обновления `lastSeen` объединяются в одну отложенную запись.
- Клиентские и owner hashes обязаны различаться; роль нельзя повысить повторным использованием одного секрета.
- Owner API и landing page доступны также под настраиваемым WebSocket prefix и compatibility prefix `/apiws`.
- State-файл записывается атомарной заменой с правами `0600`; systemd unit разрешает запись только в каталоги логов и owner-state.

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
