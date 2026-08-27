package relay

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSocketUpgradeRequiresBinarySubprotocol(t *testing.T) {
	req := httptest.NewRequest("GET", "http://relay/apiws?dc=2&media=0", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "keep-alive, Upgrade")
	req.Header.Set("Sec-WebSocket-Key", "test-key")
	req.Header.Set("Sec-WebSocket-Version", "13")
	if isWebSocketUpgrade(req) {
		t.Fatal("upgrade without binary subprotocol must be rejected")
	}
	req.Header.Set("Sec-WebSocket-Protocol", "chat, binary")
	if !isWebSocketUpgrade(req) {
		t.Fatal("binary subprotocol must be accepted")
	}
}

func TestWebSocketNegotiationPrefersV2AndKeepsLegacyBinary(t *testing.T) {
	req := httptest.NewRequest("GET", "http://relay/apiws?dc=2&media=0", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "binary, tgproxy-relay.v2")
	if got := selectedWebSocketSubprotocol(req); got != "tgproxy-relay.v2" {
		t.Fatalf("selected subprotocol = %q", got)
	}
	req.Header.Set("Sec-WebSocket-Protocol", "binary")
	if got := selectedWebSocketSubprotocol(req); got != "binary" {
		t.Fatalf("legacy selected subprotocol = %q", got)
	}
}

func TestRouteQueryRejectsUnknownAndDuplicateParameters(t *testing.T) {
	valid := httptest.NewRequest("GET", "http://relay/apiws?dc=2&media=1&test=0", nil)
	if err := validateRouteQuery(valid); err != nil {
		t.Fatalf("valid route query rejected: %v", err)
	}
	unknown := httptest.NewRequest("GET", "http://relay/apiws?dc=2&media=1&dst=127.0.0.1", nil)
	if err := validateRouteQuery(unknown); err == nil {
		t.Fatal("unknown destination parameter was accepted")
	}
	duplicate := httptest.NewRequest("GET", "http://relay/apiws?dc=2&dc=3&media=1", nil)
	if err := validateRouteQuery(duplicate); err == nil {
		t.Fatal("duplicate dc parameter was accepted")
	}
}

func TestClientFrameReaderReassemblesFragmentedBinaryWithInterleavedPing(t *testing.T) {
	wire := append(maskedTestFrame(false, opBinary, []byte("hello ")),
		maskedTestFrame(true, opPing, []byte("alive"))...)
	wire = append(wire, maskedTestFrame(true, opContinuation, []byte("telegram"))...)
	reader := clientFrameReader{reader: bytes.NewReader(wire), maxMessageBytes: 1024}

	opcode, payload, err := reader.nextEvent()
	if err != nil || opcode != opPing || string(payload) != "alive" {
		t.Fatalf("ping event = opcode %d payload %q err %v", opcode, payload, err)
	}
	opcode, payload, err = reader.nextEvent()
	if err != nil || opcode != opBinary || string(payload) != "hello telegram" {
		t.Fatalf("binary event = opcode %d payload %q err %v", opcode, payload, err)
	}
}

func TestReadClientFrameRejectsOversizeBeforePayloadAllocation(t *testing.T) {
	wire := []byte{0x82, 0x80 | 127}
	length := make([]byte, 8)
	binary.BigEndian.PutUint64(length, 16*1024*1024+1)
	wire = append(wire, length...)

	_, err := readClientFrame(bytes.NewReader(wire), 16*1024*1024)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected frame too large, got %v", err)
	}
	code, _ := closeForReadError(err)
	if code != 1009 {
		t.Fatalf("oversize close code = %d", code)
	}
}

func TestReadClientFrameRejectsNonMinimalLengths(t *testing.T) {
	mask := []byte{1, 2, 3, 4}
	shortAs16 := append([]byte{0x82, 0x80 | 126, 0x00, 0x7d}, mask...)
	if _, err := readClientFrame(bytes.NewReader(shortAs16), 1024); err == nil {
		t.Fatal("expected non-minimal 16-bit length rejection")
	}
	mediumAs64 := []byte{0x82, 0x80 | 127, 0, 0, 0, 0, 0, 0, 0xff, 0xff}
	mediumAs64 = append(mediumAs64, mask...)
	if _, err := readClientFrame(bytes.NewReader(mediumAs64), 1024*1024); err == nil {
		t.Fatal("expected non-minimal 64-bit length rejection")
	}
}

func TestValidateClosePayloadRejectsReservedCodeAndInvalidUTF8(t *testing.T) {
	reserved := []byte{0x03, 0xed} // 1005 must never appear on the wire.
	if code, _ := closeForReadError(validateClosePayload(reserved)); code != 1002 {
		t.Fatalf("reserved close code response = %d", code)
	}
	invalidUTF8 := []byte{0x03, 0xe8, 0xff}
	if code, _ := closeForReadError(validateClosePayload(invalidUTF8)); code != 1007 {
		t.Fatalf("invalid UTF-8 response = %d", code)
	}
	valid := []byte{0x03, 0xe8, 'o', 'k'}
	if err := validateClosePayload(valid); err != nil {
		t.Fatalf("valid close payload rejected: %v", err)
	}
}

func TestReadClientFrameRejectsUnmaskedAndFragmentedControlFrames(t *testing.T) {
	if _, err := readClientFrame(bytes.NewReader([]byte{0x82, 0x00}), 1024); err == nil {
		t.Fatal("expected unmasked client frame rejection")
	}
	if _, err := readClientFrame(bytes.NewReader(maskedTestFrame(false, opPing, nil)), 1024); err == nil {
		t.Fatal("expected fragmented control frame rejection")
	}
}

func TestWriteAllHandlesShortWritesAndRejectsNoProgress(t *testing.T) {
	writer := &shortWriter{limit: 3}
	payload := []byte("telegram-media-payload")
	if err := writeAll(writer, payload); err != nil {
		t.Fatalf("writeAll returned error: %v", err)
	}
	if !bytes.Equal(writer.data, payload) {
		t.Fatalf("payload mismatch: %q", writer.data)
	}
	if err := writeAll(zeroWriter{}, payload); err != io.ErrShortWrite {
		t.Fatalf("zero progress error = %v", err)
	}
}

type shortWriter struct {
	limit int
	data  []byte
}

func (w *shortWriter) Write(payload []byte) (int, error) {
	count := len(payload)
	if count > w.limit {
		count = w.limit
	}
	w.data = append(w.data, payload[:count]...)
	return count, nil
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func maskedTestFrame(fin bool, opcode byte, payload []byte) []byte {
	first := opcode
	if fin {
		first |= 0x80
	}
	mask := []byte{1, 2, 3, 4}
	frame := []byte{first, 0x80 | byte(len(payload))}
	frame = append(frame, mask...)
	for i, value := range payload {
		frame = append(frame, value^mask[i%4])
	}
	return frame
}
