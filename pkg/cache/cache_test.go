package cache

import (
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestSetGet(t *testing.T) {
	cache := New(1 * time.Minute)
	cache.Set("key", "value", 1*time.Minute)
	val, found := cache.Get("key")
	assert.Assert(t, found)
	assert.Equal(t, "value", val)
}

func TestExpiration(t *testing.T) {
	cache := New(1 * time.Millisecond)
	cache.Set("key", "value", 1*time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	_, found := cache.Get("key")
	assert.Assert(t, !found)
}

func TestSingleton(t *testing.T) {
	instance1 := GetInstance(0)
	instance2 := GetInstance(0)
	assert.Equal(t, instance1, instance2)
}

func TestFlush(t *testing.T) {
	cache := New(1 * time.Minute)
	cache.Set("key", "value", 1*time.Minute)
	cache.Flush()
	_, found := cache.Get("key")
	assert.Assert(t, !found)
}
