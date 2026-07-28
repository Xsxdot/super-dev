package sshkeys

import (
	"os"
	"path/filepath"
	"testing"
)

const rsaHeader = "-----BEGIN RSA PRIVATE KEY-----\n"
const opensshHeader = "-----BEGIN OPENSSH PRIVATE KEY-----\n"

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func names(keys []Key) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Name)
	}
	return out
}

func TestScanSelectsOnlyPrivateKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "id_rsa", rsaHeader+"AAAA\n-----END RSA PRIVATE KEY-----\n")
	writeFile(t, dir, "id_rsa.pub", "ssh-rsa AAAA user@host\n")
	writeFile(t, dir, "known_hosts", "github.com ssh-rsa AAAA\n")
	writeFile(t, dir, "known_hosts.old", "github.com ssh-rsa AAAA\n")
	writeFile(t, dir, "config", "Host foo\n  HostName 10.0.0.1\n")
	writeFile(t, dir, "authorized_keys", "ssh-rsa AAAA user@host\n")
	writeFile(t, dir, "agent", "not a key at all\n")

	keys, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := names(keys)
	if len(got) != 1 || got[0] != "id_rsa" {
		t.Fatalf("expected only id_rsa, got %v", got)
	}
	if keys[0].Path != filepath.Join(dir, "id_rsa") {
		t.Fatalf("unexpected path %q", keys[0].Path)
	}
	if keys[0].Type != "rsa" {
		t.Fatalf("expected type rsa, got %q", keys[0].Type)
	}
	if keys[0].Encrypted {
		t.Fatal("plain key must not be reported as encrypted")
	}
}

// 非常规命名也必须被识别：判据是内容而非文件名。
func TestScanDetectsNonConventionalName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "id_ed25519_work", opensshHeader+"AAAA\n")

	keys, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "id_ed25519_work" {
		t.Fatalf("expected id_ed25519_work, got %v", names(keys))
	}
	if keys[0].Type != "ed25519" {
		t.Fatalf("expected type ed25519, got %q", keys[0].Type)
	}
}

func TestScanDetectsEncryptedKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "legacy", rsaHeader+"Proc-Type: 4,ENCRYPTED\nDEK-Info: AES-128-CBC,ABC\n\nAAAA\n")

	keys, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(keys) != 1 || !keys[0].Encrypted {
		t.Fatalf("expected encrypted legacy key, got %+v", keys)
	}
}

// 目录不存在是首次使用的正常场景，不应报错。
func TestScanMissingDirReturnsEmpty(t *testing.T) {
	keys, err := Scan(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected empty, got %v", names(keys))
	}
}

// 单个坏文件不能让整个列表不可用。
func TestScanSkipsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "id_rsa", rsaHeader+"AAAA\n")
	writeFile(t, dir, "secret", rsaHeader+"AAAA\n")
	if err := os.Chmod(filepath.Join(dir, "secret"), 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "secret"), 0o600) })

	keys, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "id_rsa" {
		t.Fatalf("expected only id_rsa to survive, got %v", names(keys))
	}
}

func TestScanSkipsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	big := make([]byte, maxKeyFileSize+1)
	copy(big, rsaHeader)
	if err := os.WriteFile(filepath.Join(dir, "huge"), big, 0o600); err != nil {
		t.Fatalf("write huge: %v", err)
	}

	keys, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected oversize file skipped, got %v", names(keys))
	}
}

func TestScanSortsByName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b_key", rsaHeader+"AAAA\n")
	writeFile(t, dir, "a_key", rsaHeader+"AAAA\n")

	keys, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := names(keys)
	if len(got) != 2 || got[0] != "a_key" || got[1] != "b_key" {
		t.Fatalf("expected sorted by name, got %v", got)
	}
}
