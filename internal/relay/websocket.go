package relay

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}
	dc, err := strconv.Atoi(r.URL.Query().Get("dc"))
	if err != nil || dc <= 0 {
		http.Error(w, "dc is required", http.StatusBadRequest)
		return
	}
	address := s.cfg.Telegram.AddressForDC(dc)
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
	accept := websocketAccept(r.Header.Get("Sec-WebSocket-Key"))
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_, _ = rw.WriteString("Upgrade: websocket\r\n")
	_, _ = rw.WriteString("Connection: Upgrade\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Protocol: binary\r\n")
	_, _ = rw.WriteString("\r\n")
	_ = rw.Flush()

	bridgeWebSocket(client, rw.Reader, telegram)
}

func bridgeWebSocket(client net.Conn, reader *bufio.Reader, telegram net.Conn) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
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
				if writeFrame(client, 0x2, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		opcode, payload, err := readClientFrame(reader)
		if err != nil {
			closeBoth()
			return
		}
		switch opcode {
		case 0x2:
			if _, err := telegram.Write(payload); err != nil {
				closeBoth()
				return
			}
		case 0x8:
			_ = writeFrame(client, 0x8, payload)
			closeBoth()
			return
		case 0x9:
			_ = writeFrame(client, 0xA, payload)
		}
	}
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") &&
		r.Header.Get("Sec-WebSocket-Key") != ""
}

func websocketAccept(key string) string {
	sum := sha1.Sum([]byte(key + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func readClientFrame(reader io.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, nil, err
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	if !masked {
		return 0, nil, fmt.Errorf("client frame is not masked")
	}
	length := uint64(header[1] & 0x7f)
	if length == 126 {
		ext := make([]byte, 2)
		if _, err := io.ReadFull(reader, ext); err != nil {
			return 0, nil, err
		}
		length = uint64(ext[0])<<8 | uint64(ext[1])
	} else if length == 127 {
		ext := make([]byte, 8)
		if _, err := io.ReadFull(reader, ext); err != nil {
			return 0, nil, err
		}
		length = 0
		for _, b := range ext {
			length = length<<8 | uint64(b)
		}
	}
	if length > 32*1024*1024 {
		return 0, nil, fmt.Errorf("frame too large")
	}
	mask := make([]byte, 4)
	if _, err := io.ReadFull(reader, mask); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return opcode, payload, nil
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
	if _, err := writer.Write(header); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}
