package relay_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
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

func TestWebSocketCustomPathRoutesTestDcToTestAddress(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	dialer := &addressCapturingDialer{conn: appSide, addresses: make(chan string, 1)}
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:18080"
	cfg.WebSocket.Path = "/private-relay"
	cfg.Tokens = []config.Token{{Name: "phone", Hash: config.TokenHash("phone-token")}}
	cfg.Telegram.TestDCMap = map[int]string{2: "149.154.167.40"}
	server, err := relay.NewServer(cfg, relay.WithDialer(dialer))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL,
		"/private-relay?dc=2&media=1&test=1", "phone-token")
	defer wsConn.Close()
	select {
	case address := <-dialer.addresses:
		if address != "149.154.167.40:443" {
			t.Fatalf("test DC address = %q", address)
		}
	case <-time.After(time.Second):
		t.Fatal("relay did not dial the test DC")
	}
}

func TestWebSocketBridgeClosesIdleConnectionAfterConfiguredTimeout(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	server := newBridgeTestServerWithConfig(t, appSide, func(cfg *config.Config) {
		cfg.Telegram.IdleTimeoutSec = 1
	})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()

	if err := wsConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err := wsConn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected idle websocket connection to close")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("idle websocket stayed open until test read deadline")
	}
}

func TestWebSocketHeartbeatKeepsConnectionWhenClientReturnsPong(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	server := newBridgeTestServerWithConfig(t, appSide, func(cfg *config.Config) {
		cfg.Telegram.IdleTimeoutSec = 0
		cfg.WebSocket.PingIntervalSec = 1
		cfg.WebSocket.PongTimeoutSec = 1
	})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()
	if err := wsConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	opcode, ping := readServerRawFrame(t, wsConn)
	if opcode != 0x9 {
		t.Fatalf("expected ping, got opcode %x", opcode)
	}
	if err := writeClientOpcodeFrame(wsConn, 0xA, ping); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = telegramSide.Write([]byte("still alive")) }()
	opcode, payload := readServerRawFrame(t, wsConn)
	if opcode != 0x2 || string(payload) != "still alive" {
		t.Fatalf("expected binary response after pong, opcode=%x payload=%q", opcode, payload)
	}
}

func TestWebSocketHeartbeatClosesConnectionWithoutPong(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	server := newBridgeTestServerWithConfig(t, appSide, func(cfg *config.Config) {
		cfg.Telegram.IdleTimeoutSec = 0
		cfg.WebSocket.PingIntervalSec = 1
		cfg.WebSocket.PongTimeoutSec = 1
	})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()
	if err := wsConn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	opcode, _ := readServerRawFrame(t, wsConn)
	if opcode != 0x9 {
		t.Fatalf("expected ping, got opcode %x", opcode)
	}
	_, err := wsConn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("expected connection to close after pong timeout")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("connection remained open beyond pong timeout")
	}
}

func TestWebSocketHeartbeatRejectsPongForDifferentPing(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	server := newBridgeTestServerWithConfig(t, appSide, func(cfg *config.Config) {
		cfg.Telegram.IdleTimeoutSec = 0
		cfg.WebSocket.PingIntervalSec = 1
		cfg.WebSocket.PongTimeoutSec = 1
	})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()
	if err := wsConn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	opcode, ping := readServerRawFrame(t, wsConn)
	if opcode != opPingForTest || len(ping) != 8 {
		t.Fatalf("expected 8-byte ping, opcode=%x payload=%x", opcode, ping)
	}
	wrong := append([]byte(nil), ping...)
	wrong[len(wrong)-1] ^= 0x7f
	if err := writeClientOpcodeFrame(wsConn, 0xA, wrong); err != nil {
		t.Fatal(err)
	}
	_, err := wsConn.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("wrong pong kept websocket alive")
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("websocket remained open after wrong pong")
	}
}

func TestWebSocketBridgeSendsNormalCloseWhenTelegramCloses(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	server := newBridgeTestServer(t, appSide)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()
	if err := wsConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_ = telegramSide.Close()
	opcode, payload := readServerRawFrame(t, wsConn)
	if opcode != 0x8 {
		t.Fatalf("expected close frame, got opcode %x", opcode)
	}
	if len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != 1000 {
		t.Fatalf("expected normal close 1000, payload=%x", payload)
	}
}

func TestWebSocketBridgeSendsServerErrorCloseWhenTelegramWriteFails(t *testing.T) {
	appSide, telegramSide := net.Pipe()
	defer telegramSide.Close()
	server := newBridgeTestServer(t, failingWriteConn{Conn: appSide})
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	wsConn := dialWebSocket(t, ts.URL, "/apiws?dc=2&media=0", "phone-token")
	defer wsConn.Close()
	if err := wsConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := writeClientFrame(wsConn, []byte("cannot reach telegram")); err != nil {
		t.Fatal(err)
	}
	opcode, payload := readServerRawFrame(t, wsConn)
	if opcode != 0x8 {
		t.Fatalf("expected close frame, got opcode %x", opcode)
	}
	if len(payload) < 2 || binary.BigEndian.Uint16(payload[:2]) != 1011 {
		t.Fatalf("expected server error close 1011, payload=%x", payload)
	}
}

const opPingForTest byte = 0x9

func newBridgeTestServer(t *testing.T, telegramConn net.Conn) *relay.Server {
	return newBridgeTestServerWithConfig(t, telegramConn, nil)
}

func newBridgeTestServerWithConfig(t *testing.T, telegramConn net.Conn, mutate func(*config.Config)) *relay.Server {
	t.Helper()
	cfg := config.Default()
	cfg.Listen = "127.0.0.1:18080"
	cfg.Tokens = []config.Token{{Name: "phone", Hash: config.TokenHash("phone-token")}}
	cfg.Telegram.DCMap = map[int]string{2: "149.154.167.220"}
	if mutate != nil {
		mutate(&cfg)
	}
	server, err := relay.NewServer(cfg, relay.WithDialer(singleConnDialer{conn: telegramConn}))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

type singleConnDialer struct {
	conn net.Conn
}

type failingWriteConn struct {
	net.Conn
}

func (failingWriteConn) Write([]byte) (int, error) {
	return 0, errors.New("injected telegram write failure")
}

type addressCapturingDialer struct {
	conn      net.Conn
	addresses chan string
}

func (d *addressCapturingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.addresses <- address
	return d.conn, nil
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
	return writeClientOpcodeFrame(conn, 0x2, payload)
}

func writeClientOpcodeFrame(conn net.Conn, opcode byte, payload []byte) error {
	mask := []byte{1, 2, 3, 4}
	frame := []byte{0x80 | opcode}
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

func readServerRawFrame(t *testing.T, conn net.Conn) (byte, []byte) {
	t.Helper()
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		t.Fatal(err)
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(conn, ext); err != nil {
			t.Fatal(err)
		}
		length = uint64(ext[0])<<8 | uint64(ext[1])
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(conn, ext); err != nil {
			t.Fatal(err)
		}
		for _, value := range ext {
			length = length<<8 | uint64(value)
		}
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(conn, payload); err != nil {
		t.Fatal(err)
	}
	return header[0] & 0x0f, payload
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
