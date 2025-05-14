package matcher

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cache"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/hub"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"go.uber.org/zap"
	k8scheme "k8s.io/client-go/kubernetes/scheme"
)

const (
	taskAnnotationsRegexp     = `task(-[0-9]+)?$`
	pipelineAnnotationsRegexp = `pipeline$`

	// Default TTL for cached content (24 hours)
	defaultCacheTTL = 24 * time.Hour
)

// NewRemoteTasks creates a new RemoteTasks instance with cache initialized
func NewRemoteTasks(run *params.Run, event *info.Event, providerInterface provider.Interface, logger *zap.SugaredLogger) *RemoteTasks {
	rt := &RemoteTasks{
		Run:               run,
		Event:             event,
		ProviderInterface: providerInterface,
		Logger:            logger,
		fileCache:         cache.New(defaultCacheTTL),
	}

	return rt
}

type RemoteTasks struct {
	Run               *params.Run
	ProviderInterface provider.Interface
	Event             *info.Event
	Logger            *zap.SugaredLogger

	// Cache for remote content
	fileCache   *cache.Cache
	cacheConfig *cache.Config
}

// nolint: dupl
func (rt *RemoteTasks) convertToPipeline(ctx context.Context, uri, data string) (*tektonv1.Pipeline, error) {
	decoder := k8scheme.Codecs.UniversalDeserializer()
	obj, _, err := decoder.Decode([]byte(data), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("remote pipeline from uri: %s cannot be parsed as a kubernetes resource: %w", uri, err)
	}

	var pipeline *tektonv1.Pipeline
	switch o := obj.(type) {
	case *tektonv1.Pipeline:
		pipeline = o
	case *tektonv1beta1.Pipeline: //nolint: staticcheck
		c := &tektonv1.Pipeline{}
		// TODO: figure ou the issue we have with setdefault setting defaults SA
		// and then don't let pipeline do its job to automatically set a
		// pipeline on configuration
		// o.SetDefaults(ctx)
		// ctx2 := features.SetFeatureFlag(context.Background())
		// if err := o.Validate(ctx2); err != nil {
		// return nil, fmt.Errorf("remote pipeline from uri: %s with name %s cannot be validated: %w", uri, o.GetName(), err)
		// }
		if err := o.ConvertTo(ctx, c); err != nil {
			return nil, fmt.Errorf("remote pipeline from uri: %s with name %s cannot be converted to v1beta1: %w", uri, o.GetName(), err)
		}
		pipeline = c
	default:
		return nil, fmt.Errorf("remote pipeline from uri: %s has not been recognized as a tekton pipeline: %v", uri, o)
	}

	return pipeline, nil
}

// nolint: dupl
// golint has decided that it is a duplication with convertToPipeline but i swear it isn't does two are different function
// and not even sure this is possible to do this with generic crazyness.
func (rt *RemoteTasks) convertTotask(ctx context.Context, uri, data string) (*tektonv1.Task, error) {
	decoder := k8scheme.Codecs.UniversalDeserializer()
	obj, _, err := decoder.Decode([]byte(data), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("remote task from uri: %s cannot be parsed as a kubernetes resource: %w", uri, err)
	}

	var task *tektonv1.Task
	switch o := obj.(type) {
	case *tektonv1.Task:
		task = o
	case *tektonv1beta1.Task: //nolint: staticcheck // we need to support v1beta1
		c := &tektonv1.Task{}
		// o.SetDefaults(ctx)
		// if err := o.Validate(ctx); err != nil {
		// 	return nil, fmt.Errorf("remote task %s cannot be validated properly: err: %w", o.GetName(), err)
		// return nil, fmt.Errorf("remote task from uri: %s with name %s cannot be validated: %w", uri, o.GetName(), err)
		// }
		if err := o.ConvertTo(ctx, c); err != nil {
			return nil, fmt.Errorf("remote task from uri: %s with name %s cannot be converted to v1beta1: %w", uri, o.GetName(), err)
		}
		task = c
	default:
		return nil, fmt.Errorf("remote task from uri: %s has not been recognized as a tekton task: %v", uri, o)
	}

	return task, nil
}

