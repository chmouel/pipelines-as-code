package concurrency

import (
	"container/heap"
	"time"
)

// PriorityQueueItem represents an item in the priority queue
type PriorityQueueItem struct {
	Key          string
	CreationTime time.Time
	Priority     int64 // Unix timestamp for FIFO ordering
	Index        int
}

// PriorityQueue implements a min-heap based priority queue
type PriorityQueue struct {
	items     []*PriorityQueueItem
	itemByKey map[string]*PriorityQueueItem
}

// NewPriorityQueue creates a new priority queue
func NewPriorityQueue() *PriorityQueue {
	return &PriorityQueue{
		items:     []*PriorityQueueItem{},
		itemByKey: make(map[string]*PriorityQueueItem),
	}
}

// IsPending checks if a key is already in the pending queue
func (pq *PriorityQueue) IsPending(key string) bool {
	_, exists := pq.itemByKey[key]
	return exists
}

// Add adds a new item to the priority queue
func (pq *PriorityQueue) Add(key string, creationTime time.Time) {
	if pq.IsPending(key) {
		return // Already in queue
	}

	item := &PriorityQueueItem{
		Key:          key,
		CreationTime: creationTime,
		Priority:     creationTime.UnixNano(),
	}

	heap.Push(pq, item)
}

// Remove removes an item from the priority queue
func (pq *PriorityQueue) Remove(key string) {
	if item, exists := pq.itemByKey[key]; exists {
		heap.Remove(pq, item.Index)
	}
}

// PopItem removes and returns the highest priority item
func (pq *PriorityQueue) PopItem() *PriorityQueueItem {
	if pq.Len() == 0 {
		return nil
	}
	popped := heap.Pop(pq)
	item, ok := popped.(*PriorityQueueItem)
	if !ok {
		panic("failed to type assert popped item to *PriorityQueueItem")
	}
	return item
}

// Peek returns the highest priority item without removing it
func (pq *PriorityQueue) Peek() *PriorityQueueItem {
	if pq.Len() == 0 {
		return nil
	}
	return pq.items[0]
}

// Len returns the number of items in the queue
func (pq *PriorityQueue) Len() int {
	return len(pq.items)
}

// Less implements heap.Interface
func (pq PriorityQueue) Less(i, j int) bool {
	return pq.items[i].Priority < pq.items[j].Priority
}

// Swap implements heap.Interface
func (pq PriorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
	pq.items[i].Index = i
	pq.items[j].Index = j
}

// Push implements heap.Interface
func (pq *PriorityQueue) Push(x interface{}) {
	n := len(pq.items)
	item, ok := x.(*PriorityQueueItem)
	if !ok {
		panic("failed to type assert pushed item to *PriorityQueueItem")
	}
	item.Index = n
	pq.items = append(pq.items, item)
	pq.itemByKey[item.Key] = item
}

// Pop implements heap.Interface
func (pq *PriorityQueue) Pop() interface{} {
	old := pq.items
	n := len(old)
	item := old[n-1]
	item.Index = -1
	pq.items = old[0 : n-1]
	delete(pq.itemByKey, item.Key)
	return item
}

// GetPendingItems returns all pending items in priority order
func (pq *PriorityQueue) GetPendingItems() []string {
	items := make([]string, 0, pq.Len())
	for _, item := range pq.items {
		items = append(items, item.Key)
	}
	return items
}
