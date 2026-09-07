package gopowerwall

import "github.com/blackbirdworks/gopowerwall/pkgs/cache"

type (
	ResponseCache = cache.ResponseCache
	CacheEntry    = cache.Entry
)

var (
	NewResponseCache      = cache.NewResponseCache
	WriteOpReadOpCacheMap = cache.WriteOpReadOpCacheMap
)