func (rt *RemoteTasks) getRemote(ctx context.Context, uri string, fromHub bool, kind string) (string, error) {
	// If the fetchedFromURIFromProvider is true, then provider has already dealt with the URI
	if fetchedFromURIFromProvider, task, err := rt.ProviderInterface.GetTaskURI(ctx, rt.Event, uri); fetchedFromURIFromProvider {
		return task, err
	}

	// Generate a cache key based on the request parameters
	cacheKey := fmt.Sprintf("%s-%s-%v", uri, kind, fromHub)

	// Check cache if it exists
	if rt.fileCache != nil {
		if cachedContent, found := rt.fileCache.Get(cacheKey); found {
			if rt.Logger != nil {
				rt.Logger.Debugf("Cache hit for %s (kind: %s)", uri, kind)
			}
			return cachedContent.(string), nil
		}

		if rt.Logger != nil {
			rt.Logger.Debugf("Cache miss for %s (kind: %s)", uri, kind)
		}
	}

	var result string
	var err error
	var expiryHeader string

	switch {
	case strings.HasPrefix(uri, "https://"), strings.HasPrefix(uri, "http://"): // if it starts with http(s)://, it is a remote resource
		// For HTTP(S) URLs, we'll check for Expiry headers
		data, headers, err := rt.Run.Clients.GetURLWithHeaders(ctx, uri)
		if err != nil {
			return "", err
		}
		rt.Logger.Infof("successfully fetched %s from remote https url", uri)
		result = string(data)

		// Check for Expires header
		if expiry, ok := headers["Expires"]; ok && len(expiry) > 0 {
			expiryHeader = expiry[0]
			if rt.Logger != nil {
				rt.Logger.Debugf("Found Expires header for %s: %s", uri, expiryHeader)
			}
		} else if expiry, ok := headers["expires"]; ok && len(expiry) > 0 {
			// Try lowercase version too
			expiryHeader = expiry[0]
			if rt.Logger != nil {
				rt.Logger.Debugf("Found expires header for %s: %s", uri, expiryHeader)
			}
		} else if cacheControl, ok := headers["Cache-Control"]; ok && len(cacheControl) > 0 {
			// If no Expires header, try Cache-Control with max-age
			for _, directive := range cacheControl {
				if strings.Contains(directive, "max-age=") {
					parts := strings.Split(directive, "max-age=")
					if len(parts) > 1 {
						// Parse the max-age value and convert to seconds
						if seconds, err := strconv.Atoi(strings.Split(parts[1], ",")[0]); err == nil {
							expiryHeader = time.Now().Add(time.Duration(seconds) * time.Second).Format(time.RFC1123)
							if rt.Logger != nil {
								rt.Logger.Debugf("Found Cache-Control max-age for %s: %d seconds", uri, seconds)
							}
							break
						}
					}
				}
			}
		}

	case fromHub && strings.Contains(uri, "://"): // if it contains ://, it is a remote custom catalog
		split := strings.Split(uri, "://")
		catalogID := split[0]
		value, _ := rt.Run.Info.Pac.HubCatalogs.Load(catalogID)
		if _, ok := rt.Run.Info.Pac.HubCatalogs.Load(catalogID); !ok {
			rt.Logger.Infof("custom catalog %s is not found, skipping", catalogID)
			return "", nil
		}
		uri = strings.TrimPrefix(uri, fmt.Sprintf("%s://", catalogID))
		data, err := hub.GetResource(ctx, rt.Run, catalogID, uri, kind)
		if err != nil {
			return "", err
		}
		catalogValue, ok := value.(settings.HubCatalog)
		if !ok {
			return "", fmt.Errorf("could not get details for catalog name: %s", catalogID)
		}
		rt.Logger.Infof("successfully fetched %s %s from custom catalog HUB %s on URL %s", kind, uri, catalogID, catalogValue.URL)
		result = data

	case strings.Contains(uri, "/"): // if it contains a slash, it is a file inside a repository
		if rt.Event.SHA != "" {
			data, err := rt.ProviderInterface.GetFileInsideRepo(ctx, rt.Event, uri, "")
			if err != nil {
				return "", err
			}
			rt.Logger.Infof("successfully fetched %s inside repository", uri)
			result = data
		} else {
			data, err := getFileFromLocalFS(uri, rt.Logger)
			if err != nil {
				return "", err
			}
			if data == "" {
				return "", nil
			}
			rt.Logger.Infof("successfully fetched %s from local filesystem", uri)
			result = data
		}

	case fromHub: // finally a simple word will fetch from the default catalog (if enabled)
		data, err := hub.GetResource(ctx, rt.Run, "default", uri, kind)
		if err != nil {
			return "", err
		}
		value, _ := rt.Run.Info.Pac.HubCatalogs.Load("default")
		catalogValue, ok := value.(settings.HubCatalog)
		if !ok {
			return "", fmt.Errorf("could not get details for catalog name: %s", "default")
		}
		rt.Logger.Infof("successfully fetched %s %s from default configured catalog HUB on URL: %s", uri, kind, catalogValue.URL)
		result = data

	default:
		return "", fmt.Errorf(`cannot find "%s" anywhere`, uri)
	}

	// Store in cache if we successfully fetched the content and have an expiry header
	if expiryHeader != "" && result != "" {
		// Initialize cache if not already done
		if rt.fileCache == nil {
			// Default to 24 hours TTL, but we'll respect the expiry time from headers
			rt.fileCache = cache.New(defaultCacheTTL)
		}

		// Parse the expiry time from the header
		expiryTime, err := time.Parse(time.RFC1123, expiryHeader)
		if err != nil {
			// Try other common formats if RFC1123 fails
			expiryTime, err = http.ParseTime(expiryHeader)
		}

		if err == nil {
			// Calculate TTL as duration from now to expiry time
			ttl := time.Until(expiryTime)

			// Only cache if expiry is in the future
			if ttl > 0 {
				rt.fileCache.SetWithTTL(cacheKey, result, ttl)
				if rt.Logger != nil {
					rt.Logger.Debugf("Cached %s (kind: %s) until %s (TTL: %v)", uri, kind, expiryTime.Format(time.RFC3339), ttl)
				}
			}
		} else if rt.Logger != nil {
			rt.Logger.Warnf("Could not parse Expiry header '%s' for %s: %v", expiryHeader, uri, err)
		}
	}

	return result, err
}

