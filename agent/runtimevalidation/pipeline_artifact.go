// pipeline_artifact.go 准备跨平台 runtime validation 远端 pipeline 制品。
//
// 职责：
//   - 用 Go 将 A/B payload 与受控 release 脚本打成扁平 tar.gz
//   - 为每个归档写入独立的小写 SHA-256 sidecar
//   - 把所有临时与最终文件限制在 campaign-owned project 工作区
//
// 边界：
//   - 不执行 shell，不连接远端，也不选择 pipeline transport
//   - 不接受任意归档成员或远端路径，成员清单由 validation bundle 固定
package runtimevalidation

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

type remotePipelineArtifact struct {
	Path         string
	ChecksumPath string
}

type remotePipelineArchiveEntry struct {
	Source string
	Name   string
	Mode   os.FileMode
}

func prepareRemotePipelineArtifacts(bundleRoot, projectRoot, campaignID string) (map[string]remotePipelineArtifact, error) {
	if campaignID == "" || filepath.Base(campaignID) != campaignID || campaignID == "." {
		return nil, fmt.Errorf("remote pipeline campaign ID is invalid")
	}
	pipelineRoot := filepath.Join(bundleRoot, "validation", "pipeline")
	outputRoot := filepath.Join(projectRoot, ".superdev-validation", campaignID, "pipeline-artifacts")
	if err := os.MkdirAll(outputRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create remote pipeline artifact root: %w", err)
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationPipelineArtifact").WithFields(map[string]any{
		"campaign_id": campaignID,
		"output_root": outputRoot,
	})
	log.Info("开始准备跨平台远端 pipeline 制品")

	prepared := make(map[string]remotePipelineArtifact, 2)
	for _, version := range []string{"A", "B"} {
		sourceRoot := filepath.Join(pipelineRoot, "artifacts", "version-"+strings.ToLower(version))
		artifactPath := filepath.Join(outputRoot, "runtime-validation-"+campaignID+"-"+version+".tar.gz")
		checksumPath := artifactPath + ".sha256"
		entries := []remotePipelineArchiveEntry{
			{Source: filepath.Join(sourceRoot, "manifest.json"), Name: "manifest.json", Mode: 0o600},
			{Source: filepath.Join(sourceRoot, "payload.txt"), Name: "payload.txt", Mode: 0o600},
			{Source: filepath.Join(pipelineRoot, "scripts", "remote-release.sh"), Name: "remote-release.sh", Mode: 0o700},
			{Source: filepath.Join(sourceRoot, "version.txt"), Name: "version.txt", Mode: 0o600},
		}
		if err := writeRemotePipelineArchive(artifactPath, entries); err != nil {
			log.WithErr(err).WithField("version", version).Error("远端 pipeline 制品归档失败")
			return nil, fmt.Errorf("package remote pipeline artifact %s: %w", version, err)
		}
		digest, err := hashRegularFile(artifactPath)
		if err != nil {
			log.WithErr(err).WithField("version", version).Error("远端 pipeline 制品摘要失败")
			return nil, fmt.Errorf("hash remote pipeline artifact %s: %w", version, err)
		}
		if err := atomicWriteBytes(checksumPath, []byte(digest), 0o600); err != nil {
			log.WithErr(err).WithField("version", version).Error("远端 pipeline checksum sidecar 写入失败")
			return nil, fmt.Errorf("write remote pipeline checksum %s: %w", version, err)
		}
		prepared[version] = remotePipelineArtifact{Path: artifactPath, ChecksumPath: checksumPath}
		log.WithFields(map[string]any{"version": version, "artifact_sha256": digest}).Info("跨平台远端 pipeline 制品已准备")
	}
	return prepared, nil
}

func writeRemotePipelineArchive(destination string, entries []remotePipelineArchiveEntry) (err error) {
	temporary := destination + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()

	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		if err := writeRemotePipelineArchiveEntry(tarWriter, entry); err != nil {
			_ = tarWriter.Close()
			_ = gzipWriter.Close()
			_ = file.Close()
			return err
		}
	}
	closeErr := errors.Join(tarWriter.Close(), gzipWriter.Close(), file.Sync(), file.Close())
	if closeErr != nil {
		return closeErr
	}
	if err := atomicReplace(temporary, destination); err != nil {
		return err
	}
	removeTemporary = false
	return syncDirectory(filepath.Dir(destination))
}

func writeRemotePipelineArchiveEntry(writer *tar.Writer, entry remotePipelineArchiveEntry) error {
	info, err := os.Lstat(entry.Source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("remote pipeline archive source %s is not a regular file", entry.Name)
	}
	header := &tar.Header{
		Name: entry.Name, Mode: int64(entry.Mode.Perm()), Size: info.Size(),
		ModTime: time.Unix(0, 0).UTC(), AccessTime: time.Time{}, ChangeTime: time.Time{},
		Typeflag: tar.TypeReg,
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	source, err := os.Open(entry.Source)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(writer, source)
	closeErr := source.Close()
	return errors.Join(copyErr, closeErr)
}
