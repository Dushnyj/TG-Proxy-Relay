# Автонастройка из Android

TG Proxy Android может настроить Relay по SSH, если пользователь ввёл данные VPS. Процесс
сначала выполняет read-only audit и меняет сервер только после подтверждения плана.

## Данные владельца

- SSH host/IP и port, обычно `22`;
- SSH username;
- SSH password;
- публичный Relay host/domain;
- WebSocket path, по умолчанию `/apiws`;
- HTTPS mode;
- отдельные client token и owner token.

Client и owner token генерируются раздельно. В `config.json` отправляются только SHA-256
hashes. Android сохраняет raw client/owner token и, если пользователь включил опцию
«Запомнить данные VPS», SSH-реквизиты в зашифрованном локальном хранилище на базе Android
Keystore. Обычный export/share никогда не содержит SSH-данные или owner token.

После SSH-аудита Android предлагает три endpoint-режима: HTTPS по публичному IP, бесплатное
имя DuckDNS или собственный домен. Собственный домен не обязан уже использоваться на VPS и
не обязан относиться к DuckDNS: пользователь может ввести любой принадлежащий ему домен или
поддомен, если его A/AAAA-запись указывает на публичный IP VPS. Найденные в конфигурациях
сервера домены показываются отдельно только как подсказки. В режиме DuckDNS пользователь
вводит созданное имя и provider token; token хранится только в Android Keystore и не попадает
в Relay config/export.

На чистом VPS Android сам устанавливает nginx и изолированный Certbot venv, выпускает
Let's Encrypt certificate через HTTP-01 и включает проверку продления каждые 12 часов через
systemd timer либо cron. Для публичного IP используется короткоживущий профиль `shortlived`
и `--ip-address`; домен вручную на сервере настраивать не нужно.

Автонастройка определяет Linux-дистрибутив, пакетный менеджер, init-систему и архитектуру, а не
проверяет конкретную версию Ubuntu. Поддерживаемые package manager: apt, dnf/microdnf, yum,
zypper, apk, pacman, xbps и Portage. Поддерживаемые службы: systemd, OpenRC, runit, SysV;
минимальная Linux-система получает переносимый init script с reboot fallback.

## Новый Relay

Мастер создаёт/обновляет:

- бинарник и `/etc/tgproxy-relay/config.json`;
- службу автозапуска для обнаруженной init-системы;
- `/var/lib/tgproxy-relay` для owner-state;
- reverse-proxy routes для WebSocket, management, Owner API и `/connect`;
- nginx/Certbot, HTTPS certificate и renewal timer, если web stack ещё не готов;
- отдельные client/owner token hashes.

Перед restart выполняется `tgproxy-relay -check-config`. После запуска Android проверяет
версию, health, серверную DC TCP matrix и end-to-end MTProto production scopes.

## Существующий Relay

Если на VPS уже установлен совместимый Relay, Android предлагает подключиться/обновить его,
не переустанавливая всё с нуля. Repair/update:

- сохраняет существующие client tokens;
- добавляет owner hash только при его отсутствии;
- сохраняет явно отключённый `geoIpUrl`;
- создаёт state directory и необходимые права службы;
- делает backup конфигурации, service definition, cron и reverse-proxy файлов.

## Обновления сервера

Если Relay старее совместимой версии, Android предлагает:

- **Обновить**, если локально сохранены или введены SSH/owner-данные;
- **Пропустить**, если у пользователя только импортированное клиентское подключение.

Production main/media validation блокирует сохранение нерабочего обновления. Доступность
Telegram test environment показывается отдельно как diagnostic warning и не должна откатывать
исправный production Relay.

## Управление после установки

Только профиль владельца показывает Owner API:

- список client tokens;
- создать token;
- удалить/отозвать token;
- список известных и активных устройств с моделью, версиями и географией;
- первое/последнее использование, disconnect текущих сессий и durable block/unblock;
- share одного из локально известных raw client tokens.

Relay никогда не может восстановить raw secret по hash. Поэтому токен, созданный на другом
устройстве владельца и не переданный текущему устройству, виден в overview, но поделиться его
секретом нельзя; можно создать новый token или отозвать старый.

## Безопасность и rollback

- Все изменяемые server files получают timestamped backup.
- Reverse-proxy config валидируется до reload.
- При неуспешном production health/E2E check мастер восстанавливает backup.
- Динамическое owner-state не включается в обычный Android share/export.
- Не используйте один secret для client и owner roles.