func grabValuesFromAnnotations(annotations map[string]string, annotationReg string) ([]string, error) {
	rtareg := regexp.MustCompile(fmt.Sprintf("%s/%s", pipelinesascode.GroupName, annotationReg))
	var ret []string
	for annotationK, annotationV := range annotations {
		if !rtareg.MatchString(annotationK) {
			continue
		}
		items, err := getAnnotationValues(annotationV)
		if err != nil {
			return ret, err
		}
		ret = append(items, ret...)
	}
	return ret, nil
}

func GrabTasksFromAnnotations(annotations map[string]string) ([]string, error) {
	return grabValuesFromAnnotations(annotations, taskAnnotationsRegexp)
}

func GrabPipelineFromAnnotations(annotations map[string]string) (string, error) {
	pipelinesAnnotation, err := grabValuesFromAnnotations(annotations, pipelineAnnotationsRegexp)
	if err != nil {
		return "", err
	}
	if len(pipelinesAnnotation) > 1 {
		return "", fmt.Errorf("only one pipeline is allowed on remote resolution, we have received multiple of them: %+v", pipelinesAnnotation)
	}
	if len(pipelinesAnnotation) == 0 {
		return "", nil
	}
	return pipelinesAnnotation[0], nil
}

func (rt *RemoteTasks) GetTaskFromAnnotationName(ctx context.Context, name string) (*tektonv1.Task, error) {
	data, err := rt.getRemote(ctx, name, true, "task")
	if err != nil {
		return nil, fmt.Errorf("error getting remote task \"%s\": %w", name, err)
	}
	if data == "" {
		return nil, fmt.Errorf("could not get remote task \"%s\": returning empty", name)
	}

	task, err := rt.convertTotask(ctx, name, data)
	if err != nil {
		return nil, err
	}
	return task, nil
}

func (rt *RemoteTasks) GetPipelineFromAnnotationName(ctx context.Context, name string) (*tektonv1.Pipeline, error) {
	data, err := rt.getRemote(ctx, name, true, "pipeline")
	if err != nil {
		return nil, fmt.Errorf("error getting remote pipeline \"%s\": %w", name, err)
	}
	if data == "" {
		return nil, fmt.Errorf("could not get remote pipeline \"%s\": returning empty", name)
	}

	pipeline, err := rt.convertToPipeline(ctx, name, data)
	if err != nil {
		return nil, err
	}
	return pipeline, nil
}

