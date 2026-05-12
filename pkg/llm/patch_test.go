package llm

import (
	"testing"

	"gotest.tools/v3/assert"
)

const validSingleBlockLog = `
===ANALYSIS_BEGIN===
{"status":"success","backend":"claude","content":"Root cause found."}
===ANALYSIS_END===

===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: abc123def456
role: failure-analysis
chunks: 1
chunk: 1/1
H4sIAAAAAAAAA+3BMQEAAADCoPVP7WsIoAAA
===LLM_PATCH_END===
`

func TestExtractMachinePatchBlocksSingleBlock(t *testing.T) {
	blocks, err := extractMachinePatchBlocks(validSingleBlockLog)
	assert.NilError(t, err)
	assert.Equal(t, len(blocks), 1)
	b := blocks[0]
	assert.Equal(t, b.version, 1)
	assert.Equal(t, b.format, "git-diff")
	assert.Equal(t, b.encoding, "gzip+base64")
	assert.Equal(t, b.baseSHA, "abc123def456")
	assert.Equal(t, b.role, "failure-analysis")
	assert.Equal(t, b.chunkCount, 1)
	assert.Equal(t, b.chunkIdx, 1)
	assert.Equal(t, b.data, "H4sIAAAAAAAAA+3BMQEAAADCoPVP7WsIoAAA")
}

func TestExtractMachinePatchBlocksNoBlock(t *testing.T) {
	log := "===ANALYSIS_BEGIN===\n{}\n===ANALYSIS_END===\n"
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	assert.Equal(t, len(blocks), 0)
}

func TestExtractMachinePatchBlocksMissingEnd(t *testing.T) {
	log := "===LLM_PATCH_BEGIN===\nversion: 1\n"
	_, err := extractMachinePatchBlocks(log)
	assert.ErrorContains(t, err, "without matching")
}

func TestExtractMachinePatchBlocksMultiChunk(t *testing.T) {
	log := `===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: sha1
role: myrole
chunks: 2
chunk: 1/2
AAAAAA
===LLM_PATCH_END===
===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: sha1
role: myrole
chunks: 2
chunk: 2/2
BBBBBB
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	assert.Equal(t, len(blocks), 2)
	assert.Equal(t, blocks[0].chunkIdx, 1)
	assert.Equal(t, blocks[1].chunkIdx, 2)
}

func TestParseMachinePatchValidSingleBlock(t *testing.T) {
	blocks, err := extractMachinePatchBlocks(validSingleBlockLog)
	assert.NilError(t, err)

	meta, payload, err := parseMachinePatch(blocks, "failure-analysis", "abc123def456")
	assert.NilError(t, err)
	assert.Assert(t, meta != nil)
	assert.Equal(t, meta.Version, 1)
	assert.Equal(t, meta.Format, "git-diff")
	assert.Equal(t, meta.Encoding, "gzip+base64")
	assert.Equal(t, meta.BaseSHA, "abc123def456")
	assert.Equal(t, meta.Available, true)
	assert.Equal(t, payload, "H4sIAAAAAAAAA+3BMQEAAADCoPVP7WsIoAAA")
}

func TestParseMachinePatchValidMultiChunk(t *testing.T) {
	log := `===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: sha1
role: myrole
chunks: 2
chunk: 1/2
AAAAAA
===LLM_PATCH_END===
===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: sha1
role: myrole
chunks: 2
chunk: 2/2
BBBBBB
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)

	meta, payload, err := parseMachinePatch(blocks, "myrole", "sha1")
	assert.NilError(t, err)
	assert.Assert(t, meta != nil)
	assert.Equal(t, meta.ChunkCount, 2)
	assert.Equal(t, payload, "AAAAAABBBBBB")
}

