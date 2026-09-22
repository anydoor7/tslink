package server

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"tailscale.com/client/tailscale/apitype"
)

// whoisCacheTTL bounds how long a resolved principal is reused for one source
// address. It is short because the answer can change under it -- a device can
// be transferred or removed -- and long enough that a burst of requests from
// one address costs one WhoIs rather than one per request.
const whoisCacheTTL = 60 * time.Second

// whoisEntry stores a cached WhoIs result with expiration.
type whoisEntry struct {
	resp      *apitype.WhoIsResponse
	expiresAt time.Time
}

// whoisCache provides a TTL cache for WhoIs results keyed by IP address.
type whoisCache struct {
	mu      sync.Mutex
	entries map[string]whoisEntry
	ttl     time.Duration
}

func newWhoisCache(ttl time.Duration) *whoisCache {
	return &whoisCache{
		entries: make(map[string]whoisEntry),
		ttl:     ttl,
	}
}

// get returns a cached WhoIs response for the given IP, or nil if not cached/expired.
func (c *whoisCache) get(ip string) *apitype.WhoIsResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[ip]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(c.entries, ip)
		}
		return nil
	}
	return entry.resp
}

// set stores a WhoIs response for the given IP.
func (c *whoisCache) set(ip string, resp *apitype.WhoIsResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[ip] = whoisEntry{
		resp:      resp,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// IdentityResolver answers "who is behind this request address" for every
// consumer on one service node, through a single shared cache.
//
// One instance per node is the point. The proxy's X-Tailscale-* injection and
// the access log's login field both want the same answer for the same address
// in the same request, and before this type each consumer that wanted identity
// carried its own cache or none at all. Sharing one means adding a consumer
// costs no additional WhoIs calls.
//
// Every method is fail-open and nil-safe: a nil resolver, an unavailable local
// client and a WhoIs error are all reported as "not known" rather than as an
// error, because the callers are observability and header enrichment, not
// authorization. Authorization lives in ACLMiddleware and MCPAuthMiddleware,
// which resolve identity themselves and fail closed. Routing those through this
// cache would let a revoked principal keep its access for the rest of a TTL, so
// they deliberately do not use it.
type IdentityResolver struct {
	cache *whoisCache

	mu sync.Mutex
	// client is resolved on first use rather than at construction. A file
	// share with no allow list must start on a node whose local client is
	// unavailable -- making identity a startup dependency would turn a share
	// that works today into one that refuses to start for a log field.
	client *LocalClient
	// provide is nil for a resolver built from an already-open client.
	provide func() (*LocalClient, error)
	// warned keeps a permanently unavailable client from writing one warning
	// per request into the log it is trying to annotate.
	warned bool
}

// NewIdentityResolver returns a resolver that acquires its local client on
// first use by calling provide. provide may be called more than once: an early
// failure is transient often enough that giving up forever would silently cost
// identity for the life of the process.
func NewIdentityResolver(provide func() (*LocalClient, error)) *IdentityResolver {
	if provide == nil {
		return nil
	}
	return &IdentityResolver{cache: newWhoisCache(whoisCacheTTL), provide: provide}
}

// NewStaticIdentityResolver returns a resolver over an already-open local
// client. A nil client yields a nil resolver, so "no identity is available" has
// one representation rather than two.
func NewStaticIdentityResolver(client *LocalClient) *IdentityResolver {
	if client == nil {
		return nil
	}
	return &IdentityResolver{cache: newWhoisCache(whoisCacheTTL), client: client}
}

// localClient returns the client to query, or nil when none can be obtained.
func (r *IdentityResolver) localClient() *LocalClient {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client != nil || r.provide == nil {
		return r.client
	}
	client, err := r.provide()
	if err != nil || client == nil {
		if !r.warned {
			r.warned = true
			slog.Warn("identity unavailable; access log login will be empty for this node", "error", err)
		}
		return nil
	}
	r.client = client
	return r.client
}

// whoIs returns the identity behind remoteAddr, or nil when it is not known.
//
// Only successful lookups are cached. A failure is left uncached on purpose: it
// is usually transient, and caching it would extend one unlucky moment into a
// full TTL of blank identity.
func (r *IdentityResolver) whoIs(ctx context.Context, remoteAddr string) *apitype.WhoIsResponse {
	if r == nil {
		return nil
	}
	ip, _, _ := net.SplitHostPort(remoteAddr)
	if ip == "" {
		ip = remoteAddr
	}
	if cached := r.cache.get(ip); cached != nil {
		return cached
	}
	client := r.localClient()
	if client == nil {
		return nil
	}
	resp, err := client.WhoIs(ctx, remoteAddr)
	if err != nil || resp == nil {
		return nil
	}
	r.cache.set(ip, resp)
	return resp
}

// Principal returns the verified login and node name behind remoteAddr. Both
// are empty when identity cannot be established.
//
// A tagged node reports an empty login rather than the pseudo-user Tailscale
// attributes tagged traffic to. The field exists to answer "which account did
// this", and a tag is a machine: filling it with a tag name would make a fleet
// of tagged nodes read as one very busy person. The node name still identifies
// the caller, so nothing is lost -- it is reported in the field that means
// "machine" instead of the one that means "account".
func (r *IdentityResolver) Principal(ctx context.Context, remoteAddr string) (login, node string) {
	who := r.whoIs(ctx, remoteAddr)
	if who == nil {
		return "", ""
	}
	if who.Node != nil {
		node = who.Node.ComputedName
		if len(who.Node.Tags) > 0 {
			return "", node
		}
	}
	if who.UserProfile != nil {
		login = who.UserProfile.LoginName
	}
	return login, node
}