// GetCachedTask attempts to get a parsed task from cache
// Returns the parsed task and true if found in cache, nil and false otherwise
func (rt *RemoteTasks) GetCachedTask(ctx context.Context, taskName string) (*tektonv1.Task, bool) {
	if rt.fileCache == nil {
		return nil, false
	}

	// Generate cache key matching the format used in getRemote
	// For tasks from annotations, fromHub=true and kind="task"
	cacheKey := fmt.Sprintf("%s-%s-%v", taskName, "task", true)

	if cachedContent, found := rt.fileCache.Get(cacheKey); found {
		if rt.Logger != nil {
			rt.Logger.Infof("Cross-run cache hit for task %s", taskName)
		}

		// Parse the cached content
		if contentStr, ok := cachedContent.(string); ok {
			task, err := rt.convertTotask(ctx, taskName, contentStr)
			if err != nil {
				if rt.Logger != nil {
					rt.Logger.Infof("Failed to parse cached task %s: %v", taskName, err)
				}
				return nil, false
			}
			return task, true
		}
	}

	return nil, false
}

// GetCachedPipeline attempts to get a parsed pipeline from cache
// Returns the parsed pipeline and true if found in cache, nil and false otherwise
func (rt *RemoteTasks) GetCachedPipeline(ctx context.Context, pipelineName string) (*tektonv1.Pipeline, bool) {
	if rt.fileCache == nil {
		return nil, false
	}

	// Generate cache key matching the format used in getRemote
	// For pipelines from annotations, fromHub=true and kind="pipeline"
	cacheKey := fmt.Sprintf("%s-%s-%v", pipelineName, "pipeline", true)

	if cachedContent, found := rt.fileCache.Get(cacheKey); found {
		if rt.Logger != nil {
			rt.Logger.Infof("Cross-run cache hit for pipeline %s", pipelineName)
		}

		// Parse the cached content
		if contentStr, ok := cachedContent.(string); ok {
			pipeline, err := rt.convertToPipeline(ctx, pipelineName, contentStr)
			if err != nil {
				if rt.Logger != nil {
					rt.Logger.Infof("Failed to parse cached pipeline %s: %v", pipelineName, err)
				}
				return nil, false
			}
			return pipeline, true
		}
	}

	return nil, false
}

// getFileFromLocalFS get task locally if file exist
// TODO: may want to try chroot to the git root dir first as well if we are able so.
func getFileFromLocalFS(fileName string, logger *zap.SugaredLogger) (string, error) {
	var data string
	// We are most probably running with tkn pac resolve -f here, so
	// let's try by any chance to check locally if the task is here on
	// the filesystem
	if _, err := os.Stat(fileName); errors.Is(err, os.ErrNotExist) {
		logger.Warnf("could not find remote file %s inside Repo", fileName)
		return "", nil
	}

	b, err := os.ReadFile(fileName)
	data = string(b)
	if err != nil {
		return "", err
	}
	return data, nil
}

// UpdateCacheConfig updates the cache configuration
// This method is maintained for backward compatibility but no longer controls caching behavior
// Caching is now determined by HTTP headers from the server
func (rt *RemoteTasks) UpdateCacheConfig(enabled bool, ttl time.Duration) {
	if rt.fileCache == nil {
		rt.fileCache = cache.New(defaultCacheTTL)
	}

	if rt.Logger != nil {
		rt.Logger.Infof("Cache is now controlled by HTTP headers instead of environment variables")
	}
}

// InitCacheFromEnv is retained for backward compatibility
// It initializes a basic cache but no longer uses environment variables for configuration
// Caching is now determined by HTTP headers from the server
func (rt *RemoteTasks) InitCacheFromEnv() {
	// Initialize a cache with default settings
	if rt.fileCache == nil {
		rt.fileCache = cache.New(defaultCacheTTL)
	}

	if rt.Logger != nil {
		rt.Logger.Infof("Cache is now controlled by HTTP headers instead of environment variables")
	}
}
