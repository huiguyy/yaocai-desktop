package cache

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// LoginCache stores cookie data for each platform to avoid repeated logins
type LoginCache struct {
	mu     sync.RWMutex
	data   map[string]*CacheEntry
	path   string
}

// CacheEntry holds cached login data for a single platform
type CacheEntry struct {
	CookieDict map[string]interface{} `json:"cookie_dict"`
	CookieStr  string                 `json:"cookie_str"`
	Username   string                 `json:"username"`
	CachedAt   int64                  `json:"cached_at"` // Unix timestamp
	TTL        int64                  `json:"ttl"`       // seconds
}

// DefaultTTL is the default cache time-to-live (8 hours)
const DefaultTTL = 8 * 60 * 60

var (
	globalCache *LoginCache
	once        sync.Once
)

// GetCache returns the global login cache instance
func GetCache(path string) *LoginCache {
	once.Do(func() {
		globalCache = &LoginCache{
			data: make(map[string]*CacheEntry),
			path: path,
		}
		globalCache.load()
	})
	if path != "" && globalCache.path != path {
		globalCache.path = path
		globalCache.load()
	}
	return globalCache
}

// load reads cache from disk
func (lc *LoginCache) load() {
	if lc.path == "" {
		lc.path = "login_cache.json"
	}
	data, err := os.ReadFile(lc.path)
	if err != nil {
		return
	}
	var entries map[string]*CacheEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return
	}
	lc.mu.Lock()
	lc.data = entries
	lc.mu.Unlock()
}

// save writes cache to disk
func (lc *LoginCache) save() error {
	lc.mu.RLock()
	data, err := json.MarshalIndent(lc.data, "", "  ")
	lc.mu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(lc.path, data, 0644)
}

// Set stores a login cache entry
func (lc *LoginCache) Set(platform string, cookieDict map[string]interface{}, cookieStr string, username string) {
	lc.mu.Lock()
	lc.data[platform] = &CacheEntry{
		CookieDict: cookieDict,
		CookieStr:  cookieStr,
		Username:   username,
		CachedAt:   time.Now().Unix(),
		TTL:        DefaultTTL,
	}
	lc.mu.Unlock()
	lc.save()
}

// Get retrieves a cached login entry. Returns nil if not found or expired.
func (lc *LoginCache) Get(platform string) *CacheEntry {
	lc.mu.RLock()
	entry, ok := lc.data[platform]
	lc.mu.RUnlock()
	if !ok {
		return nil
	}
	// Check expiry
	if time.Now().Unix()-entry.CachedAt > entry.TTL {
		return nil
	}
	return entry
}

// Clear removes cache for a specific platform (or all if platform=="")
func (lc *LoginCache) Clear(platform string) {
	lc.mu.Lock()
	if platform == "" {
		lc.data = make(map[string]*CacheEntry)
	} else {
		delete(lc.data, platform)
	}
	lc.mu.Unlock()
	lc.save()
}

// Status returns the cache status for all platforms
func (lc *LoginCache) Status() map[string]interface{} {
	lc.mu.RLock()
	defer lc.mu.RUnlock()

	status := make(map[string]interface{})
	now := time.Now().Unix()
	for platform, entry := range lc.data {
		age := now - entry.CachedAt
		remaining := entry.TTL - age
		if remaining < 0 {
			remaining = 0
		}
		status[platform] = map[string]interface{}{
			"code":      100,
			"age":       age,
			"ttl":       entry.TTL,
			"remaining": remaining,
			"expired":   remaining <= 0,
			"username":  entry.Username,
		}
	}
	return status
}

// IsExpired checks if a platform's cache is expired
func (lc *LoginCache) IsExpired(platform string) bool {
	entry := lc.Get(platform)
	return entry == nil
}
