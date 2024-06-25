package pipelineascode

import (
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gopkg.in/yaml.v2"
)

// "oci://image-registry.openshift-image-registry.svc:5000/$(context.pipelineRun.namespace)/cache-go:{{hash}}",

type CachedStepActions struct {
	Task       string   `yaml:"task"`
	FetchAfter string   `yaml:"fetch_after"`
	SaveBefore string   `yaml:"save_before"`
	Patterns   []string `yaml:"patterns"`
	CachePath  string   `yaml:"cache-path"`
	Target     string   `yaml:"target"`
}

func addStepCaching(prs []*tektonv1.PipelineRun) error {
	for _, pr := range prs {
		caching_value, ok := pr.GetAnnotations()[keys.Caching]
		if !ok {
			continue
		}
		var caching []CachedStepActions

		// try to decode the caching_value as CachedStepActions
		if err := yaml.Unmarshal([]byte(caching_value), &caching); err != nil {
			return fmt.Errorf("PipelineRun %s failed to unmarshal %s caching value: %w", pr.GetGenerateName(), keys.Caching, err)
		}
		if pr.Spec.PipelineSpec == nil {
			return fmt.Errorf("PipelineRun %s caching injection fail, remote pipeline is not supported for caching", pr.GetGenerateName())
		}
		for _, task := range pr.Spec.PipelineSpec.Tasks {
			for _, config := range caching {
				if task.Name == config.Task {
					if task.TaskSpec == nil {
						return fmt.Errorf("PipelineRun %s caching injection fail, task %s, remote task is not supported for caching", pr.GetGenerateName(), task.Name)
					}
					retsteps := []tektonv1.Step{}
					defaultParams := []tektonv1.Param{
						{
							Name: "patterns",
							Value: tektonv1.ParamValue{
								ArrayVal: config.Patterns,
								Type:     tektonv1.ParamTypeArray,
							},
						},
						{
							Name: "workingdir",
							Value: tektonv1.ParamValue{
								StringVal: "$(workspaces.source.path)",
								Type:      tektonv1.ParamTypeString,
							},
						},
						{
							Name: "cachePath",
							Value: tektonv1.ParamValue{
								StringVal: config.CachePath,
								Type:      tektonv1.ParamTypeString,
							},
						},
					}
					for _, step := range task.TaskSpec.Steps {
						if step.Name == config.SaveBefore {
							params := defaultParams
							params = append(params, tektonv1.Param{
								Name: "target",
								Value: tektonv1.ParamValue{
									Type:      tektonv1.ParamTypeString,
									StringVal: config.Target,
								},
							})

							retsteps = append(retsteps, tektonv1.Step{
								Name:   "cache-upload",
								Params: params,
								Ref: &tektonv1.Ref{
									ResolverRef: tektonv1.ResolverRef{
										Resolver: tektonv1.ResolverName("http"),
										Params: tektonv1.Params{
											{
												Name: "url",
												Value: tektonv1.ParamValue{
													Type:      tektonv1.ParamTypeString,
													StringVal: "https://raw.githubusercontent.com/openshift-pipelines/tekton-caches/main/tekton/cache-upload.yaml",
												},
											},
										},
									},
								},
							})
						}
						retsteps = append(retsteps, step)
						if step.Name == config.FetchAfter {
							params := defaultParams
							params = append(params, tektonv1.Param{
								Name: "source",
								Value: tektonv1.ParamValue{
									Type:      tektonv1.ParamTypeString,
									StringVal: config.Target,
								},
							})
							retsteps = append(retsteps, tektonv1.Step{
								Name:   "cache-fetch",
								Params: params,
								Ref: &tektonv1.Ref{
									ResolverRef: tektonv1.ResolverRef{
										Resolver: tektonv1.ResolverName("http"),
										Params: tektonv1.Params{
											{
												Name: "url",
												Value: tektonv1.ParamValue{
													Type:      tektonv1.ParamTypeString,
													StringVal: "https://raw.githubusercontent.com/openshift-pipelines/tekton-caches/main/tekton/cache-fetch.yaml",
												},
											},
										},
									},
								},
							})
						}
					}
					task.TaskSpec.Steps = retsteps
				}
			}
		}
	}
	return nil
}
