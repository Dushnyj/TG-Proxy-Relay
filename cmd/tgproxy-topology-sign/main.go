package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Dushnyj/TG-Proxy-Relay/internal/topology"
)

func main() {
	input := flag.String("in", "", "unsigned topology payload JSON")
	output := flag.String("out", "", "signed bundle output path")
	keyPath := flag.String("key", "", "base64 Ed25519 private-key file")
	generatePrefix := flag.String("generate-key-prefix", "", "generate <prefix>.private and <prefix>.public")
	flag.Parse()
	if *generatePrefix != "" {
		if err := generateKeys(*generatePrefix); err != nil {
			fatal(err)
		}
		return
	}
	if *input == "" || *output == "" || *keyPath == "" {
		fatal(fmt.Errorf("-in, -out and -key are required"))
	}
	data, err := os.ReadFile(*input)
	if err != nil {
		fatal(err)
	}
	var payload topology.Payload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		fatal(fmt.Errorf("input must contain exactly one JSON object"))
	}
	keyData, err := os.ReadFile(*keyPath)
	if err != nil {
		fatal(err)
	}
	privateKey, err := decodePrivateKey(string(keyData))
	if err != nil {
		fatal(err)
	}
	bundle, err := topology.Sign(payload, privateKey, time.Now().UTC())
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*output, append(bundle, '\n'), 0o600); err != nil {
		fatal(err)
	}
}

func generateKeys(prefix string) error {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privatePath := prefix + ".private"
	publicPath := prefix + ".public"
	if err := writeExclusive(privatePath, []byte(
		base64.RawStdEncoding.EncodeToString(privateKey)+"\n"), 0o600); err != nil {
		return err
	}
	if err := writeExclusive(publicPath, []byte(
		base64.RawStdEncoding.EncodeToString(publicKey)+"\n"), 0o644); err != nil {
		_ = os.Remove(privatePath)
		return err
	}
	return nil
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}

func decodePrivateKey(value string) (ed25519.PrivateKey, error) {
	raw := strings.TrimSpace(value)
	decoded, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(raw)
	}
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("key file must contain a base64 Ed25519 private key")
	}
	return ed25519.PrivateKey(decoded), nil
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "tgproxy-topology-sign:", err)
	os.Exit(1)
}
