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
  https://github.com/Dushnyj/TG-Proxy-Relay/releases/download/v1.0.5/TG-Proxy-Relay-v1.0.5-linux-amd64.tar.gz
curl -L -o SHA256SUMS.txt \
  https://github.com/Dushnyj/TG-Proxy-Relay/releases/download/v1.0.5/SHA256SUMS.txt
grep 'TG-Proxy-Relay-v1.0.5-linux-amd64.tar.gz' SHA256SUMS.txt | sha256sum -c -
tar -xzf relay.tar.gz
chmod +x tgproxy-relay
./tgproxy-relay -version
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
/opt/tgproxy-relay/tgproxy-relay -config /etc/tgproxy-relay/config.json -check-config
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
    "idleTimeoutSec": 0,
    "dcMap": {
      "1": "149.154.175.50",
      "2": "149.154.167.51",
      "3": "149.154.175.100",
      "4": "149.154.167.91",
      "5": "149.154.171.5",
      "203": "91.105.192.100"
    },
    "testDcMap": {
      "1": "149.154.175.10",
      "2": "149.154.167.40",
      "3": "149.154.175.117"
    }
  },
  "websocket": {
    "path": "/apiws",
    "pingIntervalSec": 25,
    "pongTimeoutSec": 12,
    "writeTimeoutSec": 15,
    "maxMessageBytes": 16777216
  }
}
```

`websocket.path` должен быть абсолютным путём без query/fragment и совпадать с Android/reverse proxy. Пути `/healthz`, `/version` и `/test-routes` зарезервированы. После изменения всегда запускайте `-check-config` до restart.

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
