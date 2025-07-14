package hub

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
)

// Client defines methods to fetch resources from a hub implementation.
type Client interface {
	GetResource(ctx context.Context, cs *params.Run, catalog settings.HubCatalog, resource, kind string) (string, error)
}

func clientForType(hType string) Client {
	switch hType {
	case settings.HubTypeArtifact:
		return &artifactClient{}
	default:
		return &tektonClient{}
	}
}

// GetResource fetches a resource from the catalog based on its configuration.
func GetResource(ctx context.Context, cs *params.Run, catalogName, resource, kind string) (string, error) {
	value, _ := cs.Info.Pac.HubCatalogs.Load(catalogName)
	catalog, ok := value.(settings.HubCatalog)
	if !ok {
		return "", fmt.Errorf("could not get details for catalog name: %s", catalogName)
	}
	client := clientForType(catalog.Type)
	return client.GetResource(ctx, cs, catalog, resource, kind)
}
