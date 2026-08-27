package relay

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/config"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request, token config.Token) {
	if !isWebSocketUpgrade(r) {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	dc, err := strconv.Atoi(r.URL.Query().Get("dc"))
	if err != nil || dc <= 0 {
		http.Error(w, "dc is required", http.StatusBadRequest)
		return
	}
	media := r.URL.Query().Get("media")
	if media != "0" && media != "1" {
		http.Error(w, "media must be 0 or 1", http.StatusBadRequest)
		return
	}
	testRoute := false
	switch r.URL.Query().Get("test") {
	case "", "0":
	case "1":
		testRoute = true
	default:
		http.Error(w, "test must be 0 or 1", http.StatusBadRequest)
		return
	}
	address := s.cfg.Telegram.AddressForRoute(dc, testRoute)
	if address == "" {
		http.Error(w, "unknown dc", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(),
		time.Duration(s.cfg.Telegram.ConnectTimeoutMs)*time.Millisecond)
	defer cancel()
	telegram, err := s.dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		http.Error(w, "telegram dc unavailable", http.StatusBadGateway)
		return
	}
	tuneTCPConnection(telegram)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = telegram.Close()
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	client, rw, err := hijacker.Hijack()
	if err != nil {
		_ = telegram.Close()
		return
	}
	tuneTCPConnection(client)
	unregister, registered := s.control.registerSession(token, r, func() {
		_ = client.Close()
		_ = telegram.Close()
	})
	if !registered {
		_ = client.Close()
		_ = telegram.Close()
		return
	}
	defer unregister()
	accept := websocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_, _ = rw.WriteString("Upgrade: websocket\r\n")
	_, _ = rw.WriteString("Connection: Upgrade\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Protocol: binary\r\n")
	_, _ = rw.WriteString("\r\n")
	if err := rw.Flush(); err != nil {
		_ = client.Close()
		_ = telegram.Close()
		return
	}

	bridgeWebSocket(client, rw.Reader, telegram, s.bridgeSettings())
}

type bridgeSettings struct {
	idleTimeout     time.Duration
	pingInterval    time.Duration
	pongTimeout     time.Duration
	writeTimeout    time.Duration
	maxMessageBytes int
}

func (s *Server) bridgeSettings() bridgeSettings {
	return bridgeSettings{
		idleTimeout:     time.Duration(s.cfg.Telegram.IdleTimeoutSec) * time.Second,
		pingInterval:    time.Duration(s.cfg.WebSocket.PingIntervalSec) * time.Second,
		pongTimeout:     time.Duration(s.cfg.WebSocket.PongTimeoutSec) * time.Second,
		writeTimeout:    time.Duration(s.cfg.WebSocket.WriteTimeoutSec) * time.Second,
		maxMessageBytes: s.cfg.WebSocket.MaxMessageBytes,
	}
}

func bridgeWebSocket(client net.Conn, reader *bufio.Reader, telegram net.Conn, settings bridgeSettings) {
	var once sync.Once
	done := make(chan struct{})
	writer := websocketWriter{conn: client, writeTimeout: settings.writeTimeout}
	frameReader := clientFrameReader{reader: reader, maxMessageBytes: settings.maxMessageBytes}
	now := time.Now().UnixNano()
	var expectedPong atomic.Uint64
	var acknowledgedPong atomic.Uint64
	var lastApplicationActivity atomic.Int64
	lastApplicationActivity.Store(now)

	closeBoth := func() {
		once.Do(func() {
			close(done)
			_ = client.Close()
			_ = telegram.Close()
		})
	}

	go func() {
		defer closeBoth()
		buf := make([]byte, 32*1024)
		for {
			n, err := telegram.Read(buf)
			if n > 0 {
				lastApplicationActivity.Store(time.Now().UnixNano())
				if writer.writeFrame(opBinary, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					_ = writer.writeClose(1000, "telegram closed")
				} else {
					_ = writer.writeClose(1011, "telegram read failed")
				}
				return
			}
		}
	}()

	go heartbeatLoop(done, closeBoth, &writer, &expectedPong, &acknowledgedPong, settings)
	if settings.idleTimeout > 0 {
		go idleWatchdog(done, closeBoth, &lastApplicationActivity, settings.idleTimeout)
	}

	defer closeBoth()
	for {
		opcode, payload, err := frameReader.nextEvent()
		if err != nil {
			code, reason := closeForReadError(err)
			_ = writer.writeClose(code, reason)
			return
		}
		switch opcode {
		case opBinary:
			lastApplicationActivity.Store(time.Now().UnixNano())
			setWriteDeadline(telegram, settings.writeTimeout)
			if err := writeAll(telegram, payload); err != nil {
				_ = writer.writeClose(1011, "telegram write failed")
				return
			}
		case opClose:
			if err := validateClosePayload(payload); err != nil {
				code, reason := closeForReadError(err)
				_ = writer.writeClose(code, reason)
			} else {
				_ = writer.writeFrame(opClose, payload)
			}
			return
		case opPing:
			if err := writer.writeFrame(opPong, payload); err != nil {
				return
			}
		case opPong:
			if len(payload) == 8 {
				sequence := binary.BigEndian.Uint64(payload)
				if sequence != 0 && expectedPong.Load() == sequence {
					acknowledgedPong.Store(sequence)
					expectedPong.CompareAndSwap(sequence, 0)
				}
			}
		default:
			_ = writer.writeClose(1002, "unsupported opcode")
			return
		}
	}
}

