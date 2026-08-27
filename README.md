# TG Proxy VPS Relay

**TG Proxy VPS Relay** - серверная часть для [TG Proxy Android](https://github.com/Dushnyj/TG-Proxy).
Relay принимает авторизованный WebSocket-трафик от Android-приложения и открывает TCP-соединения к Telegram DC.

![TG Proxy VPS Relay overview](docs/assets/tg-proxy-relay-hero-v2.png)

## Навигация

- [Как это работает](#как-это-работает)
- [Релизные файлы](#релизные-файлы)
- [Установка](#установка)
- [Reverse proxy](#reverse-proxy)
- [Токены](#токены)
- [Управление владельца](#управление-владельца)
- [Ссылки подключения](#ссылки-подключения)
- [Telegram topology](#telegram-topology)
- [Автонастройка из Android](#автонастройка-из-android)
- [Документация](#документация)
- [Сборка](#сборка)
- [Безопасность](#безопасность)

## Как это работает

```text
Telegram Android
  -> MTProto Proxy (127.0.0.1:1443)
  -> TG Proxy Android route engine
  -> WebSocket/TLS
  -> tgproxy-relay
  -> TCP Telegram DC:443
```

Публичный HTTPS endpoint (домен, DuckDNS или IP certificate) обычно проксирует один путь:

```text
WS   /apiws?dc=2&media=0&test=0
GET  /apiws/healthz      -> /healthz
GET  /apiws/version      -> /version
GET  /apiws/capabilities -> /capabilities
POST /apiws/test-routes  -> /test-routes
```

Клиентские WebSocket и служебные endpoints требуют заголовок:

```text
Authorization: Bearer <token>
```

Owner API использует **отдельный** owner-token. Один raw-секрет нельзя одновременно
использовать как клиентский и owner-token.

Основной WebSocket path задаётся в `websocket.path`. `/apiws` используется по умолчанию и остаётся compatibility alias при переходе на custom path.

## Релизные файлы

GitHub Actions публикует:

```text
TG-Proxy-Relay-v<version>-linux-{amd64,386,arm64,armv7,armv6,armv5}.tar.gz
TG-Proxy-Relay-v<version>-linux-{riscv64,ppc64,ppc64le,s390x,loong64}.tar.gz
TG-Proxy-Relay-v<version>-linux-{mips,mipsle,mips64,mips64le}.tar.gz
SHA256SUMS.txt
```

Для большинства обычных x86-64 VPS нужен `linux-amd64`; для 64-битного ARM — `linux-arm64`.
Android-мастер сам сопоставляет `uname -m` с правильным asset, проверяет checksum и версию
до замены бинарника.

## Установка

Ручная установка описана в [docs/INSTALL.md](docs/INSTALL.md).
Если используется TG Proxy Android, удобнее открыть **Настройки -> Relay -> Автонастройка VPS**.

## Reverse proxy

Relay обычно слушает `127.0.0.1:18080`, а наружу публикуется через nginx, Caddy или Apache по
HTTPS. Android-мастер умеет полностью настроить чистый VPS по публичному IP, DuckDNS или уже
имеющемуся домену, включая certificate и renewal timer/cron. Мастер не привязан к Ubuntu:
поддерживаются systemd, OpenRC, runit и SysV, а пакеты устанавливаются через пакетный менеджер
обнаруженного Linux-дистрибутива.
Безопасные path-based примеры есть в [docs/REVERSE_PROXY.md](docs/REVERSE_PROXY.md).

## Токены

На сервере хранятся хэши токенов, а не raw-токены:

```bash
tgproxy-relay -token "long-random-token" -print-token-hash
```

Полученный hash записывается в `config.json`. Подробнее: [docs/TOKENS.md](docs/TOKENS.md).

## Управление владельца

Начиная с `1.1.0`, владелец VPS может из Android-приложения:

- видеть существующие клиентские токены;
- создать новый токен и получить его raw-значение один раз;
- немедленно отозвать токен и закрыть его активные сессии;
- видеть привязанные устройства: марку, модель, версию приложения/Android,
  первое и последнее подключение, активные сессии, страну и город.
- отключить только сессии выбранного устройства, заблокировать и разблокировать его без
  отзыва общего token.

Доступ выдаётся только отдельным owner-token из `admin.tokens`. Динамическое состояние
хранится в `/var/lib/tgproxy-relay/state.json`; raw клиентские токены в этот файл не попадают.
Внешнее GeoIP-определение можно отключить значением `"geoIpUrl": ""`.

Контракт API и конфигурации: [docs/API.md](docs/API.md), [docs/TOKENS.md](docs/TOKENS.md).

## Ссылки подключения

Relay публикует landing page:

```text
GET /connect
GET <websocket.path>/connect
```

Android создаёт HTTPS-ссылку вида
`https://relay.example.com/apiws/connect#data=<payload>`. Fragment после `#` не отправляется
на сервер и локально преобразуется страницей в `tgproxy://import?...`. Та же payload-модель
используется для QR-кода, системного меню «Поделиться» и импорта из файла. SSH-данные и
owner-token в клиентское подключение не входят.

## Telegram topology

Relay 1.2.0 принимает несколько IPv4/IPv6 endpoints и произвольный port на DC, разделяет
regular/media/CDN pools, быстро гоняет альтернативы и ведёт endpoint-level cooldown. Новый
signed topology manifest обновляется без APK и без ручного редактирования каждого клиента:

```text
signed current -> atomic LKG -> embedded owner bootstrap
```

Android и Relay согласуют protocol/features/current DC revision через `/capabilities`.
Неизвестный DC не превращается в client-supplied destination: адрес выбирается только из
server-side owner config или проверенного Ed25519 bundle. Настройка и signing CLI описаны в
[docs/TOPOLOGY.md](docs/TOPOLOGY.md).

## Автонастройка из Android

TG Proxy Android умеет:

- проверить VPS без изменений;
- найти уже установленный совместимый Relay;
- добавить новый token в существующий Relay;
- установить или обновить Relay, если пользователь владеет VPS;
- сохранить SSH/owner-данные локально в зашифрованном хранилище Android;
- управлять токенами и подключёнными устройствами;
- импортировать подключение без SSH-данных.

Подробнее: [docs/ANDROID_AUTO_SETUP.md](docs/ANDROID_AUTO_SETUP.md).

## Документация

- [Установка](docs/INSTALL.md)
- [Reverse proxy](docs/REVERSE_PROXY.md)
- [API](docs/API.md)
- [Токены](docs/TOKENS.md)
- [Обновления](docs/UPDATES.md)
- [Автонастройка Android](docs/ANDROID_AUTO_SETUP.md)
- [Telegram topology и signing](docs/TOPOLOGY.md)

## Сборка

```bash
go test ./...
go build -trimpath -o tgproxy-relay ./cmd/tgproxy-relay
```

Release workflow задаёт `internal/relay.Version` из Git-тега.

## Безопасность

Не публикуйте raw-токены, SSH-данные, приватные ключи и полные production-конфиги.
Правила сообщения об уязвимостях описаны в [SECURITY.md](SECURITY.md).

## Лицензия

MIT, см. [LICENSE](LICENSE).
