# Обновления

Релизы TG Proxy VPS Relay независимы от релизов TG Proxy Android.

## Проверить версию сервера

```bash
curl -H "Authorization: Bearer <token>" \
  https://relay.example.com/apiws/version
```

## Ручное обновление

```bash
set -euo pipefail
version=1.0.5
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

## Подсказка в Android

TG Proxy Android может показать, что подключенный Relay старее совместимой версии.
Если у пользователя есть только token подключения, он должен сообщить владельцу VPS, а не пытаться обновлять сервер самостоятельно.
