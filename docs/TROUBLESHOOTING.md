# Устранение проблем Relay

Сначала определите уровень ошибки: service, локальный HTTP, reverse proxy/HTTPS, token или
доступ Relay к Telegram DC. Не считайте один успешный TCP connect доказательством работы media.

## 1. Проверить версию и конфигурацию

```bash
/opt/tgproxy-relay/tgproxy-relay -version
/opt/tgproxy-relay/tgproxy-relay \
  -config /etc/tgproxy-relay/config.json \
  -check-config
```

`config ok` означает только корректный формат и внутренние ограничения. После него нужно
проверить service и публичный endpoint.

## 2. Проверить службу

### systemd

```bash
systemctl status tgproxy-relay --no-pager
journalctl -u tgproxy-relay -n 200 --no-pager
```

Для OpenRC/runit/SysV используйте штатный статус своей init-системы. Если процесс сразу
завершается, проверьте путь к config/binary, права на `/var/lib/tgproxy-relay`, занятый listen и
последнюю строку лога.

## 3. Проверить локальный listener

На самом VPS:

```bash
ss -lntp | grep 18080
curl -i -H "Authorization: Bearer <raw-client-token>" \
  http://127.0.0.1:18080/healthz
```

Ожидается HTTP 200 и `ok`. HTTP 401/403 означает неверный или отозванный client token. Raw
owner token здесь не подходит.

## 4. Проверить HTTPS и prefix

```bash
curl -i -H "Authorization: Bearer <raw-client-token>" \
  https://relay.example.com/apiws/healthz

curl -i -H "Authorization: Bearer <raw-client-token>" \
  https://relay.example.com/apiws/version
```

Если локальный health работает, а HTTPS нет:

- DNS должен указывать на этот VPS;
- сертификат должен покрывать выбранный host;
- `websocket.path`, Android path и reverse-proxy prefix должны совпадать;
- proxy обязан передавать `Authorization` и WebSocket Upgrade;
- `/healthz`, `/version`, `/capabilities`, `/test-routes`, `/connect` и `/admin/*` должны
  маршрутизироваться под тем же prefix;
- WebSocket buffering нужно отключить, timeout сделать длинным.

Готовые конфиги: [REVERSE_PROXY.md](REVERSE_PROXY.md).

## 5. Проверить server-side Telegram routes

```bash
curl -sS \
  -H "Authorization: Bearer <raw-client-token>" \
  -H "Content-Type: application/json" \
  -d '{"dcs":[{"dc":1},{"dc":2},{"dc":3},{"dc":4},{"dc":5},{"dc":203}]}' \
  https://relay.example.com/apiws/test-routes
```

Результат `TCP_ONLY` означает, что VPS установил TCP-соединение к endpoint из server-side
topology. Окончательный Telegram `req_pq/resPQ` выполняет Android. Поэтому:

- TCP_ONLY PASS + Android MTProto FAIL — проверяйте WebSocket bridge, protocol/capabilities и
  конкретный endpoint;
- main PASS + media FAIL — проверяйте media endpoint pool, порт, IPv4/IPv6 и firewall;
- все DC timeout — возможна фильтрация Telegram у VPS-провайдера или проблема DNS/route.

## 6. Голосовые сообщения, фото или видео не загружаются

1. Обновите Android и Relay до совместимых версий.
2. Проверьте `/capabilities` и `/test-routes`.
3. Убедитесь, что topology содержит media endpoints, а не только один legacy `dcMap` IP.
4. Проверьте endpoint cooldown/error в Android diagnostic ZIP.
5. Проверьте, что reverse proxy не ограничивает WebSocket body, buffer или idle timeout.
6. Сравните regular и media результаты одного DC.

В конфигурации по умолчанию `websocket.maxMessageBytes` равен 16 MiB, а media поток идёт как
последовательность binary messages; reverse proxy не должен буферизовать его целиком.

## 7. Token не принят

- используйте raw client token, не строку `sha256:...`;
- owner token не является client token;
- проверьте, что token не отозван в owner state;
- два подключения одного endpoint могут иметь разные token и сохраняются независимо;
- сервер не может восстановить raw secret из hash; потерянный token нужно импортировать из
  сохранённой ссылки/QR либо создать новый.

Hash без публикации raw secret:

```bash
TGPROXY_RELAY_TOKEN='<new-random-client-token>' \
  /opt/tgproxy-relay/tgproxy-relay -print-token-hash
```

Не передавайте secret в общедоступной истории shell на production VPS.

## 8. Owner API не работает

```bash
curl -i -H "Authorization: Bearer <raw-owner-token>" \
  https://relay.example.com/apiws/admin/v1/overview
```

Проверьте отдельный admin hash, write-доступ к `admin.statePath` и проксирование `/admin/*`.
Client и owner secret обязаны различаться.

## 9. Сертификат не выпускается или не продлевается

- TCP 80 должен быть доступен из интернета;
- DNS должен разрешаться в публичный IP VPS;
- системное время должно быть правильным;
- другой процесс не должен перехватывать challenge path;
- проверьте Certbot timer/cron и его log;
- для IP endpoint нужна версия Certbot/ACME flow, установленная Android-мастером.

Не выключайте renewal после успешной установки: истёкший сертификат одновременно отключит все
клиенты этого endpoint.

## 10. Что приложить к issue

Для проблемы Android/автонастройки сохраните **полный ZIP** из раздела диагностики после
воспроизведения. Для ручного сервера приложите обезличенные:

- Relay version;
- Linux/architecture/init;
- reverse proxy type;
- вывод `-check-config`;
- status и последние server logs;
- HTTP status health/version/capabilities/test-routes;
- ожидаемое и фактическое поведение.

Удалите реальные host/IP, token/hash, device IP, SSH/TLS keys и персональные данные. См.
[SUPPORT.md](../SUPPORT.md).
