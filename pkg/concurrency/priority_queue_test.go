package concurrency

import (
	"testing"
	"time"
)

func TestPriorityQueue_FIFOOrdering(t *testing.T) {
	pq := NewPriorityQueue()

	// Add items with different timestamps
	time1 := time.Now()
	time2 := time1.Add(1 * time.Second)
	time3 := time2.Add(1 * time.Second)

	pq.Add("pr-3", time3)
	pq.Add("pr-1", time1)
	pq.Add("pr-2", time2)

	// Should return items in FIFO order (earliest first)
	expected := []string{"pr-1", "pr-2", "pr-3"}

	for i, expectedKey := range expected {
		item := pq.PopItem()
		if item == nil {
			t.Fatalf("expected item at position %d, got nil", i)
		}
		if item.Key != expectedKey {
			t.Errorf("expected %s at position %d, got %s", expectedKey, i, item.Key)
		}
	}

	// Queue should be empty now
	if pq.Len() != 0 {
		t.Errorf("expected empty queue, got %d items", pq.Len())
	}
}

func TestPriorityQueue_IsPending(t *testing.T) {
	pq := NewPriorityQueue()

	// Initially not pending
	if pq.IsPending("pr-1") {
		t.Error("expected pr-1 to not be pending initially")
	}

	// Add item
	pq.Add("pr-1", time.Now())

	// Should be pending now
	if !pq.IsPending("pr-1") {
		t.Error("expected pr-1 to be pending after adding")
	}

	// Remove item
	pq.Remove("pr-1")

	// Should not be pending after removal
	if pq.IsPending("pr-1") {
		t.Error("expected pr-1 to not be pending after removal")
	}
}

func TestPriorityQueue_GetPendingItems(t *testing.T) {
	pq := NewPriorityQueue()

	// Add items
	pq.Add("pr-1", time.Now())
	pq.Add("pr-2", time.Now().Add(1*time.Second))
	pq.Add("pr-3", time.Now().Add(2*time.Second))

	// Get all pending items
	items := pq.GetPendingItems()

	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}

	// Should be in FIFO order
	expected := []string{"pr-1", "pr-2", "pr-3"}
	for i, item := range items {
		if item != expected[i] {
			t.Errorf("expected %s at position %d, got %s", expected[i], i, item)
		}
	}
}

func TestPriorityQueue_DuplicateAdd(t *testing.T) {
	pq := NewPriorityQueue()

	// Add same item twice
	pq.Add("pr-1", time.Now())
	pq.Add("pr-1", time.Now().Add(1*time.Second))

	// Should only have one item
	if pq.Len() != 1 {
		t.Errorf("expected 1 item after duplicate add, got %d", pq.Len())
	}

	// Should still be pending
	if !pq.IsPending("pr-1") {
		t.Error("expected pr-1 to still be pending after duplicate add")
	}
}
