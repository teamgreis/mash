package runner

import (
	"context"
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mash-protocol/mash-go/pkg/discovery"
)

// srvResolvingDialer resolves the device's actual TCP port via mDNS SRV
// records before each dial. This is the path enabled by `-srv-resolve` and
// closes mash-py-spec-issues Issue #3: today the test harness uses one
// `-target host:port` for both commissioning and operational connections,
// which silently breaks against a DEC-082 two-port deployment (mash-py).
//
// Behaviour:
//   * On commissioning dials, browse `_mash-comm._tcp` and use the SRV port.
//   * On operational dials, browse `_mash._tcp` and use the SRV port.
//   * The host hint from the original target (without port) is used as a
//     filter when multiple services are visible -- preferring the service
//     whose IPs contain the hint, falling back to the first match.
//   * If mDNS resolution fails or times out (macOS browse reliability), the
//     dialer falls back to the literal target so existing single-port use
//     cases keep working. This guarantees `-srv-resolve` is strictly safer
//     than the default: at worst it behaves as if absent.
//
// The base dialer (TLS handshake + zone-CA verification) is delegated to so
// the only behavioural change is the dial address.
type srvResolvingDialer struct {
	base          Dialer
	browser       srvBrowser
	debugf        func(string, ...any)
	resolveTTL    time.Duration
	// discriminator narrows the commissionable browse to a single device.
	// Zero means "accept any commissionable record" (legacy behaviour). On
	// shared LANs the field MUST be set or stale records from earlier
	// runs will silently win the race for "first match".
	discriminator uint16

	mu             sync.Mutex
	cachedCommAddr string
	// cachedOpAddrs maps zoneID -> resolved operational host:port. The empty
	// string key holds the unfiltered (zoneID="") cache entry for callers
	// that can't supply a zone-id (e.g. failsafe paths). DEC-088 makes
	// `_mash._tcp` instances per-zone, so a single shared cache slot would
	// serve the wrong port to a multi-zone harness.
	cachedOpAddrs  map[string]operationalCacheEntry
	cachedAt       time.Time
}

// operationalCacheEntry pairs a resolved address with its insertion time so
// the TTL expiry can be checked alongside the zone-keyed lookup.
type operationalCacheEntry struct {
	addr string
	at   time.Time
}

// srvBrowser narrows discovery.Browser to the two methods srvResolvingDialer
// uses, so unit tests can supply a fake without standing up a real mDNS
// browser. The concrete *discovery.MDNSBrowser satisfies this implicitly.
type srvBrowser interface {
	BrowseCommissionable(ctx context.Context) (added, removed <-chan *discovery.CommissionableService, err error)
	BrowseOperational(ctx context.Context, zoneID string) (<-chan *discovery.OperationalService, error)
}

// NewSRVResolvingDialer wraps base so that DialCommissioning and DialOperational
// resolve the per-ALPN SRV port via mDNS before each TCP connection. The
// discriminator filter (when non-zero) narrows commissionable browse to one
// device; required on shared LANs where stale records can otherwise be
// picked first.
func NewSRVResolvingDialer(base Dialer, debugf func(string, ...any), discriminator uint16) Dialer {
	browser, err := discovery.NewMDNSBrowser(discovery.BrowserConfig{
		BrowseTimeout: 3 * time.Second,
	})
	if err != nil {
		// Browser construction shouldn't fail in practice (no I/O at
		// construction time). If it does, log and degrade gracefully.
		if debugf != nil {
			debugf("srv-resolve: browser init failed: %v -- falling back to base dialer", err)
		}
		return base
	}
	return newSRVResolvingDialerWithBrowser(base, browser, debugf, discriminator)
}

// newSRVResolvingDialerWithBrowser is the constructor exercised by tests; it
// takes the browser dependency directly so the production browser-init path
// is not invoked.
func newSRVResolvingDialerWithBrowser(base Dialer, browser srvBrowser, debugf func(string, ...any), discriminator uint16) *srvResolvingDialer {
	return &srvResolvingDialer{
		base:          base,
		browser:       browser,
		debugf:        debugf,
		resolveTTL:    5 * time.Second,
		discriminator: discriminator,
		cachedOpAddrs: map[string]operationalCacheEntry{},
	}
}

func (d *srvResolvingDialer) DialCommissioning(ctx context.Context, target string) (*tls.Conn, error) {
	resolved := d.resolveCommissioning(ctx, target)
	return d.base.DialCommissioning(ctx, resolved)
}

func (d *srvResolvingDialer) DialOperational(ctx context.Context, target, zoneID string, crypto CryptoState) (*tls.Conn, error) {
	// Operational SRV resolution under DEC-082: when the caller provides a
	// zone-id we can filter `_mash._tcp` advertisements to exactly the
	// device-under-test's operational instance, then dial its SRV-advertised
	// port. Without a zone-id the operational record set on a shared LAN
	// is ambiguous, so we fall back to the literal target -- correct under
	// single-port deployments (mash-go-device) and the legacy harness path.
	if zoneID == "" {
		return d.base.DialOperational(ctx, target, zoneID, crypto)
	}
	resolved := d.resolveOperational(ctx, target, zoneID)
	return d.base.DialOperational(ctx, resolved, zoneID, crypto)
}

// ResolveTarget returns the SRV-resolved address for the requested ALPN, or
// the original target if mDNS browse fails. Operational dials skip SRV
// resolution for the same reason DialOperational does (no zone-id filter).
func (d *srvResolvingDialer) ResolveTarget(ctx context.Context, target string, commissioning bool) string {
	if commissioning {
		return d.resolveCommissioning(ctx, target)
	}
	return target
}

