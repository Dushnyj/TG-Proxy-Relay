# Документация TG Proxy VPS Relay

## Пользователю TG Proxy Android

- [Требования к VPS](VPS_REQUIREMENTS.md) — какой сервер выбрать, сеть, Linux и порты.
- [Автонастройка из Android](ANDROID_AUTO_SETUP.md) — что приложение проверяет и меняет.
- [Устранение проблем](TROUBLESHOOTING.md) — HTTPS, token, media, service и reverse proxy.
- [Android-инструкция VPS Relay](https://github.com/Dushnyj/TG-Proxy/blob/main/docs/VPS_RELAY.md).
- [DuckDNS без покупки домена](https://github.com/Dushnyj/TG-Proxy/blob/main/docs/DUCKDNS.md).
- [Ссылка, QR и импорт](https://github.com/Dushnyj/TG-Proxy/blob/main/docs/SHARING_AND_IMPORT.md).

## Администратору сервера

- [Ручная установка](INSTALL.md).
- [Reverse proxy](REVERSE_PROXY.md).
- [Client и owner tokens](TOKENS.md).
- [HTTP/WebSocket/Owner API](API.md).
- [Обновление](UPDATES.md).
- [Telegram topology и signing](TOPOLOGY.md).
- [Устранение проблем](TROUBLESHOOTING.md).

## Разработчику и сопровождающему

- [Разработка и тесты](DEVELOPMENT.md).
- [Процесс релиза](RELEASES.md).
- [Участие в проекте](../CONTRIBUTING.md).
- [Поддержка пользователей](../SUPPORT.md).
- [Политика безопасности](../SECURITY.md).

TG Proxy VPS Relay не является самостоятельным Telegram-клиентом или MTProto-прокси для
публичного ввода. Он принимает авторизованный WebSocket от
[TG Proxy Android](https://github.com/Dushnyj/TG-Proxy) и выбирает разрешённый Telegram DC из
серверной topology.
