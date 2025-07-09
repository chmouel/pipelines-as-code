package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
)

type artifactClient struct{}

type artifactHubPkgData struct {
	ManifestRaw string `json:"manifestRaw"`
}

type artifactHubPkgResponse struct {
	Data artifactHubPkgData `json:"data"`
}

func (a *artifactClient) GetResource(ctx context.Context, cs *params.Run, catalog settings.HubCatalog, resource, kind string) (string, error) {
	name := resource
	version := ""
	if strings.Contains(resource, ":") {
		parts := strings.Split(resource, ":")
		name = parts[0]
		version = parts[len(parts)-1]
	}

	endpoint := fmt.Sprintf("%s/api/v1/packages/tekton/%s/%s", catalog.URL, kind, name)
	if version != "" {
		endpoint = fmt.Sprintf("%s?version=%s", endpoint, url.QueryEscape(version))
	}

	data, err := cs.Clients.GetURL(ctx, endpoint)
	if err != nil {
		return "", err
	}
	resp := artifactHubPkgResponse{}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", err
	}
	if resp.Data.ManifestRaw == "" {
		return "", fmt.Errorf("empty manifest received from artifact hub")
	}
	return resp.Data.ManifestRaw, nil
}
