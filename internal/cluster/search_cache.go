package cluster

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

type searchCacheEntry struct {
	done    chan struct{}
	value   GlobalResourceSearch
	err     error
	expires time.Time
}

// Bounded short-lived summary cache. It merges concurrent identical requests;
// query scope is fleet-wide today and must join the key if authorization becomes scoped.
func (service *OnboardingService) cachedIndexSearch(ctx context.Context, repo ResourceIndexRepository, query string) (GlobalResourceSearch, error) {
	key := strings.ToLower(strings.TrimSpace(query))
	if n := len([]rune(key)); n < 2 || n > 128 {
		return GlobalResourceSearch{}, newError(CodeSearchQueryInvalid)
	}
	service.searchMu.Lock()
	if service.searchEntries == nil {
		service.searchEntries = map[string]*searchCacheEntry{}
		service.searchSlots = make(chan struct{}, 4)
	}
	if entry, ok := service.searchEntries[key]; ok {
		select {
		case <-entry.done:
			if time.Now().Before(entry.expires) {
				service.searchMu.Unlock()
				return entry.value, entry.err
			}
			delete(service.searchEntries, key)
		default:
			service.searchMu.Unlock()
			select {
			case <-entry.done:
				return entry.value, entry.err
			case <-ctx.Done():
				return GlobalResourceSearch{}, ctx.Err()
			}
		}
	}
	select {
	case service.searchSlots <- struct{}{}:
	default:
		service.searchMu.Unlock()
		return GlobalResourceSearch{}, newError("PLATFORM_BUSY")
	}
	if len(service.searchEntries) >= 32 {
		for old, entry := range service.searchEntries {
			select {
			case <-entry.done:
				delete(service.searchEntries, old)
			default:
			}
			if len(service.searchEntries) < 32 {
				break
			}
		}
	}
	entry := &searchCacheEntry{done: make(chan struct{})}
	service.searchEntries[key] = entry
	service.searchMu.Unlock()
	go func() {
		defer func() { <-service.searchSlots }()
		read, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		value, err := service.searchResourceIndex(read, repo, key)
		service.searchMu.Lock()
		defer service.searchMu.Unlock()
		entry.value = value
		entry.err = err
		entry.expires = time.Now().Add(10 * time.Second)
		if value.IndexPending {
			entry.expires = time.Now().Add(time.Second)
		}
		encoded, _ := json.Marshal(value)
		if err != nil || len(encoded) > 256<<10 {
			delete(service.searchEntries, key)
		}
		close(entry.done)
	}()
	select {
	case <-entry.done:
		return entry.value, entry.err
	case <-ctx.Done():
		return GlobalResourceSearch{}, ctx.Err()
	}
}
