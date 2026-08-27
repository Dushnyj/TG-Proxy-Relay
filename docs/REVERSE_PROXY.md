# Reverse proxy

Relay рассчитан на работу за существующим HTTPS virtual host. Безопасный вариант — отдельный
path, например `/apiws`, при внутреннем bind `127.0.0.1:18080`.

## Контракт маршрутов

```text
/apiws                         -> /apiws                   WebSocket
/apiws/healthz                 -> /healthz                 client auth
/apiws/version                 -> /version                 client auth
/apiws/test-routes             -> /test-routes             client auth
/apiws/admin/v1/*              -> /apiws/admin/v1/*        owner auth
/apiws/connect                 -> /apiws/connect            public landing
```

Reverse proxy должен передавать `Authorization`, реальный клиентский IP и WebSocket upgrade.
Новый share payload находится в URL fragment после `#`; fragment по HTTP не передаётся и в
access log не попадает.

## nginx

```nginx
location = /apiws {
    proxy_pass http://127.0.0.1:18080/apiws;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    proxy_buffering off;
    proxy_request_buffering off;
}

location = /apiws/healthz {
    proxy_pass http://127.0.0.1:18080/healthz;
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
}

location = /apiws/version {
    proxy_pass http://127.0.0.1:18080/version;
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
}

location = /apiws/test-routes {
    proxy_pass http://127.0.0.1:18080/test-routes;
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
}

# Owner API и public landing сохраняют prefix без rewrite.
location ^~ /apiws/ {
    proxy_pass http://127.0.0.1:18080;
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
}
```

Безопасный reload:

```bash
nginx -t && systemctl reload nginx
```

## Caddy

```caddyfile
relay.example.com {
    handle /apiws/healthz {
        rewrite * /healthz
        reverse_proxy 127.0.0.1:18080
    }

    handle /apiws/version {
        rewrite * /version
        reverse_proxy 127.0.0.1:18080
    }

    handle /apiws/test-routes {
        rewrite * /test-routes
        reverse_proxy 127.0.0.1:18080
    }

    # Включает /apiws, /apiws/admin/v1/* и /apiws/connect.
    handle /apiws* {
        reverse_proxy 127.0.0.1:18080 {
            flush_interval -1
            stream_timeout 0
        }
    }
}
```

Caddy автоматически передаёт стандартные `X-Forwarded-*` заголовки. Безопасный reload:

```bash
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
```

## Apache

```bash
a2enmod proxy proxy_http proxy_wstunnel headers
systemctl reload apache2
```

Более специфичные HTTP routes должны стоять раньше WebSocket route:

```apache
ProxyPreserveHost On
RequestHeader set X-Forwarded-Proto "https"

ProxyPass        "/apiws/healthz" "http://127.0.0.1:18080/healthz"
ProxyPassReverse "/apiws/healthz" "http://127.0.0.1:18080/healthz"
ProxyPass        "/apiws/version" "http://127.0.0.1:18080/version"
ProxyPassReverse "/apiws/version" "http://127.0.0.1:18080/version"
ProxyPass        "/apiws/test-routes" "http://127.0.0.1:18080/test-routes"
ProxyPassReverse "/apiws/test-routes" "http://127.0.0.1:18080/test-routes"
ProxyPass        "/apiws/admin/" "http://127.0.0.1:18080/apiws/admin/"
ProxyPassReverse "/apiws/admin/" "http://127.0.0.1:18080/apiws/admin/"
ProxyPass        "/apiws/connect" "http://127.0.0.1:18080/apiws/connect"
ProxyPassReverse "/apiws/connect" "http://127.0.0.1:18080/apiws/connect"

ProxyPass        "/apiws" "ws://127.0.0.1:18080/apiws" timeout=3600
ProxyPassReverse "/apiws" "ws://127.0.0.1:18080/apiws"
```

## Проверка после reload

```bash
curl -fsS -H "Authorization: Bearer <client-token>" \
  https://relay.example.com/apiws/version
curl -fsS -H "Authorization: Bearer <owner-token>" \
  https://relay.example.com/apiws/admin/v1/overview
curl -fsSI https://relay.example.com/apiws/connect
```

## Правила безопасности

- Не перезаписывайте существующий virtual host без backup.
- Не выпускайте и не меняйте TLS certificates автоматически без явного подтверждения.
- Для сложных сайтов используйте отдельный subdomain.
- Всегда валидируйте config до reload.
- Если Relay стоит за reverse proxy, оставляйте bind на `127.0.0.1`.
- Relay доверяет `X-Forwarded-For` только когда непосредственный peer loopback; не публикуйте
  внутренний HTTP listener в интернет.
