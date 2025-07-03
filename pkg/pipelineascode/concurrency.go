package pipelineascode

import (
	"sync"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

type ConcurrencyManager struct {
	enabled      bool
	pipelineRuns []*v1.PipelineRun
	mutex        *sync.Mutex
}

func NewConcurrencyManager() *ConcurrencyManager {
	return &ConcurrencyManager{
		pipelineRuns: []*v1.PipelineRun{},
		mutex:        &sync.Mutex{},
	}
}

func (c *ConcurrencyManager) AddPipelineRun(pr *v1.PipelineRun) {
	if !c.enabled {
		return
	}
	if pr == nil {
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.pipelineRuns = append(c.pipelineRuns, pr)
}

func (c *ConcurrencyManager) Enable() {
	c.enabled = true
}
