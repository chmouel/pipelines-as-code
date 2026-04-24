package llm

import (
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
)

const (
	externalIDSeparator    = "|"
	externalIDKindAnalysis = "llm-analysis"
	externalIDKindFix      = "llm-fix"
)

// ExternalIDParts holds the parsed components of a PAC AI check-run external ID.
type ExternalIDParts struct {
	Kind   string // "llm-analysis" or "llm-fix"
	Parent string // parent PipelineRun name
	Role   string // analysis role name
	SHA    string // commit SHA
}

// BuildExternalID constructs a pipe-separated external ID for AI check runs.
func BuildExternalID(kind, parent, role, sha string) string {
	return strings.Join([]string{kind, parent, role, sha}, externalIDSeparator)
}

// ParseExternalID parses a pipe-separated external ID into its components.
func ParseExternalID(externalID string) (ExternalIDParts, bool) {
	parts := strings.SplitN(externalID, externalIDSeparator, 4)
	if len(parts) != 4 {
		return ExternalIDParts{}, false
	}
	if parts[0] != externalIDKindAnalysis && parts[0] != externalIDKindFix {
		return ExternalIDParts{}, false
	}
	if parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return ExternalIDParts{}, false
	}
	return ExternalIDParts{
		Kind:   parts[0],
		Parent: parts[1],
		Role:   parts[2],
		SHA:    parts[3],
	}, true
}

// IsAnalysisExternalID returns true if the external ID is a valid analysis external ID.
func IsAnalysisExternalID(externalID string) bool {
	parsed, ok := ParseExternalID(externalID)
	return ok && parsed.Kind == externalIDKindAnalysis
}

// FixCheckRunStatusOpts returns base StatusOpts for a fix check run.
func FixCheckRunStatusOpts(parentName, roleName, sha string) status.StatusOpts {
	return status.StatusOpts{
		PipelineRunName:         BuildExternalID(externalIDKindFix, parentName, roleName, sha),
		OriginalPipelineRunName: fmt.Sprintf("AI Fix / %s", roleName),
		Title:                   fmt.Sprintf("AI Fix - %s", roleName),
		PipelineRun:             nil,
	}
}
