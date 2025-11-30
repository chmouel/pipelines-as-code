// Copyright © 2022 The Tekton Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package hub

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
)

const (
	artifactHubTaskType                   = "tekton-task"
	artifactHubPipelineType               = "tekton-pipeline"
	defaultArtifactHubCatalogTaskName     = "tekton-catalog-tasks"
	defaultArtifactHubCatalogPipelineName = "tekton-catalog-pipelines"
)

// artifactHubClient is a client for the Artifact Hub.
type artifactHubClient struct {
	params *params.Run
	url    string
	name   string
}

// newArtifactHubClient returns a new Artifact Hub client.
func newArtifactHubClient(params *params.Run, url, name string) Client {
	return &artifactHubClient{params: params, url: url, name: name}
}

// GetResource gets a resource from the Artifact Hub.
func (a *artifactHubClient) GetResource(ctx context.Context, catalogName, resource, kind string) (string, error) {
	var rawURL string
	var err error

	if strings.Contains(resource, ":") {
		rawURL, err = a.getSpecificVersion(ctx, catalogName, resource, kind)
	} else {
		rawURL, err = a.getLatestVersion(ctx, catalogName, resource, kind)
	}
	if err != nil {
		return "", fmt.Errorf("could not fetch remote %s %s, hub API returned: %w", kind, resource, err)
	}

	data, err := a.params.Clients.GetURL(ctx, rawURL)
	if err != nil {
		return "", fmt.Errorf("could not fetch remote %s %s, hub API returned: %w", kind, resource, err)
	}
	return string(data), err
}

func getTypeByKind(catalogName, kind string) (string, string) {
	pkgType := artifactHubPipelineType
	if catalogName == "default" || catalogName == "" {
		catalogName = defaultArtifactHubCatalogPipelineName
	}
	if kind == "task" {
		pkgType = artifactHubTaskType
		if catalogName == "default" || catalogName == "" {
			catalogName = defaultArtifactHubCatalogTaskName
		}
	}
	return pkgType, catalogName
}

// getLatestVersion gets the latest version of a resource from the Artifact Hub.
// url is like:
// https://artifacthub.io/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone
func (a *artifactHubClient) getLatestVersion(ctx context.Context, catalogName, resource, kind string) (string, error) {
	pkgType, catalogName := getTypeByKind(catalogName, kind)
	url := fmt.Sprintf("%s/api/v1/packages/%s/%s/%s", a.url, pkgType, catalogName, resource)
	resp := new(artifactHubPkgResponse)
	data, err := a.params.Clients.GetURL(ctx, url)
	if err != nil {
		return "", err
	}
	err = json.Unmarshal(data, &resp)
	if err != nil {
		return "", err
	}
	return resp.Data.ManifestRaw, nil
}

// getSpecificVersion gets a specific version of a resource from the Artifact Hub.
// url is like:
// https://artifacthub.io/api/v1/packages/tekton-task/tekton-catalog-tasks/git-clone/0.9.0
func (a *artifactHubClient) getSpecificVersion(ctx context.Context, catalogName, resource, kind string) (string, error) {
	pkgType, catalogName := getTypeByKind(catalogName, kind)

	split := strings.Split(resource, ":")
	version := split[len(split)-1]
	resourceName := split[0]

	url := fmt.Sprintf("%s/api/v1/packages/%s/%s/%s/%s", a.url, pkgType, catalogName, resourceName, version)
	resp := new(artifactHubPkgResponse)
	data, err := a.params.Clients.GetURL(ctx, url)
	if err != nil {
		return "", err
	}
	err = json.Unmarshal(data, &resp)
	if err != nil {
		return "", err
	}
	return resp.Data.ManifestRaw, nil
}

// artifactHubPkgResponse is the response from the Artifact Hub API.
type artifactHubPkgResponse struct {
	Data artifactHubPkgData `json:"data,omitempty"`
}

// artifactHubPkgData is the data from the Artifact Hub API.
type artifactHubPkgData struct {
	ManifestRaw string `json:"manifestRaw"`
}
