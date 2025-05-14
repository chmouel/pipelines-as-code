// Package cache provides caching functionality for Pipelines as Code
// It implements an in-memory cache with time-based expiration
// primarily used for storing GitHub content to reduce API calls
// and avoid rate limiting.
package cache

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jonboulle/clockwork"
)

const (
	// DefaultExpiryDuration is the default duration (2 minutes) used when no expiry headers are present
	DefaultSHAURLExpiryDuration    = 24 * time.Hour  // 24 hours for SHA references
	DefaultNonSHAURLExpiryDuration = 2 * time.Minute // 24 hours for SHA references
)

// Cache is a simple in-memory cache with TTL
// It provides thread-safe storage for content with automatic expiration
type Cache struct {
	items map[string]cacheItem
	ttl   time.Duration
	mu    sync.RWMutex
}

type cacheItem struct {
	value      interface{}
	expiration time.Time
}

// Config holds configuration options for the cache
type Config struct {
	// TTL is the default time-to-live for cache entries
	TTL time.Duration
	// Enabled determines if the cache should be used
	Enabled bool
}

// New creates a new cache with the specified TTL
func New(ttl time.Duration) *Cache {
	return &Cache{
		items: make(map[string]cacheItem),
		ttl:   ttl,
	}
}

// Set adds a key-value pair to the cache
func (c *Cache) Set(key string, value interface{}, expiry time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cacheItem{
		value:      value,
		expiration: expiry,
	}
}

// SetWithTTL adds a key-value pair to the cache with a specific TTL
func (c *Cache) SetWithTTL(key string, value interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cacheItem{
		value:      value,
		expiration: time.Now().Add(ttl),
	}
}

// Get retrieves a value from the cache by key
// Returns the value and a boolean indicating whether the key was found
func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, found := c.items[key]
	if !found {
		return nil, false
	}

	// Check if the item has expired
	if time.Now().After(item.expiration) {
		// Remove expired item in a separate goroutine to not block the read
		go func() {
			c.mu.Lock()
			delete(c.items, key)
			c.mu.Unlock()
		}()
		return nil, false
	}

	return item.value, true
}

// Delete removes a key from the cache
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

// Clear removes all items from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string]cacheItem)
}

// IsSHAReference checks if a string reference is a SHA (and not a branch or tag)
// This is important for caching, since SHA references are immutable in Git,
// while branch and tag references can change over time.
func IsSHAReference(ref string) bool {
	// SHA references are typically 40 character hexadecimal strings
	// This is a simplified check - in a real implementation you might want to do more validation
	if len(ref) != 40 {
		return false
	}

	// Check that all characters are valid hex digits
	for _, c := range ref {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}

	return true
}

// GenerateCacheKey creates a unique key for caching based on repository details and file path
// The format is: owner/repo/sha/path
// This ensures each piece of content has a unique key based on its location and identity
func GenerateCacheKey(owner, repo, sha, path string) string {
	return owner + "/" + repo + "/" + sha + "/" + path
}

// getAbsoluteExpiryTime calculates the absolute expiration time of a resource
// based on HTTP headers, using the provided clock.
//
// This function checks for the following headers in order of priority:
// 1. "Expires" (case-sensitive or lowercase): A direct expiration timestamp.
// 2. "Cache-Control": A "max-age" directive specifying the number of seconds until expiration.
//
// If neither header is present or if parsing fails, it returns a zero time.Time and an error.
//
// Parameters:
//   - clock (clockwork.Clock): The clock to use for determining the current time (for max-age).
//   - headers (map[string][]string): A map of HTTP headers where the key is the header name
//     and the value is a slice of strings (to handle multiple header values).
//
// Returns:
//   - time.Time: The absolute expiration time.
//   - error: An error if no relevant expiration headers are found or if parsing fails.
func getAbsoluteExpiryTime(clock clockwork.Clock, headers map[string][]string) (time.Time, error) {
	// Check for Expires header
	var expiryStr string
	if expiryValues, ok := headers["Expires"]; ok && len(expiryValues) > 0 {
		expiryStr = expiryValues[0]
	} else if expiryValues, ok := headers["expires"]; ok && len(expiryValues) > 0 { // Try lowercase
		expiryStr = expiryValues[0]
	}

	if expiryStr != "" {
		// Parse the Expires header string.
		// Common formats are RFC1123 (which is http.TimeFormat).
		expiryTime, err := time.Parse(http.TimeFormat, expiryStr) // http.TimeFormat is time.RFC1123
		if err != nil {
			// Optionally, try other common date formats if RFC1123 fails.
			return time.Time{}, fmt.Errorf("could not parse 'Expires' header value '%s': %w", expiryStr, err)
		}
		return expiryTime, nil
	}

	// If no Expires header, try Cache-Control with max-age
	if cacheControlValues, ok := headers["Cache-Control"]; ok && len(cacheControlValues) > 0 {
		for _, directive := range cacheControlValues {
			directive = strings.ToLower(strings.TrimSpace(directive)) // Normalize
			if strings.HasPrefix(directive, "max-age=") {
				parts := strings.SplitN(directive, "=", 2)
				if len(parts) == 2 {
					// The value might be followed by other directives, e.g., "max-age=3600, public"
					// So we take the part before the first comma.
					valueStr := strings.SplitN(parts[1], ",", 2)[0]
					valueStr = strings.TrimSpace(valueStr)

					if seconds, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
						if seconds < 0 { // max-age should not be negative
							return time.Time{}, fmt.Errorf("invalid negative 'max-age' value: %d", seconds)
						}
						// Calculate absolute expiry time from max-age using the provided clock
						absoluteExpiry := clock.Now().Add(time.Duration(seconds) * time.Second)
						return absoluteExpiry, nil
					} else {
						return time.Time{}, fmt.Errorf("could not parse 'max-age' value '%s': %w", valueStr, err)
					}
				}
			}
		}
	}

	// Return zero time and an error if no relevant headers are found
	return time.Time{}, fmt.Errorf("no relevant expiration headers ('Expires' or 'Cache-Control: max-age') found")
}

// GetExpiryForRef determines the expiry time for a reference, with fallback to provided TTL
func GetExpiryForRef(clock clockwork.Clock, headers map[string][]string, ref string, fallbackTTL time.Duration) time.Time {
	duration, err := getAbsoluteExpiryTime(clock, headers)
	if err == nil {
		return duration
	}
	// Check if the reference is a SHA
	if IsSHAReference(ref) {
		// If it's a SHA, we can use a longer expiration time
		// Use fallback TTL if provided, otherwise use default
		if fallbackTTL > 0 {
			return clock.Now().Add(fallbackTTL)
		}
		return clock.Now().Add(DefaultSHAURLExpiryDuration)
	}

	// For non-SHA references, use a shorter TTL
	if fallbackTTL > 0 {
		// For non-SHA refs, use a shorter duration even if fallback is long
		shortTTL := fallbackTTL
		if shortTTL > DefaultNonSHAURLExpiryDuration {
			shortTTL = DefaultNonSHAURLExpiryDuration
		}
		return clock.Now().Add(shortTTL)
	}
	return clock.Now().Add(DefaultNonSHAURLExpiryDuration)
}
