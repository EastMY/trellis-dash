package codexusage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func readOwnCache(path string) (map[string]fileData, bool, error) {
	if path == "" {
		return make(map[string]fileData), false, nil
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return make(map[string]fileData), false, nil
	}
	if err != nil {
		return make(map[string]fileData), false, err
	}
	defer file.Close()
	var index cacheIndex
	if err := json.NewDecoder(bufio.NewReader(file)).Decode(&index); err != nil {
		return make(map[string]fileData), true, err
	}
	if index.Version != cacheVersion {
		return make(map[string]fileData), true, fmt.Errorf("缓存版本 %d 不受支持", index.Version)
	}
	files := make(map[string]fileData, len(index.Files))
	for _, item := range index.Files {
		if item.PathKey != "" {
			files[item.PathKey] = item
		}
	}
	return files, true, nil
}

func writeOwnCache(path string, files map[string]fileData) error {
	if path == "" {
		return nil
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	ordered := make([]fileData, 0, len(files))
	for _, item := range files {
		ordered = append(ordered, item)
	}
	sortFileData(ordered)

	temporary, err := os.CreateTemp(parent, ".codex-usage-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	// Encoder 背后的缓冲必须显式 flush，因此这里改用可控 writer。
	writer := bufio.NewWriterSize(temporary, 256<<10)
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(cacheIndex{Version: cacheVersion, Files: ordered}); err != nil {
		temporary.Close()
		return err
	}
	if err := writer.Flush(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

type referenceCache struct {
	Version int                 `json:"version"`
	Files   []referenceFileData `json:"files"`
}

type referenceFingerprint struct {
	Size            int64 `json:"s"`
	ModifiedSeconds int64 `json:"t"`
	ModifiedNanos   int64 `json:"n"`
}

type referenceFileData struct {
	PathKey     string                `json:"p"`
	Fingerprint *referenceFingerprint `json:"f"`
	SessionID   string                `json:"i"`
	EventCount  int                   `json:"n"`
	Models      []string              `json:"m"`
	Events      [][]int64             `json:"e"`
}

func readReferenceCache(codexHome string) map[string]referenceFileData {
	path := filepath.Join(codexHome, "cache", "codex-token", "index-v1.json")
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var index referenceCache
	if json.NewDecoder(bufio.NewReader(file)).Decode(&index) != nil || index.Version != 1 {
		return nil
	}
	files := make(map[string]referenceFileData, len(index.Files))
	for _, item := range index.Files {
		if item.PathKey != "" {
			files[item.PathKey] = item
		}
	}
	return files
}

func importReferenceFile(ctx context.Context, candidate candidateFile, item referenceFileData, maxLineSize int) (fileData, bool) {
	if item.Fingerprint == nil || candidate.Fingerprint != (fingerprint{
		Size: item.Fingerprint.Size, ModifiedSeconds: item.Fingerprint.ModifiedSeconds,
		ModifiedNanos: item.Fingerprint.ModifiedNanos,
	}) {
		return fileData{}, false
	}
	sessionID, cwd, err := readSessionMetadata(ctx, candidate.Path, maxLineSize)
	if err != nil {
		return fileData{}, false
	}
	if sessionID == "" {
		sessionID = item.SessionID
	}
	result := fileData{
		PathKey: candidate.PathKey, Fingerprint: candidate.Fingerprint,
		SessionID: sessionID, CWD: cwd, EventCount: item.EventCount,
		Events: make([]tokenEvent, 0, len(item.Events)),
	}
	for _, compact := range item.Events {
		if len(compact) != 7 {
			return fileData{}, false
		}
		model := ""
		if compact[1] >= 0 && compact[1] < int64(len(item.Models)) {
			model = item.Models[compact[1]]
		}
		result.Events = append(result.Events, tokenEvent{
			Timestamp: compact[0], Model: model,
			Usage: tokenUsage{
				InputTokens: compact[2], CachedInputTokens: compact[3], OutputTokens: compact[4],
				ReasoningOutputTokens: compact[5], TotalTokens: compact[6],
			},
		})
	}
	return result, true
}
