# Установка TG Proxy VPS Relay

## Требования

- Linux VPS с `systemd`.
- Публичный IPv4 или IPv6.
- HTTPS-домен для production-режима через reverse proxy.
- Root или sudo-доступ.

Relay может работать IP-only без HTTPS для тестов, но для постоянного использования рекомендуется HTTPS-домен.

## Скачать релиз

Скачайте asset под архитектуру VPS:

```bash
mkdir -p /opt/tgproxy-relay
cd /opt/tgproxy-relay
curl -L -o relay.tar.gz \
  https://github.com/Dushnyj/TG-Proxy-Relay/releases/download/v1.0.0/TG-Proxy-Relay-v1.0.0-linux-amd64.tar.gz
tar -xzf relay.tar.gz
chmod +x tgproxy-relay
```

Для ARM VPS используйте `linux-arm64`.

## Создать token hash

Создайте длинный случайный token:

```bash
/opt/tgproxy-relay/tgproxy-relay -token "replace-with-long-random-token" -print-token-hash
```

В серверный конфиг записывается только hash. Raw-token хранится в TG Proxy Android и используется для служебных запросов.

## Конфиг

```bash
install -d -m 0750 /etc/tgproxy-relay
cp /opt/tgproxy-relay/config.example.json /etc/tgproxy-relay/config.json
nano /etc/tgproxy-relay/config.json
```

Минимальный production-конфиг:

```json
{
  "listen": "127.0.0.1:18080",
  "publicUrl": "https://relay.example.com/apiws",
  "tokens": [
    {
      "name": "phone",
      "hash": "sha256:replace-with-token-hash"
    }
  ],
  "telegram": {
    "connectTimeoutMs": 7000,
    "idleTimeoutSec": 125
  }
}
```

## Systemd

```bash
useradd --system --home /nonexistent --shell /usr/sbin/nologin tgproxy-relay || true
cp /opt/tgproxy-relay/packaging/tgproxy-relay.service /etc/systemd/system/tgproxy-relay.service
systemctl daemon-reload
systemctl enable --now tgproxy-relay
systemctl status tgproxy-relay --no-pager
```

Логи:

```bash
journalctl -u tgproxy-relay -f
```

## Проверка

```bash
curl -H "Authorization: Bearer replace-with-raw-token" \
  https://relay.example.com/apiws/healthz
```

Ожидаемый ответ:

```text
ok
```
