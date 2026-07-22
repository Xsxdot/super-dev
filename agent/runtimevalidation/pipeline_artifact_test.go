// pipeline_artifact_test.go 验证远端 pipeline 的跨平台制品准备合同。
//
// 职责：
//   - 证明 runner 用 Go 生成 A/B tar.gz 与独立 SHA-256 sidecar
//   - 锁定归档根内只包含远端 release 所需的四个文件
//
// 边界：
//   - 不启动 Agent/MCP，也不连接远端 Host
//   - 不测试 pipeline transfer 或远端脚本执行
package runtimevalidation

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrepareRemotePipelineArtifactsCreatesPortableArchives(t *testing.T) {
	t.Parallel()

	bundleRoot := t.TempDir()
	projectRoot := t.TempDir()
	writeRemotePipelineTestSources(t, bundleRoot)

	prepared, err := prepareRemotePipelineArtifacts(bundleRoot, projectRoot, "rv-darwin-arm64-20260716T120000Z-abcdef")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"A", "B"}, mapKeys(prepared))

	for version, artifact := range prepared {
		require.FileExists(t, artifact.Path, version)
		require.FileExists(t, artifact.ChecksumPath, version)
		digest, err := hashRegularFile(artifact.Path)
		require.NoError(t, err)
		sidecar, err := os.ReadFile(artifact.ChecksumPath)
		require.NoError(t, err)
		require.Equal(t, digest, string(sidecar), version)
		require.Equal(t, []string{"manifest.json", "payload.txt", "remote-release.sh", "version.txt"}, archiveEntryNames(t, artifact.Path), version)
	}
}

func writeRemotePipelineTestSources(t *testing.T, bundleRoot string) {
	t.Helper()
	pipelineRoot := filepath.Join(bundleRoot, "validation", "pipeline")
	for _, version := range []string{"A", "B"} {
		source := filepath.Join(pipelineRoot, "artifacts", "version-"+strings.ToLower(version))
		require.NoError(t, os.MkdirAll(source, 0o755))
		for name, content := range map[string]string{
			"manifest.json": `{"version":"` + version + `"}`,
			"payload.txt":   "payload-" + version,
			"version.txt":   version + "\n",
		} {
			require.NoError(t, os.WriteFile(filepath.Join(source, name), []byte(content), 0o600))
		}
	}
	scriptRoot := filepath.Join(pipelineRoot, "scripts")
	require.NoError(t, os.MkdirAll(scriptRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(scriptRoot, "remote-release.sh"), []byte("#!/bin/sh\n"), 0o700))
}

func mapKeys(values map[string]remotePipelineArtifact) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func archiveEntryNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	var names []string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, header.Name)
	}
	sort.Strings(names)
	return names
}
