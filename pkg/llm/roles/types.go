package roles

import "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"

// Role describes a repository-backed AI role loaded from .tekton/ai/<name>.md.
// The YAML frontmatter holds the metadata and the Markdown body is the prompt.
type Role struct {
	Name string `yaml:"name"`
	// Image overrides the top-level AIAnalysisConfig.Image for this role only.
	// When empty the shared default image from the Repository CR is used.
	Image        string                  `yaml:"image,omitempty"`
	Model        string                  `yaml:"model,omitempty"`
	MaxTokens    int                     `yaml:"max_tokens,omitempty"`
	OnCEL        string                  `yaml:"on_cel,omitempty"`
	Output       string                  `yaml:"output,omitempty"`
	ContextItems *v1alpha1.ContextConfig `yaml:"context_items,omitempty"`
	Prompt       string                  `yaml:"-"`
	Path         string                  `yaml:"-"`
}

func (r Role) AnalysisRole() v1alpha1.AnalysisRole {
	return v1alpha1.AnalysisRole{
		Name:         r.Name,
		Prompt:       r.Prompt,
		Image:        r.Image,
		Model:        r.Model,
		MaxTokens:    r.MaxTokens,
		OnCEL:        r.OnCEL,
		Output:       r.Output,
		ContextItems: r.ContextItems,
	}
}
