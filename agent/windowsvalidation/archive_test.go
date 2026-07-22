// archive_test.go 验证 ZIP 内容在相同输入下字节级确定。
//
// 职责：
//   - 防止源文件时间和遍历顺序污染可复制包摘要

// 边界：
//   - 不交叉编译真实驱动
package windowsvalidation

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateDeterministicZip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	one := filepath.Join(root, "one.zip")
	two := filepath.Join(root, "two.zip")
	if err := CreateDeterministicZip(source, one); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_ = os.Chtimes(filepath.Join(source, "b.txt"), now, now)
	if err := CreateDeterministicZip(source, two); err != nil {
		t.Fatal(err)
	}
	oneBytes, _ := os.ReadFile(one)
	twoBytes, _ := os.ReadFile(two)
	if !bytes.Equal(oneBytes, twoBytes) {
		t.Fatal("deterministic ZIP bytes differ")
	}
}
