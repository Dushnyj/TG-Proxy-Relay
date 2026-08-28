package relay

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const maxSharePayloadLength = 16 * 1024

func (s *Server) registerOwnerRoutes(mux *http.ServeMux, prefix string) {
	if s == nil || mux == nil {
		return
	}
	base := strings.TrimSuffix(prefix, "/") + "/admin/v1"
	mux.HandleFunc(base+"/info", s.withAdminAuth(s.handleOwnerInfo))
	mux.HandleFunc(base+"/overview", s.withAdminAuth(s.handleOwnerOverview))
	mux.HandleFunc(base+"/tokens", s.withAdminAuth(s.handleOwnerTokens))
	mux.HandleFunc(base+"/tokens/", s.withAdminAuth(s.handleOwnerToken))
}

func (s *Server) handleOwnerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	instanceID, persistent := s.control.instanceIdentity()
	writeJSON(w, http.StatusOK, struct {
		InstanceID         string `json:"instanceId"`
		IdentityPersistent bool   `json:"identityPersistent"`
		Name               string `json:"name"`
		Version            string `json:"version"`
		OwnerProtocol      int    `json:"ownerProtocol"`
		PublicURL          string `json:"publicUrl,omitempty"`
	}{
		InstanceID: instanceID, IdentityPersistent: persistent, Name: Name,
		Version: Version, OwnerProtocol: OwnerAPIProtocol, PublicURL: s.cfg.PublicURL,
	})
}

func (s *Server) handleOwnerOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.control.overview())
}

func (s *Server) handleOwnerTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxControlBodyBytes)
	var request struct {
		Name           string `json:"name"`
		Secret         string `json:"secret,omitempty"`
		IdempotencyKey string `json:"idempotencyKey,omitempty"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	headerKey := r.Header.Get("Idempotency-Key")
	if headerKey != "" && request.IdempotencyKey != "" && headerKey != request.IdempotencyKey {
		http.Error(w, "conflicting idempotency key", http.StatusBadRequest)
		return
	}
	if request.IdempotencyKey == "" {
		request.IdempotencyKey = headerKey
	}
	token, secret, replayed, err := s.control.createToken(
		request.Name, request.Secret, request.IdempotencyKey)
	if err != nil {
		message := err.Error()
		if strings.Contains(message, "invalid") || strings.Contains(message, "requires") {
			http.Error(w, message, http.StatusBadRequest)
			return
		}
		if strings.Contains(message, "idempotency key") {
			http.Error(w, message, http.StatusConflict)
			return
		}
		http.Error(w, "token could not be created", http.StatusInternalServerError)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	writeJSON(w, http.StatusCreated, createdTokenResponse{
		Token:  tokenView{ID: token.ID, Name: token.Name, CreatedAt: token.CreatedAt},
		Secret: secret,
	})
}

func (s *Server) handleOwnerToken(w http.ResponseWriter, r *http.Request) {
	marker := "/admin/v1/tokens/"
	markerAt := strings.LastIndex(r.URL.Path, marker)
	if markerAt < 0 {
		http.Error(w, "invalid token id", http.StatusBadRequest)
		return
	}
	rawPath := strings.Trim(r.URL.Path[markerAt+len(marker):], "/")
	parts := strings.Split(rawPath, "/")
	if rawPath == "" || len(parts) == 0 {
		http.Error(w, "invalid token id", http.StatusBadRequest)
		return
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil || sanitizeIdentifier(id, 96) == "" {
		http.Error(w, "invalid token id", http.StatusBadRequest)
		return
	}
	if len(parts) == 1 {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := s.control.deleteToken(id); err != nil {
			writeOwnerMutationError(w, err, "token could not be deleted")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) != 4 || parts[1] != "devices" {
		http.Error(w, "invalid device path", http.StatusBadRequest)
		return
	}
	deviceID, err := url.PathUnescape(parts[2])
	if err != nil || sanitizeIdentifier(deviceID, 96) == "" {
		http.Error(w, "invalid device id", http.StatusBadRequest)
		return
	}
	switch parts[3] {
	case "disconnect":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		err = s.control.disconnectDevice(id, deviceID)
	case "block":
		if r.Method != http.MethodPut && r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		err = s.control.setDeviceBlocked(id, deviceID, r.Method == http.MethodPut)
	default:
		http.Error(w, "invalid device action", http.StatusBadRequest)
		return
	}
	if err != nil {
		writeOwnerMutationError(w, err, "device action failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeOwnerMutationError(w http.ResponseWriter, err error, fallback string) {
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, fallback, http.StatusInternalServerError)
		return
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

const connectLandingHTML = `<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark"><title>Подключение TG Proxy</title>
<style>body{margin:0;font:16px system-ui,sans-serif;background:#0d1524;color:#eef4ff;display:grid;min-height:100vh;place-items:center}.c{box-sizing:border-box;width:min(92vw,520px);padding:28px;border:1px solid #2b3b55;border-radius:24px;background:#152238;box-shadow:0 24px 64px #0006}h1{font-size:25px;margin:0 0 12px}p{line-height:1.55;color:#bfd0e8}.b{display:block;padding:15px 18px;border-radius:14px;background:#76a9ff;color:#071425;text-align:center;text-decoration:none;font-weight:750}.s{margin-top:12px;background:transparent;color:#c9dcff;border:1px solid #47658e}.bad{color:#ffb4ab}.hidden{display:none}</style></head>
<body><main class="c"><h1>Добавить подключение VPS Relay</h1><p>Ссылка содержит только параметры клиентского подключения. Данные входа на VPS и ключ владельца не передаются.</p>
<p id="error" class="bad hidden">Ссылка подключения повреждена или устарела.</p><div id="actions" class="hidden"><a id="open" class="b" href="#">Открыть TG Proxy</a><a id="retry" class="b s" href="#">Повторить через приложение</a></div>
<p>Если приложение не установлено, установите TG Proxy и откройте эту ссылку ещё раз.</p></main>
<script>(()=>{const q=new URLSearchParams(location.search);const h=new URLSearchParams(location.hash.slice(1));const d=h.get('data')||q.get('data')||'';const ok=/^[A-Za-z0-9_-]{1,16384}$/.test(d);const actions=document.getElementById('actions');const error=document.getElementById('error');if(!ok){error.classList.remove('hidden');return}const e=encodeURIComponent(d);const custom='tgproxy://import?data='+e;const intent='intent://import?data='+e+'#Intent;scheme=tgproxy;package=com.dushnyj.tgproxy;end';document.getElementById('open').href=intent;document.getElementById('retry').href=custom;actions.classList.remove('hidden');setTimeout(()=>{location.href=custom},250)})()</script>
</body></html>`

func (s *Server) handleConnectLanding(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// New links keep data in the fragment, which browsers never send to the server. Accept a
	// valid query payload only for backward compatibility with links created by older clients.
	payload := strings.TrimSpace(r.URL.Query().Get("data"))
	if payload != "" && !validSharePayload(payload) {
		http.Error(w, "invalid connection payload", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, connectLandingHTML)
}

func validSharePayload(value string) bool {
	if value == "" || len(value) > maxSharePayloadLength {
		return false
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}
