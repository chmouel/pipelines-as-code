package roles

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"gopkg.in/yaml.v3"
)

const basePath = ".tekton/ai"

var roleNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type frontmatterRole struct {
	Name         string `yaml:"name"`
	Model        string `yaml:"model,omitempty"`
	MaxTokens    *int   `yaml:"max_tokens,omitempty"`
	OnCEL        string `yaml:"on_cel,omitempty"`
	Output       string `yaml:"output,omitempty"`
	ContextItems struct {
		CommitContent bool     `yaml:"commit_content,omitempty"`
		PRContent     bool     `yaml:"pr_content,omitempty"`
		ErrorContent  bool     `yaml:"error_content,omitempty"`
		DiffContent   bool     `yaml:"diff_content,omitempty"`
		Files         []string `yaml:"files,omitempty"`
		ContainerLogs *struct {
			Enabled  bool `yaml:"enabled"`
			MaxLines *int `yaml:"max_lines,omitempty"`
		} `yaml:"container_logs,omitempty"`
	} `yaml:"context_items,omitempty"`
}

func Path(name string) string {
	return fmt.Sprintf("%s/%s.md", basePath, name)
}

// DiscoverAndLoad lists all .md files in the repository's .tekton/ai directory and
// parses each as a repo role. Invalid files are skipped and their errors collected.
func DiscoverAndLoad(ctx context.Context, prov provider.Interface, event *info.Event) ([]Role, []error) {
	if prov == nil || event == nil {
		return nil, nil
	}

	paths, err := prov.ListDirFilesInsideRepo(ctx, event, basePath)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to list %s: %w", basePath, err)}
	}
	if len(paths) == 0 {
		return nil, nil
	}

	roles := make([]Role, 0, len(paths))
	errs := []error{}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".md")
		if err := validateRoleName(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}

		content, err := prov.GetFileInsideRepo(ctx, event, path, "")
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
			continue
		}

		role, err := ParseRole(path, name, content)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		roles = append(roles, role)
	}

	return roles, errs
}

func ParseRole(path, expectedName, content string) (Role, error) {
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return Role{}, fmt.Errorf("%s: %w", path, err)
	}

	role := Role{Path: path}
	var parsed frontmatterRole
	if err := yaml.Unmarshal([]byte(frontmatter), &parsed); err != nil {
		return Role{}, fmt.Errorf("%s: failed to parse YAML frontmatter: %w", path, err)
	}
	role.Name = parsed.Name
	role.Model = parsed.Model
	role.OnCEL = parsed.OnCEL
	role.Output = parsed.Output
	if parsed.MaxTokens != nil {
		role.MaxTokens = *parsed.MaxTokens
	}
	role.ContextItems = frontmatterContextConfig(parsed)
	role.Prompt = strings.TrimSpace(body)

	if err := ValidateRole(role, expectedName, parsed); err != nil {
		return Role{}, fmt.Errorf("%s: %w", path, err)
	}

	return role, nil
}

func ValidateRole(role Role, expectedName string, parsed frontmatterRole) error {
	if role.Name == "" {
		return fmt.Errorf("name is required")
	}
	if err := validateRoleName(role.Name); err != nil {
		return err
	}
	if expectedName != "" && role.Name != expectedName {
		return fmt.Errorf("frontmatter name %q does not match requested role %q", role.Name, expectedName)
	}
	if role.Prompt == "" {
		return fmt.Errorf("prompt body is required")
	}
	if role.Output != "" && role.Output != "pr-comment" && role.Output != "check-run" {
		return fmt.Errorf("invalid output destination %q", role.Output)
	}
	if parsed.MaxTokens != nil && (role.MaxTokens < 1 || role.MaxTokens > 4000) {
		return fmt.Errorf("max_tokens must be between 1 and 4000 when set")
	}

	if parsed.ContextItems.ContainerLogs != nil && parsed.ContextItems.ContainerLogs.MaxLines != nil {
		maxLines := *parsed.ContextItems.ContainerLogs.MaxLines
		if maxLines < 1 || maxLines > 1000 {
			return fmt.Errorf("container_logs.max_lines must be between 1 and 1000 when set")
		}
	}

	if role.OnCEL != "" {
		if err := cel.Validate(role.OnCEL); err != nil {
			return fmt.Errorf("invalid on_cel expression: %w", err)
		}
	}

	return nil
}

func validateRoleName(name string) error {
	if !roleNamePattern.MatchString(name) {
		return fmt.Errorf("invalid role name %q: only letters, digits, '_' and '-' are allowed", name)
	}
	return nil
}

func frontmatterContextConfig(parsed frontmatterRole) *v1alpha1.ContextConfig {
	if !parsed.ContextItems.CommitContent &&
		!parsed.ContextItems.PRContent &&
		!parsed.ContextItems.ErrorContent &&
		!parsed.ContextItems.DiffContent &&
		len(parsed.ContextItems.Files) == 0 &&
		parsed.ContextItems.ContainerLogs == nil {
		return nil
	}

	config := &v1alpha1.ContextConfig{
		CommitContent: parsed.ContextItems.CommitContent,
		PRContent:     parsed.ContextItems.PRContent,
		ErrorContent:  parsed.ContextItems.ErrorContent,
		DiffContent:   parsed.ContextItems.DiffContent,
		Files:         parsed.ContextItems.Files,
	}

	if parsed.ContextItems.ContainerLogs != nil {
		config.ContainerLogs = &v1alpha1.ContainerLogsConfig{
			Enabled: parsed.ContextItems.ContainerLogs.Enabled,
		}
		if parsed.ContextItems.ContainerLogs.MaxLines != nil {
			config.ContainerLogs.MaxLines = *parsed.ContextItems.ContainerLogs.MaxLines
		}
	}

	return config
}

func splitFrontmatter(content string) (string, string, error) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", fmt.Errorf("missing YAML frontmatter")
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", "", fmt.Errorf("missing closing YAML frontmatter delimiter")
	}

	return strings.Join(lines[1:end], "\n"), strings.Join(lines[end+1:], "\n"), nil
}
