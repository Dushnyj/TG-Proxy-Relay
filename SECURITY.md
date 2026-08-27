# Политика безопасности

## Поддерживаемые версии

Исправления безопасности выпускаются для последней опубликованной версии TG Proxy VPS Relay.

| Версия | Поддержка |
| --- | --- |
| latest | Да |
| older | Только критические исправления по возможности |

## Закрытое сообщение

Используйте
[Private vulnerability reporting](https://github.com/Dushnyj/TG-Proxy-Relay/security/advisories/new).
Не создавайте публичный issue с proof, позволяющим атаковать production Relay.

Укажите:

- версию Relay и asset architecture;
- Linux/init и reverse proxy;
- затронутый endpoint/компонент;
- минимальные шаги воспроизведения;
- ожидаемое и фактическое поведение;
- влияние на auth, isolation, topology, availability или data;
- обезличенные логи без secrets.

## Никогда не отправляйте публично

- raw client/owner tokens или production hashes;
- полный production `config.json`/`state.json`;
- SSH password/private key;
- TLS private key;
- Ed25519 topology signing private key;
- реальные private domains/IP и device IP;
- GitHub credentials;
- user diagnostic archive без проверки.

## Основные security boundaries

- client и owner roles разделены;
- raw tokens не хранятся сервером;
- destination принадлежит server topology, а не запросу клиента;
- private/reserved endpoints и replay/downgrade topology отклоняются;
- state и topology LKG записываются атомарно;
- revoke/block закрывает активные sessions;
- reverse proxy должен сохранять auth и ограничивать public routes заявленным prefix.

В scope входят Relay binary, config/state migration, owner API, WebSocket/TCP bridge, topology,
release artifacts и поставляемые reverse-proxy/service contracts.