// targetResolver is the runtime contract handleConnect uses to opt into SRV
// resolution. The default tlsDialer does not implement it, so the helper
// short-circuits to the literal target -- behaviour identical to before
// -srv-resolve existed.
type targetResolver interface {
	ResolveTarget(ctx context.Context, target string, commissioning bool) string
}

// resolveCommissioning returns the SRV-resolved address for the commissioning
// service, falling back to the literal target on any failure.
func (d *srvResolvingDialer) resolveCommissioning(ctx context.Context, target string) string {
	d.mu.Lock()
	if d.cachedCommAddr != "" && time.Since(d.cachedAt) < d.resolveTTL {
		addr := d.cachedCommAddr
		d.mu.Unlock()
		return addr
	}
	d.mu.Unlock()

	resolved, ok := d.browseFirstCommissionable(ctx, target)
	if !ok {
		return target
	}
	d.mu.Lock()
	d.cachedCommAddr = resolved
	d.cachedAt = time.Now()
	d.mu.Unlock()
	if d.debugf != nil {
		d.debugf("srv-resolve: commissioning %s -> %s", target, resolved)
	}
	return resolved
}

// resolveOperational returns the SRV-resolved address for the operational
// service of the given zone, falling back to the literal target on any
// failure. zoneID is required: the cache is per-zone because multiple
// operational records can coexist on the same LAN (one per zone).
func (d *srvResolvingDialer) resolveOperational(ctx context.Context, target, zoneID string) string {
	d.mu.Lock()
	if entry, ok := d.cachedOpAddrs[zoneID]; ok && time.Since(entry.at) < d.resolveTTL {
		addr := entry.addr
		d.mu.Unlock()
		if d.debugf != nil {
			d.debugf("srv-resolve: operational zone=%s %s -> %s (cached)", zoneID, target, addr)
		}
		return addr
	}
	d.mu.Unlock()

	resolved, ok := d.browseFirstOperational(ctx, target, zoneID)
	if !ok {
		if d.debugf != nil {
			d.debugf("srv-resolve: operational zone=%s browse miss, falling back to literal %s", zoneID, target)
		}
		return target
	}
	d.mu.Lock()
	d.cachedOpAddrs[zoneID] = operationalCacheEntry{addr: resolved, at: time.Now()}
	d.mu.Unlock()
	if d.debugf != nil {
		d.debugf("srv-resolve: operational zone=%s %s -> %s", zoneID, target, resolved)
	}
	return resolved
}

func (d *srvResolvingDialer) browseFirstCommissionable(parent context.Context, target string) (string, bool) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()

	added, _, err := d.browser.BrowseCommissionable(ctx)
	if err != nil {
		if d.debugf != nil {
			d.debugf("srv-resolve: BrowseCommissionable err: %v", err)
		}
		return "", false
	}

	hostHint := hostFromTarget(target)
	for {
		select {
		case svc, ok := <-added:
			if !ok {
				return "", false
			}
			// On shared LANs many stale records may be visible. When a
			// discriminator hint is configured, skip records whose TXT
			// "D" doesn't match -- otherwise the first stale record
			// wins and the harness silently dials a defunct port.
			if d.discriminator != 0 && svc.Discriminator != d.discriminator {
				if d.debugf != nil {
					d.debugf("srv-resolve: skip %s (D=%d != %d)",
						svc.InstanceName, svc.Discriminator, d.discriminator)
				}
				continue
			}
			if addr, matched := pickAddress(svc.Addresses, svc.Port, hostHint); matched {
				return addr, true
			}
		case <-ctx.Done():
			return "", false
		}
	}
}

// browseFirstOperational waits up to 3 s for an operational service that
// matches zoneID. The Browser filters server-side via the Zone-ID TXT field
// (DEC-088), so this loop only ever sees records the caller asked for --
// no manual sift here. Empty zoneID means "any operational record" and is
// kept for callers that have no zone-id to hand (none today; defensive).
func (d *srvResolvingDialer) browseFirstOperational(parent context.Context, target, zoneID string) (string, bool) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()

	ch, err := d.browser.BrowseOperational(ctx, zoneID)
	if err != nil {
		if d.debugf != nil {
			d.debugf("srv-resolve: BrowseOperational(zone=%s) err: %v", zoneID, err)
		}
		return "", false
	}

	hostHint := hostFromTarget(target)
	for {
		select {
		case svc, ok := <-ch:
			if !ok {
				return "", false
			}
			if addr, matched := pickAddress(svc.Addresses, svc.Port, hostHint); matched {
				return addr, true
			}
		case <-ctx.Done():
			return "", false
		}
	}
}

// pickAddress chooses host:port from a discovered service. When hostHint is
// non-empty and matches one of the service's addresses, prefer it; otherwise
// take the first IPv4 address (or, lacking one, the first address). Returns
// the empty string if the service has no addresses.
func pickAddress(addresses []string, port uint16, hostHint string) (string, bool) {
	if len(addresses) == 0 {
		return "", false
	}
	if hostHint != "" && hostHint != "localhost" && hostHint != "127.0.0.1" && hostHint != "::1" {
		for _, a := range addresses {
			if a == hostHint {
				return net.JoinHostPort(a, strconv.Itoa(int(port))), true
			}
		}
	}
	// Prefer a non-link-local IPv4 if available.
	for _, a := range addresses {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil && !v4.IsLinkLocalUnicast() {
			return net.JoinHostPort(a, strconv.Itoa(int(port))), true
		}
	}
	return net.JoinHostPort(addresses[0], strconv.Itoa(int(port))), true
}

// hostFromTarget strips the :port from a host:port string. Returns the
// original input unchanged if it has no port.
func hostFromTarget(target string) string {
	if !strings.Contains(target, ":") {
		return target
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return target
	}
	return host
}

// Compile-time: the wrapper must satisfy Dialer.
var _ Dialer = (*srvResolvingDialer)(nil)
