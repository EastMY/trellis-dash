package trellis

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yunnnn/trellis-dash/internal/model"
	"github.com/yunnnn/trellis-dash/internal/streamwalk"
)

type artifactCandidate struct {
	path string
	kind string
	name string
}

func scanArtifacts(ctx context.Context, root, projectID string, item scannedTask, budget *scanBudget) ([]model.Artifact, error) {
	candidates := make([]artifactCandidate, 0, 8)
	for _, fixed := range []struct {
		name string
		kind string
	}{
		{name: "prd.md", kind: "prd"},
		{name: "design.md", kind: "design"},
		{name: "implement.md", kind: "implementation"},
		{name: "info.md", kind: "info"},
	} {
		path := filepath.Join(item.directory, fixed.name)
		_, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, artifactCandidate{path: path, kind: fixed.kind, name: fixed.name})
	}

	researchRoot, exists, err := optionalDirectory(root, filepath.Join(item.directory, "research"))
	if err != nil {
		return nil, err
	}
	if exists {
		walkEntries := 0
		err := streamwalk.Walk(ctx, root, researchRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := contextError(ctx); err != nil {
				return err
			}
			walkEntries++
			if err := budget.addWalk("research"); err != nil {
				return err
			}
			if walkEntries > MaxWalkEntries {
				return fmt.Errorf("%w: research 遍历项超过 %d", ErrResourceLimit, MaxWalkEntries)
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if _, err := resolveExistingPath(root, path); err != nil {
					return err
				}
			}
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				return nil
			}
			relative, err := filepath.Rel(researchRoot, path)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(filepath.Join("research", relative))
			candidates = append(candidates, artifactCandidate{path: path, kind: "research", name: name})
			if len(candidates) > MaxArtifactsPerTask {
				return fmt.Errorf("%w: 任务 %s 的文档超过 %d 个", ErrFileTooLarge, item.task.Key, MaxArtifactsPerTask)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(candidates, func(i, j int) bool { return candidates[i].name < candidates[j].name })
	artifacts := make([]model.Artifact, 0, len(candidates))
	var totalBytes int64
	for _, candidate := range candidates {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		content, info, err := readSafeFile(root, candidate.path, MaxMarkdownBytes)
		if err != nil {
			return nil, err
		}
		totalBytes += int64(len(content))
		if err := budget.addRead(int64(len(content)), "任务文档"); err != nil {
			return nil, err
		}
		if totalBytes > MaxTaskArtifactBytes {
			return nil, fmt.Errorf("%w: 任务 %s 的文档总量超过 %d 字节", ErrFileTooLarge, item.task.Key, MaxTaskArtifactBytes)
		}
		logicalPath := filepath.ToSlash(filepath.Join(item.logicalDir, candidate.name))
		artifacts = append(artifacts, model.Artifact{
			ProjectID:   projectID,
			TaskKey:     item.task.Key,
			Kind:        candidate.kind,
			Name:        candidate.name,
			Path:        logicalPath,
			ContentType: "text/markdown",
			Content:     string(content),
			Size:        int64(len(content)),
			Hash:        hashBytes(content),
			ModifiedAt:  info.ModTime().UTC(),
		})
	}
	return artifacts, nil
}