func TestParseMachinePatchUnsupportedVersion(t *testing.T) {
	log := `===LLM_PATCH_BEGIN===
version: 2
format: git-diff
encoding: gzip+base64
base_sha: sha1
chunks: 1
chunk: 1/1
DATA
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "", "")
	assert.ErrorContains(t, err, "unsupported patch version")
}

func TestParseMachinePatchUnsupportedFormat(t *testing.T) {
	log := `===LLM_PATCH_BEGIN===
version: 1
format: json-patch
encoding: gzip+base64
base_sha: sha1
chunks: 1
chunk: 1/1
DATA
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "", "")
	assert.ErrorContains(t, err, "unsupported patch format")
}

func TestParseMachinePatchUnsupportedEncoding(t *testing.T) {
	log := `===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: plain
base_sha: sha1
chunks: 1
chunk: 1/1
DATA
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "", "")
	assert.ErrorContains(t, err, "unsupported patch encoding")
}

func TestParseMachinePatchMissingBaseSHA(t *testing.T) {
	log := `===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
chunks: 1
chunk: 1/1
DATA
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "", "")
	assert.ErrorContains(t, err, "missing base_sha")
}

func TestParseMachinePatchSHAMismatch(t *testing.T) {
	blocks, err := extractMachinePatchBlocks(validSingleBlockLog)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "", "different-sha")
	assert.ErrorContains(t, err, "does not match expected SHA")
}

func TestParseMachinePatchRoleMismatch(t *testing.T) {
	blocks, err := extractMachinePatchBlocks(validSingleBlockLog)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "wrong-role", "abc123def456")
	assert.ErrorContains(t, err, "does not match expected role")
}

func TestParseMachinePatchDuplicateChunk(t *testing.T) {
	log := `===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: sha1
chunks: 2
chunk: 1/2
A
===LLM_PATCH_END===
===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: sha1
chunks: 2
chunk: 1/2
A
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "", "sha1")
	assert.ErrorContains(t, err, "duplicate chunk")
}

func TestParseMachinePatchChunkCountMismatch(t *testing.T) {
	// Says 2 chunks but only 1 block present
	log := `===LLM_PATCH_BEGIN===
version: 1
format: git-diff
encoding: gzip+base64
base_sha: sha1
chunks: 2
chunk: 1/2
A
===LLM_PATCH_END===
`
	blocks, err := extractMachinePatchBlocks(log)
	assert.NilError(t, err)
	_, _, err = parseMachinePatch(blocks, "", "sha1")
	assert.ErrorContains(t, err, "expected 2 chunk(s)")
}

func TestParseMachinePatchNoBlocks(t *testing.T) {
	_, _, err := parseMachinePatch(nil, "", "")
	assert.ErrorContains(t, err, "no patch blocks")
}

func TestIsMachinePatchValid(t *testing.T) {
	tests := []struct {
		name  string
		meta  *MachinePatchMetadata
		valid bool
	}{
		{
			name: "valid",
			meta: &MachinePatchMetadata{
				Version:   1,
				Format:    "git-diff",
				Encoding:  "gzip+base64",
				BaseSHA:   "abc123",
				Available: true,
			},
			valid: true,
		},
		{
			name:  "nil",
			meta:  nil,
			valid: false,
		},
		{
			name: "not available",
			meta: &MachinePatchMetadata{
				Version:   1,
				Format:    "git-diff",
				Encoding:  "gzip+base64",
				BaseSHA:   "abc123",
				Available: false,
			},
			valid: false,
		},
		{
			name: "wrong version",
			meta: &MachinePatchMetadata{
				Version:   2,
				Format:    "git-diff",
				Encoding:  "gzip+base64",
				BaseSHA:   "abc123",
				Available: true,
			},
			valid: false,
		},
		{
			name: "missing base sha",
			meta: &MachinePatchMetadata{
				Version:   1,
				Format:    "git-diff",
				Encoding:  "gzip+base64",
				BaseSHA:   "",
				Available: true,
			},
			valid: false,
		},
		{
			name: "unsupported encoding",
			meta: &MachinePatchMetadata{
				Version:   1,
				Format:    "git-diff",
				Encoding:  "plain",
				BaseSHA:   "abc123",
				Available: true,
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, isMachinePatchValid(tt.meta), tt.valid)
		})
	}
}
