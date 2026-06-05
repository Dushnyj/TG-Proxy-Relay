# Обновления

Релизы TG Proxy VPS Relay независимы от релизов TG Proxy Android.

## Проверить версию сервера

```bash
curl -H "Authorization: Bearer <token>" \
  https://relay.example.com/apiws/version
```

## Ручное обновление

```bash
systemctl stop tgproxy-relay
cp /opt/tgproxy-relay/tgproxy-relay /opt/tgproxy-relay/tgproxy-relay.backup
curl -L -o /tmp/tgproxy-relay.tar.gz \
  https://github.com/Dushnyj/TG-Proxy-Relay/releases/download/v1.0.1/TG-Proxy-Relay-v1.0.1-linux-amd64.tar.gz
tar -xzf /tmp/tgproxy-relay.tar.gz -C /opt/tgproxy-relay tgproxy-relay
chmod +x /opt/tgproxy-relay/tgproxy-relay
systemctl start tgproxy-relay
```

Проверка:

```bash
systemctl status tgproxy-relay --no-pager
```

## Подсказка в Android

TG Proxy Android может показать, что подключенный Relay старее совместимой версии.
Если у пользователя есть только token подключения, он должен сообщить владельцу VPS, а не пытаться обновлять сервер самостоятельно.
