package dbprovision

import (
	"strings"
	"testing"
)

func TestNewResourceNameShapeAndLimit(t *testing.T) {
	name, err := NewResourceName("Super-Debug 项目")
	if err != nil {
		t.Fatalf("NewResourceName 失败: %v", err)
	}
	if !strings.HasPrefix(name, ResourcePrefix) {
		t.Fatalf("名字必须带前缀 %s，实际 %s", ResourcePrefix, name)
	}
	if len(name) > 63 {
		t.Fatalf("名字超过 PG 标识符上限 63：%d", len(name))
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
			t.Fatalf("名字含非法字符 %q：%s", r, name)
		}
	}
}

func TestNewResourceNameIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		name, err := NewResourceName("tk")
		if err != nil {
			t.Fatalf("NewResourceName 失败: %v", err)
		}
		if seen[name] {
			t.Fatalf("生成了重复名字: %s", name)
		}
		seen[name] = true
	}
}

func TestNewResourceNameTruncatesLongProject(t *testing.T) {
	name, err := NewResourceName(strings.Repeat("x", 200))
	if err != nil {
		t.Fatalf("NewResourceName 失败: %v", err)
	}
	if len(name) > 63 {
		t.Fatalf("超长项目名未被截断：%d", len(name))
	}
}
