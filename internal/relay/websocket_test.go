package relay_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
	"github.com/Dushnyj/TG-Proxy-Relay/internal/relay"
)

func TestWebSocketBridgeForwardsBinaryFramesToTelegramConnection(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	server := newBridgeTestServer(t, appSide)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()

	if err := writeClientFrame(wsConn, []byte("hello telegram")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("hello telegram"))
	if _, err := io.ReadFull(telegramSide, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello telegram" {
		t.Fatalf("telegram received %q", string(buf))
	}

	go func() {
		_, _ = telegramSide.Write([]byte("hello app"))
	}()
	payload := readServerFrame(t, wsConn)
	if string(payload) != "hello app" {
		t.Fatalf("app received %q", string(payload))
	}
}

func TestWebSocketBridgeAcceptsLargeBinaryFrames(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	server := newBridgeTestServer(t, appSide)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()

	payload := make([]byte, 70_000)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	go func() {
		_ = writeClientFrame(wsConn, payload)
	}()
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(telegramSide, buf); err != nil {
		t.Fatal(err)
	}
	for i := range payload {
		if buf[i] != payload[i] {
			t.Fatalf("payload mismatch at %d", i)
		}
	}
}

func newBridgeTestServer(t *testing.T, telegramConn net.Conn) *relay.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.Tokens = []config.Token{{Name: "phone", Hash: config.TokenHash("phone-token")}}
	cfg.Telegram.DCMap = map[int]string{2: "149.154.167.220"}
	server, err := relay.NewServer(cfg, relay.WithDialer(singleConnDialer{conn: telegramConn}))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type singleConnDialer struct {
	conn net.Conn
}

func (d singleConnDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	return d.conn, nil
}

func dialWebSocket(t *testing.T, rawURL string, path string, token string) net.Conn {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", parsed.Host, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: binary\r\n\r\n",
		path, parsed.Host, token, key)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("websocket status = %s", status)
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	if expected := acceptKey(key); expected == "" {
		t.Fatal("empty accept key")
	}
	return &bufferedConn{Conn: conn, reader: reader}
}

func writeClientFrame(conn net.Conn, payload []byte) error {
	mask := []byte{1, 2, 3, 4}
	frame := []byte{0x82}
	if len(payload) < 126 {
		frame = append(frame, byte(0x80|len(payload)))
	} else if len(payload) <= 0xffff {
		frame = append(frame, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	} else {
		length := uint64(len(payload))
		frame = append(frame, 0x80|127,
			byte(length>>56),
			byte(length>>48),
			byte(length>>40),
			byte(length>>32),
			byte(length>>24),
			byte(length>>16),
			byte(length>>8),
			byte(length))
	}
	frame = append(frame, mask...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	_, err := conn.Write(frame)
	return err
}

func readServerFrame(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatal(err)
	}
	if header[0]&0x0f != 2 {
		t.Fatalf("unexpected opcode: %x", header[0]&0x0f)
	}
	length := int(header[1] & 0x7f)
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func acceptKey(key string) string {
	hash := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(hash[:])
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}
