package sync

import (
	"sync"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	tektonVersionedClient "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
)

// QueueManagerRegistry provides a clean way to share QueueManager instances
// and clients between different components of the system (controller and watcher)
type QueueManagerRegistry struct {
	queueManager QueueManagerInterface
	tektonClient tektonVersionedClient.Interface
	pacClient    versioned.Interface
	mu           sync.RWMutex
}

// defaultRegistry is the default registry instance
var defaultRegistry = &QueueManagerRegistry{}

// RegisterQueueManager registers a QueueManager instance
func RegisterQueueManager(qm QueueManagerInterface) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	defaultRegistry.queueManager = qm
}

// RegisterClients registers the Tekton and PAC clients
func RegisterClients(tektonClient tektonVersionedClient.Interface, pacClient versioned.Interface) {
	defaultRegistry.mu.Lock()
	defer defaultRegistry.mu.Unlock()
	defaultRegistry.tektonClient = tektonClient
	defaultRegistry.pacClient = pacClient
}

// GetRegisteredQueueManager returns the registered QueueManager instance
func GetRegisteredQueueManager() QueueManagerInterface {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	return defaultRegistry.queueManager
}

// GetRegisteredClients returns the registered Tekton and PAC clients
func GetRegisteredClients() (tektonVersionedClient.Interface, versioned.Interface) {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	return defaultRegistry.tektonClient, defaultRegistry.pacClient
}

// IsQueueManagerRegistered checks if a QueueManager is registered
func IsQueueManagerRegistered() bool {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	return defaultRegistry.queueManager != nil
}

// AreClientsRegistered checks if clients are registered
func AreClientsRegistered() bool {
	defaultRegistry.mu.RLock()
	defer defaultRegistry.mu.RUnlock()
	return defaultRegistry.tektonClient != nil && defaultRegistry.pacClient != nil
}
