package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGenerateKeysDoesNotOverwriteAndKeepsPrivateKeyPrivate(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "topology")
	if err := generateKeys(prefix); err != nil {
		t.Fatal(err)
	}
	privateInfo, err := os.Stat(prefix + ".private")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && privateInfo.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode = %o", privateInfo.Mode().Perm())
	}
	before, err := os.ReadFile(prefix + ".private")
	if err != nil {
		t.Fatal(err)
	}
	if err := generateKeys(prefix); err == nil {
		t.Fatal("existing signing keys were overwritten")
	}
	after, err := os.ReadFile(prefix + ".private")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("private key changed after refused overwrite")
	}
}
