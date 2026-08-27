# Установка TG Proxy VPS Relay

Для выбора подходящего сервера, Linux, ресурсов и firewall сначала прочитайте
[VPS_REQUIREMENTS.md](VPS_REQUIREMENTS.md). Новичкам рекомендуется автонастройка из TG Proxy
Android; команды ниже предназначены для ручного администрирования.

## Требования

- Linux VPS. Для автонастройки из Android поддерживаются `systemd`, OpenRC, runit, SysV init
  и переносимый init-script fallback.
- Публичный IPv4 или IPv6.
- Публичный IP; по желанию бесплатное имя DuckDNS или уже имеющийся домен.
- Root или sudo-доступ.

TG Proxy Android публикует Relay только через HTTPS: по домену либо по публичному IP с
короткоживущим Let's Encrypt IP-сертификатом. Мастер приложения сам устанавливает reverse
proxy, выпускает certificate и включает renewal timer. Незащищённый standalone HTTP остаётся
только ручным диагностическим режимом.

## Скачать релиз

Скачайте asset под архитектуру VPS:

```bash
mkdir -p /opt/tgproxy-relay
cd /opt/tgproxy-relay
asset=TG-Proxy-Relay-v1.2.0-linux-amd64.tar.gz
curl -fL -o "$asset" \
  "https://github.com/Dushnyj/TG-Proxy-Relay/releases/download/v1.2.0/$asset"
curl -L -o SHA256SUMS.txt \
  https://github.com/Dushnyj/TG-Proxy-Relay/releases/download/v1.2.0/SHA256SUMS.txt
grep " $asset$" SHA256SUMS.txt | sha256sum -c -
tar -xzf "$asset"
chmod +x tgproxy-relay
./tgproxy-relay -version
```

Доступные суффиксы архитектур: `amd64`, `386`, `arm64`, `armv7`, `armv6`, `armv5`, `riscv64`,
`ppc64`, `ppc64le`, `s390x`, `loong64`, `mips`, `mipsle`, `mips64`, `mips64le`.
Автонастройка Android выбирает суффикс автоматически по `uname -m`.

## Создать client и owner hashes

Создайте длинный случайный token:

```bash
/opt/tgproxy-relay/tgproxy-relay -token "replace-with-long-random-token" -print-token-hash
/opt/tgproxy-relay/tgproxy-relay -token "replace-with-different-owner-token" -print-token-hash
```

В серверный конфиг записываются только hashes. Значения обязаны различаться. Raw client
token хранится у подключаемого Android-клиента; raw owner token — только у владельца VPS.

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
      "id": "primary",
      "name": "phone",
      "hash": "sha256:replace-with-token-hash"
    }
  ],
  "admin": {
    "tokens": [
      {
        "id": "owner",
        "name": "owner",
        "hash": "sha256:replace-with-different-owner-token-hash"
      }
    ],
    "statePath": "/var/lib/tgproxy-relay/state.json",
    "geoIpUrl": "https://ipwho.is/%s?lang=ru&fields=success,country,city"
  },
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

`websocket.path` должен быть абсолютным путём без query/fragment и совпадать с
Android/reverse proxy. Пути `/healthz`, `/version`, `/capabilities`, `/test-routes`, `/connect` и `/admin/*`
зарезервированы. `geoIpUrl` можно явно задать пустой строкой, чтобы не отправлять публичные
IP внешнему GeoIP-сервису. После изменения всегда запускайте `-check-config` до restart.

## Systemd (пример ручной установки)

Ниже показан только ручной systemd-вариант. Android-мастер генерирует соответствующую службу
сам: OpenRC service, runit service directory, SysV init script или переносимый init script.

```bash
useradd --system --home /nonexistent --shell /usr/sbin/nologin tgproxy-relay || true
install -d -o tgproxy-relay -g tgproxy-relay -m 0750 /var/lib/tgproxy-relay
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
read -rsp 'Client token: ' TGPROXY_CLIENT_TOKEN; echo
curl -H "Authorization: Bearer ${TGPROXY_CLIENT_TOKEN}" \
  https://relay.example.com/apiws/healthz

curl -H "Authorization: Bearer ${TGPROXY_CLIENT_TOKEN}" \
  https://relay.example.com/apiws/capabilities
unset TGPROXY_CLIENT_TOKEN
```

Ожидаемый ответ:

```text
ok
```

Owner API:

```bash
read -rsp 'Owner token: ' TGPROXY_OWNER_TOKEN; echo
curl -H "Authorization: Bearer ${TGPROXY_OWNER_TOKEN}" \
  https://relay.example.com/apiws/admin/v1/overview
unset TGPROXY_OWNER_TOKEN
```

Проверьте также `https://relay.example.com/apiws/connect`: должна открыться страница
«Добавить подключение VPS Relay» без раскрытия client token в URL query.
