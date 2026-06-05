// Package store_test 验证本地制品仓库持久化。
package store_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

func TestArtifactStorePutGetListAndPruneFile(t *testing.T) {
	s, err := store.New(t.TempDir() + "/logs.db")
	require.NoError(t, err)
	defer s.Close()

	ctx := context.Background()
	ref, err := s.PutArtifact(ctx, "p1", "deploy", model.ArtifactRef{
		Version: "v1",
		Kind:    model.ArtifactKindFile,
		Meta:    map[string]string{"commit": "abc"},
	}, strings.NewReader("hello"))
	require.NoError(t, err)
	assert.Equal(t, "v1", ref.Version)
	require.FileExists(t, ref.Location)

	got, err := s.GetArtifact(ctx, "p1", "deploy", "v1")
	require.NoError(t, err)
	assert.Equal(t, model.ArtifactKindFile, got.Kind)
	assert.Equal(t, "abc", got.Meta["commit"])
	data, err := os.ReadFile(got.Location)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))

	list, err := s.ListArtifacts(ctx, "p1", "deploy")
	require.NoError(t, err)
	require.Len(t, list, 1)

	_, err = s.PutArtifact(ctx, "p1", "deploy", model.ArtifactRef{Version: "v2", Kind: model.ArtifactKindFile}, strings.NewReader("new"))
	require.NoError(t, err)
	require.NoError(t, s.PruneArtifacts(ctx, "p1", "deploy", 1))
	_, err = s.GetArtifact(ctx, "p1", "deploy", "v1")
	assert.ErrorIs(t, err, store.ErrArtifactNotFound)
}

func TestArtifactStoreImageUsesNilBody(t *testing.T) {
	s, err := store.New(t.TempDir() + "/logs.db")
	require.NoError(t, err)
	defer s.Close()

	ref, err := s.PutArtifact(context.Background(), "p1", "deploy", model.ArtifactRef{
		Version:  "v1",
		Kind:     model.ArtifactKindImage,
		Location: "registry.example.com/api:v1",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, "registry.example.com/api:v1", ref.Location)
}
