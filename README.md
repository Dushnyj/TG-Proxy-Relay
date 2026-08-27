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

Публичный HTTPS-домен обычно проксирует один путь:

```text
WS   /apiws?dc=2&media=0&test=0
GET  /apiws/healthz      -> /healthz
GET  /apiws/version      -> /version
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
TG-Proxy-Relay-v<version>-linux-amd64.tar.gz
TG-Proxy-Relay-v<version>-linux-arm64.tar.gz
SHA256SUMS.txt
```

Для большинства обычных VPS нужен `linux-amd64`. Для ARM VPS нужен `linux-arm64`.

## Установка

Ручная установка описана в [docs/INSTALL.md](docs/INSTALL.md).
Если используется TG Proxy Android, удобнее открыть **Настройки -> VPS Relay -> Автонастройка VPS**.

## Reverse proxy

Relay обычно слушает `127.0.0.1:18080`, а наружу публикуется через nginx, Caddy или Apache на HTTPS-домене.
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
