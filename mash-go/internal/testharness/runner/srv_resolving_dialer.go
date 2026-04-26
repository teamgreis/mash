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
	base       Dialer
	browser    *discovery.MDNSBrowser
	debugf     func(string, ...any)
	resolveTTL time.Duration

	mu              sync.Mutex
	cachedCommAddr  string
	cachedOpAddr    string
	cachedAt        time.Time
}

// NewSRVResolvingDialer wraps base so that DialCommissioning and DialOperational
// resolve the per-ALPN SRV port via mDNS before each TCP connection.
func NewSRVResolvingDialer(base Dialer, debugf func(string, ...any)) Dialer {
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
	return &srvResolvingDialer{
		base:       base,
		browser:    browser,
		debugf:     debugf,
		resolveTTL: 5 * time.Second,
	}
}

func (d *srvResolvingDialer) DialCommissioning(ctx context.Context, target string) (*tls.Conn, error) {
	resolved := d.resolveCommissioning(ctx, target)
	return d.base.DialCommissioning(ctx, resolved)
}

func (d *srvResolvingDialer) DialOperational(ctx context.Context, target string, crypto CryptoState) (*tls.Conn, error) {
	resolved := d.resolveOperational(ctx, target)
	return d.base.DialOperational(ctx, resolved, crypto)
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
// service, falling back to the literal target on any failure.
func (d *srvResolvingDialer) resolveOperational(ctx context.Context, target string) string {
	d.mu.Lock()
	if d.cachedOpAddr != "" && time.Since(d.cachedAt) < d.resolveTTL {
		addr := d.cachedOpAddr
		d.mu.Unlock()
		return addr
	}
	d.mu.Unlock()

	resolved, ok := d.browseFirstOperational(ctx, target)
	if !ok {
		return target
	}
	d.mu.Lock()
	d.cachedOpAddr = resolved
	d.cachedAt = time.Now()
	d.mu.Unlock()
	if d.debugf != nil {
		d.debugf("srv-resolve: operational %s -> %s", target, resolved)
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
			if addr, matched := pickAddress(svc.Addresses, svc.Port, hostHint); matched {
				return addr, true
			}
		case <-ctx.Done():
			return "", false
		}
	}
}

func (d *srvResolvingDialer) browseFirstOperational(parent context.Context, target string) (string, bool) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()

	ch, err := d.browser.BrowseOperational(ctx, "")
	if err != nil {
		if d.debugf != nil {
			d.debugf("srv-resolve: BrowseOperational err: %v", err)
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
