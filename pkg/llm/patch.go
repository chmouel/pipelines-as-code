package llm

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	patchBeginMarker = "===LLM_PATCH_BEGIN==="
	patchEndMarker   = "===LLM_PATCH_END==="

	patchVersion1   = 1
	patchFormatDiff = "git-diff"
	patchEncoding   = "gzip+base64"
)

// MachinePatchMetadata holds the parsed metadata from a machine patch block.
// The full encoded payload is never stored here; it is retrieved on demand from logs.
type MachinePatchMetadata struct {
	Version    int
	Format     string
	Encoding   string
	BaseSHA    string
	Role       string
	ChunkCount int
	Available  bool
}

type rawPatchBlock struct {
	version    int
	format     string
	encoding   string
	baseSHA    string
	role       string
	chunkCount int
	chunkIdx   int
	data       string
}

// extractMachinePatchBlocks scans log text for ===LLM_PATCH_BEGIN=== / ===LLM_PATCH_END===
// markers and returns one rawPatchBlock per marker pair found.
// It only searches the portion of the log AFTER ===ANALYSIS_END=== so that patch markers
// embedded in the analysis envelope JSON content or in raw backend stdout (via tee) are ignored.
func extractMachinePatchBlocks(logText string) ([]rawPatchBlock, error) {
	const analysisEndMarker = "===ANALYSIS_END==="
	searchStart := 0
	if idx := strings.Index(logText, analysisEndMarker); idx != -1 {
		searchStart = idx + len(analysisEndMarker)
	}
	remaining := logText[searchStart:]

	var blocks []rawPatchBlock
	for {
		startIdx := strings.Index(remaining, patchBeginMarker)
		if startIdx == -1 {
			break
		}
		after := remaining[startIdx+len(patchBeginMarker):]
		endIdx := strings.Index(after, patchEndMarker)
		if endIdx == -1 {
			return nil, fmt.Errorf("found %s without matching %s", patchBeginMarker, patchEndMarker)
		}
		blockContent := strings.TrimSpace(after[:endIdx])
		remaining = after[endIdx+len(patchEndMarker):]

		block, err := parseRawBlock(blockContent)
		if err != nil {
			return nil, fmt.Errorf("invalid patch block: %w", err)
		}
		blocks = append(blocks, block)
	}

	return blocks, nil
}

func parseRawBlock(content string) (rawPatchBlock, error) {
	lines := strings.Split(content, "\n")
	var b rawPatchBlock
	dataStart := -1

	for i, line := range lines {
		if line == "" {
			if dataStart == -1 {
				dataStart = i + 1
			}
			continue
		}

		colonIdx := strings.IndexByte(line, ':')
		if colonIdx == -1 {
			if dataStart == -1 {
				dataStart = i
			}
			break
		}

		key := strings.TrimSpace(line[:colonIdx])
		val := strings.TrimSpace(line[colonIdx+1:])

		switch key {
		case "version":
			v, err := strconv.Atoi(val)
			if err != nil {
				return rawPatchBlock{}, fmt.Errorf("invalid version %q: %w", val, err)
			}
			b.version = v
		case "format":
			b.format = val
		case "encoding":
			b.encoding = val
		case "base_sha":
			b.baseSHA = val
		case "role":
			b.role = val
		case "chunks":
			n, err := strconv.Atoi(val)
			if err != nil {
				return rawPatchBlock{}, fmt.Errorf("invalid chunks %q: %w", val, err)
			}
			b.chunkCount = n
		case "chunk":
			parts := strings.SplitN(val, "/", 2)
			if len(parts) != 2 {
				return rawPatchBlock{}, fmt.Errorf("invalid chunk field %q", val)
			}
			idx, err := strconv.Atoi(parts[0])
			if err != nil {
				return rawPatchBlock{}, fmt.Errorf("invalid chunk index %q: %w", parts[0], err)
			}
			b.chunkIdx = idx
			if dataStart == -1 {
				dataStart = i + 1
			}
		}
	}

	if dataStart >= 0 && dataStart < len(lines) {
		b.data = strings.Join(lines[dataStart:], "\n")
		b.data = strings.TrimSpace(b.data)
	}

	return b, nil
}

// parseMachinePatch assembles and validates chunked patch blocks.
// It returns the metadata and the assembled encoded payload string.
// expectedRole and expectedSHA are used for cross-validation.
func parseMachinePatch(blocks []rawPatchBlock, expectedRole, expectedSHA string) (*MachinePatchMetadata, string, error) {
	if len(blocks) == 0 {
		return nil, "", fmt.Errorf("no patch blocks")
	}

	first := blocks[0]

	if first.version != patchVersion1 {
		return nil, "", fmt.Errorf("unsupported patch version %d", first.version)
	}
	if first.format != patchFormatDiff {
		return nil, "", fmt.Errorf("unsupported patch format %q", first.format)
	}
	if first.encoding != patchEncoding {
		return nil, "", fmt.Errorf("unsupported patch encoding %q", first.encoding)
	}
	if first.baseSHA == "" {
		return nil, "", fmt.Errorf("patch block missing base_sha")
	}
	if expectedSHA != "" && first.baseSHA != expectedSHA {
		return nil, "", fmt.Errorf("patch base_sha %q does not match expected SHA %q", first.baseSHA, expectedSHA)
	}
	if expectedRole != "" && first.role != "" && first.role != expectedRole {
		return nil, "", fmt.Errorf("patch role %q does not match expected role %q", first.role, expectedRole)
	}

	expectedChunks := first.chunkCount
	if expectedChunks == 0 {
		expectedChunks = 1
	}
	if len(blocks) != expectedChunks {
		return nil, "", fmt.Errorf("expected %d chunk(s), got %d", expectedChunks, len(blocks))
	}

	seen := make(map[int]bool, len(blocks))
	chunks := make([]string, len(blocks))
	for _, b := range blocks {
		if b.chunkIdx < 1 || b.chunkIdx > expectedChunks {
			return nil, "", fmt.Errorf("chunk index %d out of range [1,%d]", b.chunkIdx, expectedChunks)
		}
		if seen[b.chunkIdx] {
			return nil, "", fmt.Errorf("duplicate chunk index %d", b.chunkIdx)
		}
		seen[b.chunkIdx] = true
		chunks[b.chunkIdx-1] = b.data
	}

	payload := strings.Join(chunks, "")

	meta := &MachinePatchMetadata{
		Version:    first.version,
		Format:     first.format,
		Encoding:   first.encoding,
		BaseSHA:    first.baseSHA,
		Role:       first.role,
		ChunkCount: expectedChunks,
		Available:  true,
	}
	return meta, payload, nil
}

// isMachinePatchValid returns true if meta contains a complete, supported patch.
func isMachinePatchValid(meta *MachinePatchMetadata) bool {
	return meta != nil &&
		meta.Available &&
		meta.Version == patchVersion1 &&
		meta.Format == patchFormatDiff &&
		meta.Encoding == patchEncoding &&
		meta.BaseSHA != ""
}
