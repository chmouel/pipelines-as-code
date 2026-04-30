package roles

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	"gotest.tools/v3/assert"
)

func TestDiscoverAndLoadReturnsNilWhenDirectoryIsEmpty(t *testing.T) {
	prov := &tprovider.TestProviderImp{FilesInsideRepo: map[string]string{}}
	roles, errs := DiscoverAndLoad(context.Background(), prov, &info.Event{})
	assert.Equal(t, len(roles), 0)
	assert.Equal(t, len(errs), 0)
}

func TestDiscoverAndLoadReturnsNilWhenProviderIsNil(t *testing.T) {
	roles, errs := DiscoverAndLoad(context.Background(), nil, &info.Event{})
	assert.Equal(t, len(roles), 0)
	assert.Equal(t, len(errs), 0)
}

func TestDiscoverAndLoadIgnoresNonMdFiles(t *testing.T) {
	prov := &tprovider.TestProviderImp{
		FilesInsideRepo: map[string]string{
			".tekton/ai/notes.txt": "not a skill file",
		},
	}
	roles, errs := DiscoverAndLoad(context.Background(), prov, &info.Event{})
	assert.Equal(t, len(roles), 0)
	assert.Equal(t, len(errs), 0)
}

func TestDiscoverAndLoadSkipsInvalidFilesAndContinues(t *testing.T) {
	prov := &tprovider.TestProviderImp{
		FilesInsideRepo: map[string]string{
			Path("failure-analysis"): `---
name: failure-analysis
output: check-run
---
Analyze the failure.
`,
			Path("broken"): `---
name: broken
output: nowhere
---
This should be rejected.
`,
		},
	}

	roles, errs := DiscoverAndLoad(context.Background(), prov, &info.Event{})
	assert.Equal(t, len(roles), 1)
	assert.Equal(t, roles[0].Name, "failure-analysis")
	assert.Equal(t, len(errs), 1)
	assert.ErrorContains(t, errs[0], ".tekton/ai/broken.md")
}

func TestDiscoverAndLoadLoadsAllValidFiles(t *testing.T) {
	prov := &tprovider.TestProviderImp{
		FilesInsideRepo: map[string]string{
			Path("failure-analysis"): `---
name: failure-analysis
output: check-run
---
Analyze the failure.
`,
			Path("security-review"): `---
name: security-review
output: pr-comment
---
Review for security issues.
`,
		},
	}

	roles, errs := DiscoverAndLoad(context.Background(), prov, &info.Event{})
	assert.Equal(t, len(roles), 2)
	assert.Equal(t, len(errs), 0)
}

func TestParseRoleRejectsInvalidFrontmatterName(t *testing.T) {
	_, err := ParseRole(Path("bad-name"), "bad-name", "---\nname: bad name with spaces\n---\nPrompt.")
	assert.ErrorContains(t, err, "invalid role name")
}

func TestParseRole(t *testing.T) {
	role, err := ParseRole(Path("failure-analysis"), "failure-analysis", `---
name: failure-analysis
output: check-run
max_tokens: 111
context_items:
  error_content: true
  container_logs:
    enabled: true
    max_lines: 100
---
Analyze the failure and suggest a fix.
`)
	assert.NilError(t, err)
	assert.Equal(t, role.Name, "failure-analysis")
	assert.Equal(t, role.Output, "check-run")
	assert.Equal(t, role.MaxTokens, 111)
	assert.Assert(t, role.ContextItems != nil)
	assert.Assert(t, role.ContextItems.ErrorContent)
	assert.Assert(t, role.ContextItems.ContainerLogs != nil)
	assert.Equal(t, role.ContextItems.ContainerLogs.MaxLines, 100)
	assert.Equal(t, role.Prompt, "Analyze the failure and suggest a fix.")
}

func TestParseRoleRequiresFrontmatterAndBody(t *testing.T) {
	_, err := ParseRole(Path("failure-analysis"), "failure-analysis", "just prompt text")
	assert.ErrorContains(t, err, "missing YAML frontmatter")

	_, err = ParseRole(Path("failure-analysis"), "failure-analysis", `---
name: failure-analysis
---
`)
	assert.ErrorContains(t, err, "prompt body is required")
}

func TestParseRoleValidatesName(t *testing.T) {
	_, err := ParseRole(Path("failure-analysis"), "failure-analysis", `---
name: another-name
---
Analyze the failure.
`)
	assert.ErrorContains(t, err, "does not match requested role")
}

func TestParseRoleValidatesCEL(t *testing.T) {
	_, err := ParseRole(Path("failure-analysis"), "failure-analysis", `---
name: failure-analysis
on_cel: "invalid syntax ("
---
Analyze the failure.
`)
	assert.ErrorContains(t, err, "invalid on_cel expression")
}

func TestParseRoleRejectsExplicitZeroContainerMaxLines(t *testing.T) {
	_, err := ParseRole(Path("failure-analysis"), "failure-analysis", `---
name: failure-analysis
context_items:
  container_logs:
    enabled: true
    max_lines: 0
---
Analyze the failure.
`)
	assert.ErrorContains(t, err, "container_logs.max_lines must be between 1 and 1000 when set")
}

func TestParseRoleWithCustomImage(t *testing.T) {
	role, err := ParseRole(Path("failure-analysis"), "failure-analysis", `---
name: failure-analysis
image: ghcr.io/chmouel/agents-image:latest
output: check-run
---
Analyze the failure.
`)
	assert.NilError(t, err)
	assert.Equal(t, role.Name, "failure-analysis")
	assert.Equal(t, role.Image, "ghcr.io/chmouel/agents-image:latest")
	assert.Equal(t, role.Output, "check-run")

	ar := role.AnalysisRole()
	assert.Equal(t, ar.Image, "ghcr.io/chmouel/agents-image:latest")
	assert.Equal(t, ar.GetImage(), "ghcr.io/chmouel/agents-image:latest")
}

func TestParseRoleWithoutImageInheritsEmpty(t *testing.T) {
	role, err := ParseRole(Path("failure-analysis"), "failure-analysis", `---
name: failure-analysis
---
Analyze the failure.
`)
	assert.NilError(t, err)
	assert.Equal(t, role.Image, "")
	ar2 := role.AnalysisRole()
	assert.Equal(t, ar2.GetImage(), "")
}
