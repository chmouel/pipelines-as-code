package provider

import "encoding/json"

// MinimalEventInfo contains the minimum info needed to report errors
// back to the Git provider before full payload parsing completes.
// This is extracted from the raw webhook payload before full parsing.
type MinimalEventInfo struct {
	// Repository information
	Organization string // GitHub org, GitLab group, etc.
	Repository   string // Repository name
	URL          string // Repository HTML URL

	// Commit/SHA information
	SHA string // Commit SHA (varies by event type)

	// Event metadata
	EventType string // e.g., "push", "pull_request"
	Provider  string // "github", "gitlab", "gitea", "bitbucket-cloud", "bitbucket-server"

	// GitHub-specific
	InstallationID int64  // GitHub App installation ID
	GHEURL         string // GitHub Enterprise URL (if applicable)

	// Token (set after successful token generation)
	Token string
}

// githubMinimalPayload represents the minimal structure we need from GitHub webhooks.
type githubMinimalPayload struct {
	Repository struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name    string `json:"name"`
		HTMLURL string `json:"html_url"`
	} `json:"repository"`
	Installation struct {
		ID *int64 `json:"id"`
	} `json:"installation"`
	// SHA location varies by event type
	After      string `json:"after"` // Push event
	HeadCommit struct {
		ID string `json:"id"`
	} `json:"head_commit"` // Push event
	PullRequest struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Number int `json:"number"`
	} `json:"pull_request"` // PR event
	CheckRun struct {
		HeadSHA string `json:"head_sha"`
	} `json:"check_run"` // Check run event
	CheckSuite struct {
		HeadSHA string `json:"head_sha"`
	} `json:"check_suite"` // Check suite event
	Comment struct {
		CommitID string `json:"commit_id"`
	} `json:"comment"` // Commit comment event
}

// ExtractMinimalInfoFromGitHub extracts minimal info from a GitHub webhook payload.
// Returns nil if extraction fails or required fields are missing.
func ExtractMinimalInfoFromGitHub(eventType string, payload []byte, gheURL string) *MinimalEventInfo {
	var p githubMinimalPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}

	info := &MinimalEventInfo{
		Organization: p.Repository.Owner.Login,
		Repository:   p.Repository.Name,
		URL:          p.Repository.HTMLURL,
		EventType:    eventType,
		Provider:     "github",
		GHEURL:       gheURL,
	}

	if p.Installation.ID != nil {
		info.InstallationID = *p.Installation.ID
	}

	// Extract SHA based on event type
	switch eventType {
	case "push":
		if p.HeadCommit.ID != "" {
			info.SHA = p.HeadCommit.ID
		} else {
			info.SHA = p.After
		}
	case "pull_request":
		info.SHA = p.PullRequest.Head.SHA
	case "check_run":
		info.SHA = p.CheckRun.HeadSHA
	case "check_suite":
		info.SHA = p.CheckSuite.HeadSHA
	case "commit_comment":
		info.SHA = p.Comment.CommitID
	}

	// Validate we have minimum required info
	if info.Organization == "" || info.Repository == "" {
		return nil
	}

	return info
}

// giteaMinimalPayload represents the minimal structure we need from Gitea webhooks.
type giteaMinimalPayload struct {
	Repository struct {
		Owner struct {
			Login    string `json:"login"`
			Username string `json:"username"`
		} `json:"owner"`
		Name    string `json:"name"`
		HTMLURL string `json:"html_url"`
	} `json:"repository"`
	After      string `json:"after"` // Push event
	HeadCommit struct {
		ID string `json:"id"`
	} `json:"head_commit"` // Push event
	PullRequest struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	} `json:"pull_request"` // PR event
}

// ExtractMinimalInfoFromGitea extracts minimal info from a Gitea webhook payload.
func ExtractMinimalInfoFromGitea(eventType string, payload []byte) *MinimalEventInfo {
	var p giteaMinimalPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}

	owner := p.Repository.Owner.Login
	if owner == "" {
		owner = p.Repository.Owner.Username
	}

	info := &MinimalEventInfo{
		Organization: owner,
		Repository:   p.Repository.Name,
		URL:          p.Repository.HTMLURL,
		EventType:    eventType,
		Provider:     "gitea",
	}

	// Extract SHA based on event type
	switch eventType {
	case "push":
		if p.HeadCommit.ID != "" {
			info.SHA = p.HeadCommit.ID
		} else {
			info.SHA = p.After
		}
	case "pull_request":
		info.SHA = p.PullRequest.Head.SHA
	}

	if info.Organization == "" || info.Repository == "" {
		return nil
	}

	return info
}

// gitlabMinimalPayload represents the minimal structure we need from GitLab webhooks.
type gitlabMinimalPayload struct {
	Project struct {
		PathWithNamespace string `json:"path_with_namespace"`
		WebURL            string `json:"web_url"`
	} `json:"project"`
	After            string `json:"after"`        // Push event
	CheckoutSHA      string `json:"checkout_sha"` // Push event
	ObjectAttributes struct {
		LastCommit struct {
			ID string `json:"id"`
		} `json:"last_commit"`
	} `json:"object_attributes"` // MR event
}

// ExtractMinimalInfoFromGitLab extracts minimal info from a GitLab webhook payload.
func ExtractMinimalInfoFromGitLab(eventType string, payload []byte) *MinimalEventInfo {
	var p gitlabMinimalPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}

	info := &MinimalEventInfo{
		URL:       p.Project.WebURL,
		EventType: eventType,
		Provider:  "gitlab",
	}

	// GitLab uses path_with_namespace which is "org/repo"
	// We'll store the full path in Organization and leave Repository empty
	// This is fine because for error reporting we mainly need the URL
	info.Organization = p.Project.PathWithNamespace

	// Extract SHA based on event type
	switch eventType {
	case "Push Hook":
		if p.CheckoutSHA != "" {
			info.SHA = p.CheckoutSHA
		} else {
			info.SHA = p.After
		}
	case "Merge Request Hook":
		info.SHA = p.ObjectAttributes.LastCommit.ID
	}

	if info.URL == "" {
		return nil
	}

	return info
}

// genericMinimalPayload for extracting just the repository URL from any provider.
type genericMinimalPayload struct {
	Repository struct {
		HTMLURL string `json:"html_url"`
		WebURL  string `json:"web_url"` // GitLab
		Links   struct {
			HTML struct {
				Href string `json:"href"`
			} `json:"html"`
		} `json:"links"` // Bitbucket
	} `json:"repository"`
	Project struct {
		WebURL string `json:"web_url"` // GitLab
	} `json:"project"`
}

// ExtractMinimalInfoGeneric extracts whatever minimal info we can from any payload.
// This is a fallback when provider-specific extraction isn't available.
// Returns nil if even URL extraction fails.
func ExtractMinimalInfoGeneric(payload []byte) *MinimalEventInfo {
	var p genericMinimalPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}

	info := &MinimalEventInfo{}

	// Try to find repository URL from various locations
	switch {
	case p.Repository.HTMLURL != "":
		info.URL = p.Repository.HTMLURL
	case p.Repository.WebURL != "":
		info.URL = p.Repository.WebURL
	case p.Project.WebURL != "":
		info.URL = p.Project.WebURL
	case p.Repository.Links.HTML.Href != "":
		info.URL = p.Repository.Links.HTML.Href
	}

	if info.URL == "" {
		return nil
	}

	return info
}
