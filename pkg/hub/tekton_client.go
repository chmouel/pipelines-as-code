package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
)

type tektonClient struct{}

func getSpecificVersion(ctx context.Context, cs *params.Run, catalog settings.HubCatalog, resource, kind string) (string, error) {
	split := strings.Split(resource, ":")
	version := split[len(split)-1]
	resourceName := split[0]
	url := fmt.Sprintf("%s/resource/%s/%s/%s/%s", catalog.URL, catalog.Name, kind, resourceName, version)
	hr := hubResourceVersion{}
	data, err := cs.Clients.GetURL(ctx, url)
	if err != nil {
		return "", fmt.Errorf("could not fetch specific %s version from the hub %s:%s: %w", kind, resource, version, err)
	}
	if err := json.Unmarshal(data, &hr); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/raw", url), nil
}

func getLatestVersion(ctx context.Context, cs *params.Run, catalog settings.HubCatalog, resource, kind string) (string, error) {
	url := fmt.Sprintf("%s/resource/%s/%s/%s", catalog.URL, catalog.Name, kind, resource)
	hr := new(hubResource)
	data, err := cs.Clients.GetURL(ctx, url)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, &hr); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s/raw", url, *hr.Data.LatestVersion.Version), nil
}

func (t *tektonClient) GetResource(ctx context.Context, cs *params.Run, catalog settings.HubCatalog, resource, kind string) (string, error) {
	var rawURL string
	var err error
	if strings.Contains(resource, ":") {
		rawURL, err = getSpecificVersion(ctx, cs, catalog, resource, kind)
	} else {
		rawURL, err = getLatestVersion(ctx, cs, catalog, resource, kind)
	}
	if err != nil {
		return "", fmt.Errorf("could not fetch remote %s %s, hub API returned: %w", kind, resource, err)
	}
	data, err := cs.Clients.GetURL(ctx, rawURL)
	if err != nil {
		return "", fmt.Errorf("could not fetch remote %s %s, hub API returned: %w", kind, resource, err)
	}
	return string(data), nil
}