func heartbeatLoop(done <-chan struct{}, closeBoth func(), writer *websocketWriter,
	expectedPong, acknowledgedPong *atomic.Uint64, settings bridgeSettings) {
	if settings.pingInterval <= 0 || settings.pongTimeout <= 0 {
		return
	}
	ticker := time.NewTicker(settings.pingInterval)
	defer ticker.Stop()
	var sequence uint64
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			sequence++
			payload := make([]byte, 8)
			binary.BigEndian.PutUint64(payload, sequence)
			expectedPong.Store(sequence)
			if err := writer.writeFrame(opPing, payload); err != nil {
				expectedPong.CompareAndSwap(sequence, 0)
				closeBoth()
				return
			}
			timer := time.NewTimer(settings.pongTimeout)
			select {
			case <-done:
				if !timer.Stop() {
					<-timer.C
				}
				return
			case <-timer.C:
				if acknowledgedPong.Load() != sequence {
					expectedPong.CompareAndSwap(sequence, 0)
					closeBoth()
					return
				}
			}
		}
	}
}

func idleWatchdog(done <-chan struct{}, closeBoth func(), lastActivity *atomic.Int64,
	timeout time.Duration) {
	interval := timeout / 4
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	if interval > 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			last := time.Unix(0, lastActivity.Load())
			if now.Sub(last) >= timeout {
				closeBoth()
				return
			}
		}
	}
}

type websocketWriter struct {
	mu           sync.Mutex
	conn         net.Conn
	writeTimeout time.Duration
}

func (w *websocketWriter) writeFrame(opcode byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	setWriteDeadline(w.conn, w.writeTimeout)
	return writeFrame(w.conn, opcode, payload)
}

func (w *websocketWriter) writeClose(code uint16, reason string) error {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, []byte(reason)...)
	if len(payload) > 125 {
		payload = payload[:125]
	}
	return w.writeFrame(opClose, payload)
}

type clientFrameReader struct {
	reader           io.Reader
	maxMessageBytes  int
	fragmentedOpcode byte
	fragmented       []byte
}

func (r *clientFrameReader) nextEvent() (byte, []byte, error) {
	for {
		frame, err := readClientFrame(r.reader, r.maxMessageBytes)
		if err != nil {
			return 0, nil, err
		}
		switch frame.opcode {
		case opClose, opPing, opPong:
			return frame.opcode, frame.payload, nil
		case opText:
			return 0, nil, protocolError("text frames are not supported")
		case opBinary:
			if r.fragmentedOpcode != 0 {
				return 0, nil, protocolError("new data frame before fragmented message completed")
			}
			if frame.fin {
				return opBinary, frame.payload, nil
			}
			r.fragmentedOpcode = opBinary
			r.fragmented = append(r.fragmented[:0], frame.payload...)
		case opContinuation:
			if r.fragmentedOpcode == 0 {
				return 0, nil, protocolError("unexpected continuation frame")
			}
			if len(r.fragmented) > r.maxMessageBytes-len(frame.payload) {
				return 0, nil, messageTooBig("message too large")
			}
			r.fragmented = append(r.fragmented, frame.payload...)
			if frame.fin {
				message := append([]byte(nil), r.fragmented...)
				r.fragmented = r.fragmented[:0]
				r.fragmentedOpcode = 0
				return opBinary, message, nil
			}
		default:
			return 0, nil, protocolError(fmt.Sprintf("unsupported opcode %d", frame.opcode))
		}
	}
}

type websocketFrame struct {
	fin     bool
	opcode  byte
	payload []byte
}

type websocketReadError struct {
	code   uint16
	reason string
}

func (e *websocketReadError) Error() string { return e.reason }

func protocolError(reason string) error {
	return &websocketReadError{code: 1002, reason: reason}
}

