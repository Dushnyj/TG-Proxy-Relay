# Автонастройка из Android

TG Proxy Android может настроить Relay по SSH, если пользователь ввёл данные VPS. Процесс
сначала выполняет read-only audit и меняет сервер только после подтверждения плана.

## Данные владельца

- SSH host/IP и port, обычно `22`;
- SSH username;
- SSH password (или поддерживаемый приложением SSH key);
- публичный Relay host/domain;
- WebSocket path, по умолчанию `/apiws`;
- HTTPS mode;
- отдельные client token и owner token.

Client и owner token генерируются раздельно. В `config.json` отправляются только SHA-256
hashes. Android сохраняет raw client/owner token и, если пользователь включил опцию
«Запомнить данные VPS», SSH-реквизиты в зашифрованном локальном хранилище на базе Android
Keystore. Обычный export/share никогда не содержит SSH-данные или owner token.

Если public host пустой при HTTPS, Android проверяет nginx, Caddy, Apache и certificates,
чтобы найти домены-кандидаты. Пользователь выбирает домен до изменения сервера.

## Новый Relay

Мастер создаёт/обновляет:

- бинарник и `/etc/tgproxy-relay/config.json`;
- systemd service;
- `/var/lib/tgproxy-relay` для owner-state;
- reverse-proxy routes для WebSocket, management, Owner API и `/connect`;
- отдельные client/owner token hashes.

Перед restart выполняется `tgproxy-relay -check-config`. После запуска Android проверяет
версию, health, серверную DC TCP matrix и end-to-end MTProto production scopes.

## Существующий Relay

Если на VPS уже установлен совместимый Relay, Android предлагает подключиться/обновить его,
не переустанавливая всё с нуля. Repair/update:

- сохраняет существующие client tokens;
- добавляет owner hash только при его отсутствии;
- сохраняет явно отключённый `geoIpUrl`;
- создаёт state directory и systemd write permission;
- делает backup конфигурации, unit и reverse-proxy файлов.

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
