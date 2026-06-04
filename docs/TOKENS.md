# Токены

Relay использует bearer tokens для WebSocket-трафика и служебных endpoints.

## Хранение

`config.json` хранит hashes:

```json
{
  "tokens": [
    {
      "name": "phone",
      "hash": "sha256:..."
    }
  ]
}
```

Raw-token после генерации hash на сервере не нужен.

## Создать hash

```bash
tgproxy-relay -token "long-random-token" -print-token-hash
```

Лучше использовать отдельный token для каждого устройства или отдельного подключения. Тогда владелец VPS сможет отозвать один token без замены всех подключений.

## Ротация

1. Добавьте новый token hash в `config.json`.
2. Перезапустите Relay:

```bash
systemctl restart tgproxy-relay
```

3. Обновите подключение в TG Proxy Android.
4. Удалите старый token hash после миграции клиентов.

## Отзыв

Удалите token из `config.json` и перезапустите сервис:

```bash
systemctl restart tgproxy-relay
```

Android-клиенты со старым token перестанут проходить авторизацию.
