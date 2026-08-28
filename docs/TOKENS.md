# Токены и права владельца

Начиная с Relay `1.1.0`, сервер разделяет две роли; Relay `1.3.0` дополнительно поддерживает
disconnect/block/unblock отдельного устройства, stable instance identity и идемпотентное
создание client token:

- **client token** — WebSocket-трафик, `/healthz`, `/version`, `/capabilities`, `/test-routes`;
- **owner token** — только `/admin/v1/*`: список, создание и отзыв client tokens,
  просмотр, disconnect, block и unblock привязанных устройств.

Одинаковый raw-секрет или hash в обеих ролях запрещён конфиг-валидатором.

## Хранение

`config.json` хранит только SHA-256 hashes:

```json
{
  "tokens": [
    {
      "id": "primary",
      "name": "phone",
      "hash": "sha256:..."
    }
  ],
  "admin": {
    "tokens": [
      {
        "id": "owner",
        "name": "owner",
        "hash": "sha256:..."
      }
    ],
    "statePath": "/var/lib/tgproxy-relay/state.json",
    "geoIpUrl": "https://ipwho.is/%s?lang=ru&fields=success,country,city"
  }
}
```

State-файл хранит hashes динамически созданных токенов, список отозванных hashes и
метаданные устройств. Raw client/owner tokens в него не записываются. Права файла — `0600`.

## Создать client и owner hash

Используйте два разных длинных случайных значения:

```bash
tgproxy-relay -token "replace-with-random-client-token" -print-token-hash
tgproxy-relay -token "replace-with-different-random-owner-token" -print-token-hash
```

Raw client token нужен Android-подключению. Raw owner token нужен только владельцу VPS и
локально сохраняется Android-приложением в Android Keystore. Не включайте owner token в
ссылку, QR-код или экспорт обычного Relay-подключения.

## Управление из Android

Если локальный профиль содержит зашифрованный owner token, кнопка управления владельца
вызывает Owner API. Перед созданием Android сохраняет в Keystore client-generated secret и
idempotency key; повтор после обрыва сети возвращает тот же token. После подтверждённого ответа
Android сохраняет secret локально в зашифрованном виде и может поделиться только этим
клиентским подключением.

Импортированный обычный client token не открывает Owner API. Наличие SSH-реквизитов или
создание Relay через мастер дают владельцу возможность восстановить/обновить owner-настройку,
но сами SSH-реквизиты никогда не экспортируются клиенту.

## Создание и отзыв через API

```bash
curl -X POST \
  -H "Authorization: Bearer <owner-token>" \
  -H "Content-Type: application/json" \
  -d '{"name":"Телефон семьи","secret":"tgpr_<client-generated-secret>","idempotencyKey":"req_<stable-request-id>"}' \
  https://relay.example.com/apiws/admin/v1/tokens
```

Ответ содержит raw `secret` ровно один раз. Отзыв:

```bash
curl -X DELETE \
  -H "Authorization: Bearer <owner-token>" \
  https://relay.example.com/apiws/admin/v1/tokens/<token-id>
```

После успешного `204` новые подключения отклоняются, а активные сессии этого токена
закрываются.

## Ротация owner token

1. Создайте новый случайный owner token и hash.
2. Добавьте новый hash в `admin.tokens`, не удаляя старый.
3. Выполните `-check-config` и перезапустите Relay.
4. Сохраните новый owner token на устройстве владельца и проверьте overview.
5. Удалите старый owner hash, снова проверьте конфиг и перезапустите сервис.

## Ручная ротация client token

Config-токены можно менять через `config.json` и restart. Динамические токены лучше
создавать/отзывать Owner API: операция является durability boundary и не требует restart.
