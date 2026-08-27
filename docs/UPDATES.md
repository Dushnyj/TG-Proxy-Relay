# Обновления

Релизы TG Proxy VPS Relay независимы от релизов TG Proxy Android.

## Проверить версию сервера

```bash
curl -H "Authorization: Bearer <token>" \
  https://relay.example.com/apiws/version
```

## Ручное обновление (пример для systemd)

Автоматическое обновление из Android использует обнаруженную init-систему и не требует
systemd. Команды ниже оставлены как ручной пример для systemd-сервера.

```bash
set -euo pipefail
version=1.2.0
asset="TG-Proxy-Relay-v${version}-linux-amd64.tar.gz"
base="https://github.com/Dushnyj/TG-Proxy-Relay/releases/download/v${version}"

curl -fL -o "/tmp/${asset}" "${base}/${asset}"
curl -fL -o /tmp/SHA256SUMS.txt "${base}/SHA256SUMS.txt"
(cd /tmp && grep " ${asset}$" SHA256SUMS.txt | sha256sum -c -)

stage="$(mktemp -d)"
tar -xzf "/tmp/${asset}" -C "$stage"
"$stage/tgproxy-relay" -version
"$stage/tgproxy-relay" -config /etc/tgproxy-relay/config.json -check-config

cp /opt/tgproxy-relay/tgproxy-relay /opt/tgproxy-relay/tgproxy-relay.backup
install -m 0755 "$stage/tgproxy-relay" /opt/tgproxy-relay/tgproxy-relay
if ! systemctl restart tgproxy-relay || ! systemctl is-active --quiet tgproxy-relay; then
  install -m 0755 /opt/tgproxy-relay/tgproxy-relay.backup /opt/tgproxy-relay/tgproxy-relay
  systemctl restart tgproxy-relay
  exit 1
fi
rm -rf "$stage"
```

Проверка:

```bash
systemctl status tgproxy-relay --no-pager
```

## Миграция с 1.0.x на 1.1.0

Старые client tokens продолжают работать. Для управления владельца добавьте отдельный
`admin` block и каталог состояния:

```bash
install -d -o tgproxy-relay -g tgproxy-relay -m 0750 /var/lib/tgproxy-relay
```

```json
"admin": {
  "tokens": [
    {"id":"owner","name":"owner","hash":"sha256:<owner-hash>"}
  ],
  "statePath": "/var/lib/tgproxy-relay/state.json",
  "geoIpUrl": "https://ipwho.is/%s?lang=ru&fields=success,country,city"
}
```

Owner hash обязан отличаться от всех client hashes. Для полного отключения внешнего GeoIP
укажите `"geoIpUrl": ""` явно. Обновите systemd unit так, чтобы `ReadWritePaths` включал
`/var/lib/tgproxy-relay`, затем выполните:

```bash
/opt/tgproxy-relay/tgproxy-relay -config /etc/tgproxy-relay/config.json -check-config
systemctl daemon-reload
systemctl restart tgproxy-relay
```

Reverse proxy должен пропускать `/apiws/admin/v1/*` и `/apiws/connect`, см.
[REVERSE_PROXY.md](REVERSE_PROXY.md). Client token по-прежнему используется для version/health/capabilities;
owner token — только для Owner API.

## Подсказка в Android

TG Proxy Android может показать, что подключенный Relay старее совместимой версии.
Если у пользователя есть только token подключения, он должен сообщить владельцу VPS, а не пытаться обновлять сервер самостоятельно.