func messageTooBig(reason string) error {
	return &websocketReadError{code: 1009, reason: reason}
}

func invalidPayload(reason string) error {
	return &websocketReadError{code: 1007, reason: reason}
}

func closeForReadError(err error) (uint16, string) {
	var readError *websocketReadError
	if errors.As(err, &readError) {
		return readError.code, readError.reason
	}
	return 1002, "protocol error"
}

func readClientFrame(reader io.Reader, maxMessageBytes int) (websocketFrame, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return websocketFrame{}, err
	}
	if header[0]&0x70 != 0 {
		return websocketFrame{}, protocolError("RSV bits are not supported")
	}
	frame := websocketFrame{fin: header[0]&0x80 != 0, opcode: header[0] & 0x0f}
	if (frame.opcode >= 0x3 && frame.opcode <= 0x7) || frame.opcode >= 0xB {
		return websocketFrame{}, protocolError("reserved opcode")
	}
	if header[1]&0x80 == 0 {
		return websocketFrame{}, protocolError("client frame is not masked")
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(reader, ext); err != nil {
			return websocketFrame{}, err
		}
		length = uint64(ext[0])<<8 | uint64(ext[1])
		if length < 126 {
			return websocketFrame{}, protocolError("non-minimal payload length")
		}
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(reader, ext); err != nil {
			return websocketFrame{}, err
		}
		if ext[0]&0x80 != 0 {
			return websocketFrame{}, protocolError("invalid 64-bit payload length")
		}
		for _, b := range ext {
			length = length<<8 | uint64(b)
		}
		if length <= 0xffff {
			return websocketFrame{}, protocolError("non-minimal payload length")
		}
	}
	control := frame.opcode >= opClose
	if control && (!frame.fin || length > 125) {
		return websocketFrame{}, protocolError("invalid control frame")
	}
	if maxMessageBytes <= 0 || length > uint64(maxMessageBytes) {
		return websocketFrame{}, messageTooBig("message too large")
	}
	mask := make([]byte, 4)
	if _, err := io.ReadFull(reader, mask); err != nil {
		return websocketFrame{}, err
	}
	frame.payload = make([]byte, int(length))
	if _, err := io.ReadFull(reader, frame.payload); err != nil {
		return websocketFrame{}, err
	}
	for i := range frame.payload {
		frame.payload[i] ^= mask[i%4]
	}
	return frame, nil
}

func validateClosePayload(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	if len(payload) == 1 {
		return protocolError("invalid close payload")
	}
	code := binary.BigEndian.Uint16(payload[:2])
	if !validCloseCode(code) {
		return protocolError("invalid close code")
	}
	if !utf8.Valid(payload[2:]) {
		return invalidPayload("invalid close reason")
	}
	return nil
}

func validCloseCode(code uint16) bool {
	if code >= 3000 && code <= 4999 {
		return true
	}
	if code < 1000 || code >= 1016 {
		return false
	}
	switch code {
	case 1004, 1005, 1006, 1015:
		return false
	default:
		return true
	}
}

func setWriteDeadline(conn net.Conn, timeout time.Duration) {
	if timeout <= 0 {
		_ = conn.SetWriteDeadline(time.Time{})
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		r.Header.Get("Sec-WebSocket-Key") != "" &&
		r.Header.Get("Sec-WebSocket-Version") == "13" &&
		headerContainsToken(r.Header.Get("Sec-WebSocket-Protocol"), "binary")
}

func headerContainsToken(value string, token string) bool {
	for _, item := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(item), token) {
			return true
		}
	}
	return false
}

func tuneTCPConnection(conn net.Conn) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return
	}
	_ = tcp.SetNoDelay(true)
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	_ = tcp.SetReadBuffer(512 * 1024)
	_ = tcp.SetWriteBuffer(512 * 1024)
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func writeFrame(writer io.Writer, opcode byte, payload []byte) error {
	header := []byte{0x80 | opcode}
	length := len(payload)
	if length < 126 {
		header = append(header, byte(length))
	} else if length <= 0xffff {
		header = append(header, 126, byte(length>>8), byte(length))
	} else {
		header = append(header, 127,
			byte(uint64(length)>>56),
			byte(uint64(length)>>48),
			byte(uint64(length)>>40),
			byte(uint64(length)>>32),
			byte(uint64(length)>>24),
			byte(uint64(length)>>16),
			byte(uint64(length)>>8),
			byte(uint64(length)))
	}
	if err := writeAll(writer, header); err != nil {
		return err
	}
	return writeAll(writer, payload)
}

func writeAll(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		written, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[written:]
	}
	return nil
}
