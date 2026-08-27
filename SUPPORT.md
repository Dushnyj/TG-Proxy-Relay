# Поддержка TG Proxy VPS Relay

## Для пользователя Android

Сначала используйте инструкции приложения:

- [VPS Relay](https://github.com/Dushnyj/TG-Proxy/blob/main/docs/VPS_RELAY.md);
- [DuckDNS](https://github.com/Dushnyj/TG-Proxy/blob/main/docs/DUCKDNS.md);
- [Диагностика](https://github.com/Dushnyj/TG-Proxy/blob/main/docs/DIAGNOSTICS.md).

После воспроизведения сохраните полный ZIP через **Диагностика → Сбросить → воспроизвести →
Сохранить ZIP** и приложите его к форме Relay/автонастройки в Android-репозитории.

## Для администратора Relay

Пройдите [TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) и соберите обезличенные:

- версию Relay;
- Linux, architecture и init;
- reverse proxy;
- результат `-check-config`;
- status/последние логи service;
- HTTP status health/version/capabilities/test-routes;
- шаги, ожидаемое и фактическое поведение.

Выберите форму в [Issues](https://github.com/Dushnyj/TG-Proxy-Relay/issues/new/choose).

## Не публикуйте

- raw client/owner tokens и production hashes;
- SSH password/private key;
- TLS/topology signing private key;
- полный `config.json` или `state.json`;
- реальные private domains/IP и device IP;
- диагностический архив до проверки.

Уязвимости сообщайте закрыто по [SECURITY.md](SECURITY.md).
