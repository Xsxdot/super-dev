package dbprovision

import (
	"reflect"
	"testing"
)

func TestFreeDBIndexesExcludesZeroOccupiedAndTaken(t *testing.T) {
	got := freeDBIndexes(8, []int{1, 3}, []string{"db5"})
	want := []int{2, 4, 6, 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("分配池不对: got=%v want=%v", got, want)
	}
}

func TestFreeDBIndexesNeverReturnsZero(t *testing.T) {
	for _, index := range freeDBIndexes(16, nil, nil) {
		if index == 0 {
			t.Fatal("db0 永远不能进入分配池")
		}
	}
}

func TestFreeDBIndexesEmptyWhenAllTaken(t *testing.T) {
	if got := freeDBIndexes(3, []int{1, 2}, nil); len(got) != 0 {
		t.Fatalf("全被占用时分配池应为空: %v", got)
	}
}

func TestFreeDBIndexesIgnoresMalformedTaken(t *testing.T) {
	got := freeDBIndexes(4, nil, []string{"db2", "garbage", ""})
	want := []int{1, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("脏数据应被跳过: got=%v want=%v", got, want)
	}
}
