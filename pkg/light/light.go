// Package light defines a provider-agnostic interface for network status
// lights, so wizcamera can drive bulbs from different vendors (Wiz, LIFX,
// ...) through one API. Providers register themselves at init time.
package light

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Status is what the light should be showing.
type Status int

const (
	// Free means not in a meeting; the light shows its idle state (off).
	Free Status = iota
	// Busy means in a meeting; the light shows red.
	Busy
)

func (s Status) String() string {
	if s == Busy {
		return "busy"
	}
	return "free"
}

// Config identifies one light and how to reach it. It is JSON-serializable
// so the app can persist the user's selection. ID must be stable across
// reboots and DHCP leases (e.g. a MAC address); Address is the last known
// network address and may be refreshed by the provider.
type Config struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Address  string `json:"address"`
}

// Key uniquely identifies a light across providers.
func (c Config) Key() string {
	return c.Provider + ":" + c.ID
}

// Light controls one status light.
type Light interface {
	// SetStatus tells the light what to show. Implementations should
	// handle transient network issues themselves (retries, re-discovery
	// when the device's address changes).
	SetStatus(Status) error
	// Config returns the light's current config, including any address
	// updates learned since Connect, so callers can re-persist it.
	Config() Config
}

// Provider implements discovery and control for one brand of light.
type Provider interface {
	// Name is the stable identifier stored in Config.Provider.
	Name() string
	// Discover scans the local network for lights.
	Discover(ctx context.Context) ([]Config, error)
	// Connect creates a Light from a previously discovered Config.
	Connect(cfg Config) (Light, error)
}

var (
	providersMu sync.RWMutex
	providers   = map[string]Provider{}
)

// Register makes a provider available for discovery and connection.
// Typically called from a provider package's init.
func Register(p Provider) {
	providersMu.Lock()
	defer providersMu.Unlock()
	providers[p.Name()] = p
}

// Connect creates a Light from a persisted Config using the registered
// provider.
func Connect(cfg Config) (Light, error) {
	providersMu.RLock()
	p, ok := providers[cfg.Provider]
	providersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("light: unknown provider %q", cfg.Provider)
	}
	return p.Connect(cfg)
}

// DiscoverAll runs discovery across all registered providers concurrently
// and returns the union, sorted by name. Provider errors are dropped; a
// network where one brand's scan fails should still surface the others.
func DiscoverAll(ctx context.Context) []Config {
	providersMu.RLock()
	all := make([]Provider, 0, len(providers))
	for _, p := range providers {
		all = append(all, p)
	}
	providersMu.RUnlock()

	var mu sync.Mutex
	var found []Config
	var wg sync.WaitGroup
	for _, p := range all {
		wg.Add(1)
		go func(p Provider) {
			defer wg.Done()
			cfgs, err := p.Discover(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			found = append(found, cfgs...)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found
}
