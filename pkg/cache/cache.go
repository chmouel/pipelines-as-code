package cache

import (
	"sync"
	"time"
)

var (
	instance *Cache
	once     sync.Once
)

type Cache struct {
	items map[string]Item
	mu    sync.RWMutex
}

type Item struct {
	Value      interface{}
	Expiration int64
}

func Init(duration string) error {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return err
	}
	GetInstance(d)
	return nil
}

func GetInstance(duration time.Duration) *Cache {
	once.Do(func() {
		instance = New(duration)
	})
	return instance
}

func New(cleanupInterval time.Duration) *Cache {
	items := make(map[string]Item)
	c := &Cache{
		items: items,
	}

	if cleanupInterval > 0 {
		go c.startGC(cleanupInterval)
	}

	return c
}

func (c *Cache) Set(key string, value interface{}, d time.Duration) {
	var expiration int64
	if d > 0 {
		expiration = time.Now().Add(d).UnixNano()
	}
	c.mu.Lock()
	c.items[key] = Item{
		Value:      value,
		Expiration: expiration,
	}
	c.mu.Unlock()
}

func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	item, found := c.items[key]
	if !found {
		c.mu.RUnlock()
		return nil, false
	}
	if item.Expiration > 0 {
		if time.Now().UnixNano() > item.Expiration {
			c.mu.RUnlock()
			return nil, false
		}
	}
	c.mu.RUnlock()
	return item.Value, true
}

func (c *Cache) startGC(cleanupInterval time.Duration) {
	ticker := time.NewTicker(cleanupInterval)
	for range ticker.C {
		c.deleteExpired()
	}
}

func (c *Cache) deleteExpired() {
	c.mu.Lock()
	for k, v := range c.items {
		if v.Expiration > 0 && time.Now().UnixNano() > v.Expiration {
			delete(c.items, k)
		}
	}
	c.mu.Unlock()
}

func (c *Cache) Flush() {
	c.mu.Lock()
	c.items = make(map[string]Item)
	c.mu.Unlock()
}
