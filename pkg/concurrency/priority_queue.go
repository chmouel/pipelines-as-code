package concurrency

import (
	"container/heap"
	"time"
)

// PriorityQueueItem represents an item in the priority queue.
type PriorityQueueItem struct {
	Key          string
	CreationTime time.Time
	priority     int64
	index        int
}

// PriorityQueue implements a priority queue for pipeline run keys.
//
// IMPORTANT: This implementation is NOT thread-safe. All operations must be
// protected by external synchronization (e.g., mutex) when used concurrently.
// The queueManager handles this synchronization automatically.
type PriorityQueue struct {
	items     []*PriorityQueueItem
	itemByKey map[string]*PriorityQueueItem
}

// NewPriorityQueue creates a new priority queue.
// The returned queue is not thread-safe and requires external synchronization.
func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		items:     make([]*PriorityQueueItem, 0),
		itemByKey: make(map[string]*PriorityQueueItem),
	}
}

// Add adds an item to the priority queue with the given creation time as priority.
func (pq *PriorityQueue) Add(key string, creationTime time.Time) {
	if _, exists := pq.itemByKey[key]; exists {
		return // Item already exists
	}

	item := &PriorityQueueItem{
		Key:          key,
		CreationTime: creationTime,
		priority:     creationTime.UnixNano(),
	}
	heap.Push(pq, item)
}

// Remove removes an item from the priority queue.
func (pq *PriorityQueue) Remove(key string) {
	if item, exists := pq.itemByKey[key]; exists {
		heap.Remove(pq, item.index)
		delete(pq.itemByKey, key)
	}
}

// PopItem removes and returns the highest priority item.
func (pq *PriorityQueue) PopItem() *PriorityQueueItem {
	if len(pq.items) == 0 {
		return nil
	}
	popped := heap.Pop(pq)
	item, ok := popped.(*PriorityQueueItem)
	if !ok {
		return nil
	}
	return item
}

// Peek returns the highest priority item without removing it.
func (pq *PriorityQueue) Peek() *PriorityQueueItem {
	if len(pq.items) == 0 {
		return nil
	}
	return pq.items[0]
}

// Contains checks if an item exists in the queue.
func (pq *PriorityQueue) Contains(key string) bool {
	_, exists := pq.itemByKey[key]
	return exists
}

// GetPendingItems returns all pending items as a slice of keys.
func (pq *PriorityQueue) GetPendingItems() []string {
	keys := make([]string, 0, len(pq.items))
	for _, item := range pq.items {
		keys = append(keys, item.Key)
	}
	return keys
}

// Len returns the number of items in the queue.
func (pq *PriorityQueue) Len() int {
	return len(pq.items)
}

// heap.Interface implementation.
func (pq *PriorityQueue) Less(i, j int) bool {
	return pq.items[i].priority < pq.items[j].priority
}

func (pq *PriorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.items[i].index = i
	pq.items[j].index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	n := len(pq.items)
	item, ok := x.(*PriorityQueueItem)
	if !ok {
		return
	}
	item.index = n
	pq.items = append(pq.items, item)
	pq.itemByKey[item.Key] = item
}

func (pq *PriorityQueue) Pop() interface{} {
	old := pq.items
	n := len(old)
	item := old[n-1]
	item.index = -1
	pq.items = old[0 : n-1]
	delete(pq.itemByKey, item.Key)
	return item
}
