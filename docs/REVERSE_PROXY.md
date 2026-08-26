# Reverse proxy

Relay рассчитан на работу за существующим HTTPS virtual host.
Безопасный вариант по умолчанию - добавить отдельный path, например `/apiws`, не ломая существующий сайт.

## Контракт маршрутов

Публичные paths должны вести во внутренние Relay paths:

```text
/apiws              -> /apiws
/apiws/healthz      -> /healthz
/apiws/version      -> /version
/apiws/test-routes  -> /test-routes
```

Для `/apiws` reverse proxy должен сохранять WebSocket upgrade headers.

## nginx

```nginx
location = /apiws {
    proxy_pass http://127.0.0.1:18080/apiws;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header Authorization $http_authorization;
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
```

Безопасный reload:

```bash
nginx -t && systemctl reload nginx
```

## Caddy

```caddyfile
relay.example.com {
    @relayWs path /apiws
    reverse_proxy @relayWs 127.0.0.1:18080 {
        flush_interval -1
        stream_timeout 0
    }

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
}
```

Безопасный reload:

```bash
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
```

## Apache

Включите modules:

```bash
a2enmod proxy proxy_http proxy_wstunnel headers
systemctl reload apache2
```

Virtual host fragment:

```apache
ProxyPreserveHost On

ProxyPass        "/apiws/healthz" "http://127.0.0.1:18080/healthz"
ProxyPassReverse "/apiws/healthz" "http://127.0.0.1:18080/healthz"

ProxyPass        "/apiws/version" "http://127.0.0.1:18080/version"
ProxyPassReverse "/apiws/version" "http://127.0.0.1:18080/version"

ProxyPass        "/apiws/test-routes" "http://127.0.0.1:18080/test-routes"
ProxyPassReverse "/apiws/test-routes" "http://127.0.0.1:18080/test-routes"

ProxyPass        "/apiws" "ws://127.0.0.1:18080/apiws"
ProxyPassReverse "/apiws" "ws://127.0.0.1:18080/apiws"
```

## Правила безопасности

- Не перезаписывайте existing virtual host без backup.
- Не выпускайте и не меняйте TLS certificates автоматически без явного подтверждения.
- Для сложных сайтов лучше использовать отдельный subdomain.
- Всегда валидируйте config до reload.
- Если Relay стоит за reverse proxy, оставляйте bind на `127.0.0.1`.
