package gitlab

import "net/http"

type (
	MarkdownServiceInterface interface {
		Render(opt *RenderOptions, options ...RequestOptionFunc) (*Markdown, *Response, error)
	}

	// MarkdownService handles communication with the markdown related methods of
	// the GitLab API.
	//
	// GitLab API docs: https://docs.gitlab.com/api/markdown/
	MarkdownService struct {
		client *Client
	}
)

var _ MarkdownServiceInterface = (*MarkdownService)(nil)

// Markdown represents a markdown document.
//
// Gitlab API docs: https://docs.gitlab.com/api/markdown/
type Markdown struct {
	HTML string `json:"html"`
}

// RenderOptions represents the available Render() options.
//
// Gitlab API docs:
// https://docs.gitlab.com/api/markdown/#render-an-arbitrary-markdown-document
type RenderOptions struct {
	Text                    *string `url:"text,omitempty" json:"text,omitempty"`
	GitlabFlavouredMarkdown *bool   `url:"gfm,omitempty" json:"gfm,omitempty"`
	Project                 *string `url:"project,omitempty" json:"project,omitempty"`
}

// Render an arbitrary markdown document.
//
// Gitlab API docs:
// https://docs.gitlab.com/api/markdown/#render-an-arbitrary-markdown-document
func (s *MarkdownService) Render(opt *RenderOptions, options ...RequestOptionFunc) (*Markdown, *Response, error) {
	req, err := s.client.NewRequest(http.MethodPost, "markdown", opt, options)
	if err != nil {
		return nil, nil, err
	}

	md := new(Markdown)
	response, err := s.client.Do(req, md)
	if err != nil {
		return nil, response, err
	}

	return md, response, nil
}
