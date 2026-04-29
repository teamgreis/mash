package runner

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mash-protocol/mash-go/pkg/discovery"
)

// fakeSRVBrowser implements srvBrowser so dialer tests can drive
// browse results synthetically. Each call sends the configured
// services on the returned channel, then closes it. Errors short-
// circuit before any send.
type fakeSRVBrowser struct {
	commissionable []*discovery.CommissionableService
	operational    []*discovery.OperationalService
	commErr        error
	opErr          error

	commCalls    int
	opCalls      int
	opZoneFilter []string // captures the zoneID passed to each BrowseOperational call
}

func (f *fakeSRVBrowser) BrowseCommissionable(ctx context.Context) (<-chan *discovery.CommissionableService, <-chan *discovery.CommissionableService, error) {
	f.commCalls++
	if f.commErr != nil {
		return nil, nil, f.commErr
	}
	added := make(chan *discovery.CommissionableService, len(f.commissionable))
	removed := make(chan *discovery.CommissionableService)
	for _, svc := range f.commissionable {
		added <- svc
	}
	close(added)
	close(removed)
	return added, removed, nil
}

func (f *fakeSRVBrowser) BrowseOperational(ctx context.Context, zoneID string) (<-chan *discovery.OperationalService, error) {
	f.opCalls++
	f.opZoneFilter = append(f.opZoneFilter, zoneID)
	if f.opErr != nil {
		return nil, f.opErr
	}
	out := make(chan *discovery.OperationalService, len(f.operational))
	for _, svc := range f.operational {
		out <- svc
	}
	close(out)
	return out, nil
}

// stubBaseDialer captures whatever address the wrapper hands it so tests
// can assert that SRV resolution actually rewrote the dial target.
type stubBaseDialer struct {
	commTargets []string
	opTargets   []string
}

func (s *stubBaseDialer) DialCommissioning(_ context.Context, target string) (*tls.Conn, error) {
	s.commTargets = append(s.commTargets, target)
	return nil, errors.New("stub: skip TLS handshake")
}

func (s *stubBaseDialer) DialOperational(_ context.Context, target, _ string, _ CryptoState) (*tls.Conn, error) {
	s.opTargets = append(s.opTargets, target)
	return nil, errors.New("stub: skip TLS handshake")
}

func nopDebugf(string, ...any) {}

// --- pure helpers ----------------------------------------------------------

func TestPickAddress_PreferIPv4NonLinkLocal(t *testing.T) {
	addrs := []string{"fe80::1234", "192.168.1.50"}
	got, ok := pickAddress(addrs, 8443, "")
	if !ok {
		t.Fatalf("expected ok, got !ok")
	}
	want := "192.168.1.50:8443"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPickAddress_HostHintBeatsAutoSelection(t *testing.T) {
	addrs := []string{"192.168.1.50", "192.168.1.51"}
	got, ok := pickAddress(addrs, 8443, "192.168.1.51")
	if !ok {
		t.Fatalf("expected ok, got !ok")
	}
	if got != "192.168.1.51:8443" {
		t.Errorf("hostHint not honoured: got %q", got)
	}
}

func TestPickAddress_LoopbackHintIgnored(t *testing.T) {
	// 127.0.0.1 / localhost / ::1 are the typical -target hints when
	// the user doesn't know the real LAN address. They must NOT pin the
	// pick to the loopback interface; otherwise SRV resolution would
	// be a no-op against a device advertising on a real NIC.
	addrs := []string{"192.168.1.50"}
	for _, hint := range []string{"127.0.0.1", "::1", "localhost"} {
		got, ok := pickAddress(addrs, 8443, hint)
		if !ok || got != "192.168.1.50:8443" {
			t.Errorf("hint %q: got (%q,%v), want (\"192.168.1.50:8443\", true)", hint, got, ok)
		}
	}
}

func TestPickAddress_NoAddresses(t *testing.T) {
	if _, ok := pickAddress(nil, 8443, ""); ok {
		t.Errorf("expected !ok for empty address list")
	}
}

func TestPickAddress_OnlyIPv6_ReturnsFirst(t *testing.T) {
	addrs := []string{"2001:db8::1"}
	got, ok := pickAddress(addrs, 8443, "")
	if !ok {
		t.Fatalf("expected ok")
	}
	want := net.JoinHostPort("2001:db8::1", "8443")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHostFromTarget(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"localhost:8443", "localhost"},
		{"192.168.1.10:8443", "192.168.1.10"},
		{"[2001:db8::1]:8443", "2001:db8::1"},
		{"localhost", "localhost"},
		{"", ""},
	}
	for _, c := range cases {
		if got := hostFromTarget(c.in); got != c.want {
			t.Errorf("hostFromTarget(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- dialer integration with mock browser ---------------------------------

func TestSRVResolvingDialer_CommissioningUsesCommSRVPort(t *testing.T) {
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{
				InstanceName: "MASH-1234",
				Port:         18444, // commissioning SRV port (not the literal target)
				Addresses:    []string{"127.0.0.1"},
			},
		},
		operational: []*discovery.OperationalService{
			{
				InstanceName: "0000000000000000-0000000000000000",
				Port:         18443,
				Addresses:    []string{"127.0.0.1"},
			},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	// Caller's literal target is 8443; SRV resolution must replace
	// the port for commissioning dials with 18444.
	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if got, want := len(base.commTargets), 1; got != want {
		t.Fatalf("commTargets calls: got %d, want %d", got, want)
	}
	if got, want := base.commTargets[0], "127.0.0.1:18444"; got != want {
		t.Errorf("commissioning dial target: got %q, want %q", got, want)
	}
}

func TestSRVResolvingDialer_OperationalNoZoneIDUsesLiteral(t *testing.T) {
	// No zoneID hint means we cannot disambiguate operational records on
	// a shared LAN -- fall back to the literal target so single-port
	// deployments and legacy callers keep working unchanged.
	browser := &fakeSRVBrowser{
		operational: []*discovery.OperationalService{
			{Port: 18443, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	_, _ = d.DialOperational(context.Background(), "127.0.0.1:8443", "", CryptoState{})

	if got, want := base.opTargets[0], "127.0.0.1:8443"; got != want {
		t.Errorf("operational dial without zoneID should use literal target: got %q, want %q", got, want)
	}
	if browser.opCalls != 0 {
		t.Errorf("expected no operational SRV browse without zoneID, got %d calls", browser.opCalls)
	}
}

func TestSRVResolvingDialer_OperationalWithZoneIDResolvesSRV(t *testing.T) {
	// With a post-PASE zone-id, operational SRV resolution becomes
	// disambiguated: BrowseOperational filters mDNS records by the
	// requested zone, and we dial whatever port that zone advertised
	// -- mandatory under DEC-082 two-port deployments.
	browser := &fakeSRVBrowser{
		operational: []*discovery.OperationalService{
			{Port: 18445, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	const zoneID = "0123456789abcdef"
	_, _ = d.DialOperational(context.Background(), "127.0.0.1:18444", zoneID, CryptoState{})

	if got, want := base.opTargets[0], "127.0.0.1:18445"; got != want {
		t.Errorf("operational dial: got %q, want %q (SRV-resolved port)", got, want)
	}
	if browser.opCalls != 1 {
		t.Errorf("expected 1 operational SRV browse, got %d", browser.opCalls)
	}
	if got := browser.opZoneFilter[0]; got != zoneID {
		t.Errorf("BrowseOperational zoneID: got %q, want %q", got, zoneID)
	}
}

func TestSRVResolvingDialer_OperationalCachePerZone(t *testing.T) {
	// Different zones may advertise on different operational ports. Cache
	// must be zone-keyed so a hit for zone A doesn't return zone B's port.
	browser := &fakeSRVBrowser{
		operational: []*discovery.OperationalService{
			{Port: 18445, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	// First zone: browse + cache.
	_, _ = d.DialOperational(context.Background(), "127.0.0.1:18444", "zone-a", CryptoState{})
	// Same zone again within TTL: cache hit, no second browse.
	_, _ = d.DialOperational(context.Background(), "127.0.0.1:18444", "zone-a", CryptoState{})
	// Different zone: must browse again (cache miss).
	_, _ = d.DialOperational(context.Background(), "127.0.0.1:18444", "zone-b", CryptoState{})

	if browser.opCalls != 2 {
		t.Errorf("expected 2 operational browses (cache miss for new zone), got %d", browser.opCalls)
	}
}

func TestSRVResolvingDialer_TwoPortDeployment(t *testing.T) {
	// DEC-082: commissioning on one port, operational on another. The
	// runner SRV-resolves commissioning before PASE, then SRV-resolves
	// operational with the post-PASE zone-id. The literal -target may
	// be either port; both must end up on the right wire.
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{Port: 18444, Addresses: []string{"127.0.0.1"}},
		},
		operational: []*discovery.OperationalService{
			{Port: 18445, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	// Caller passes the commissioning port as -target; SRV-resolve handles
	// the rest. (The other direction -- passing the operational port --
	// works the same way: the literal port is overwritten by SRV-resolve.)
	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:18444")
	_, _ = d.DialOperational(context.Background(), "127.0.0.1:18444", "0123456789abcdef", CryptoState{})

	if got, want := base.commTargets[0], "127.0.0.1:18444"; got != want {
		t.Errorf("commissioning: got %q, want %q", got, want)
	}
	if got, want := base.opTargets[0], "127.0.0.1:18445"; got != want {
		t.Errorf("operational: got %q, want %q", got, want)
	}
}

func TestSRVResolvingDialer_BrowseFailureFallsBackToTarget(t *testing.T) {
	// mDNS-lookup failure (browser init OK, browse returns error) must
	// fall back to the literal target -- the whole point of -srv-resolve
	// being "strictly safer than its absence".
	browser := &fakeSRVBrowser{commErr: errors.New("mdns down")}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if got, want := base.commTargets[0], "127.0.0.1:8443"; got != want {
		t.Errorf("expected fallback to literal target, got %q", got)
	}
}

func TestSRVResolvingDialer_NoServicesFallsBackToTarget(t *testing.T) {
	// Browse returns successfully but yields no service entries before
	// timeout: same semantics as a hard error -- fall back so the
	// runner stays usable on networks where mDNS is flaky.
	browser := &fakeSRVBrowser{} // empty slices -> channels close immediately
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if got, want := base.commTargets[0], "127.0.0.1:8443"; got != want {
		t.Errorf("expected fallback, got %q", got)
	}
}

func TestSRVResolvingDialer_ResolveCachedAcrossDials(t *testing.T) {
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{Port: 18444, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)
	d.resolveTTL = 1 * time.Hour // force the cache to win

	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")
	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if browser.commCalls != 1 {
		t.Errorf("expected one Browse call, got %d (cache not honoured)", browser.commCalls)
	}
}

func TestSRVResolvingDialer_ResolveExpiresAfterTTL(t *testing.T) {
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{Port: 18444, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)
	d.resolveTTL = 1 * time.Nanosecond // any second dial expires

	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")
	time.Sleep(2 * time.Millisecond)
	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if browser.commCalls != 2 {
		t.Errorf("expected two Browse calls, got %d (TTL not respected)", browser.commCalls)
	}
}

func TestSRVResolvingDialer_HostHintPreferredWithinOneService(t *testing.T) {
	// pickAddress prefers the address that matches the host hint. The
	// dialer returns on the first service that yields any address, so
	// multi-service hint preference is not currently honoured (this is
	// flagged in the dialer's docstring as future work). Within a single
	// service, however, hint match wins over the auto-select.
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{Port: 18444, Addresses: []string{"10.0.0.5", "192.168.1.50"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	_, _ = d.DialCommissioning(context.Background(), "192.168.1.50:8443")

	if got, want := base.commTargets[0], "192.168.1.50:18444"; got != want {
		t.Errorf("expected hint match: got %q, want %q", got, want)
	}
}

// --- discriminator filter --------------------------------------------------

func TestSRVResolvingDialer_FiltersByDiscriminator(t *testing.T) {
	// Two commissionable records visible (typical on shared LANs with
	// stale entries from earlier runs). With a discriminator filter,
	// only the matching one wins.
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{InstanceName: "MASH-1234", Discriminator: 1234, Port: 11444, Addresses: []string{"127.0.0.1"}},
			{InstanceName: "MASH-2345", Discriminator: 2345, Port: 22444, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 2345)

	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if got, want := base.commTargets[0], "127.0.0.1:22444"; got != want {
		t.Errorf("expected discriminator-filter to pick MASH-2345 port: got %q, want %q", got, want)
	}
}

func TestSRVResolvingDialer_DiscriminatorZero_AcceptsFirst(t *testing.T) {
	// With no discriminator hint, behaviour is the legacy "first match
	// wins" so existing single-port use cases keep working.
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{Discriminator: 1234, Port: 11444, Addresses: []string{"127.0.0.1"}},
			{Discriminator: 2345, Port: 22444, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 0)

	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if got, want := base.commTargets[0], "127.0.0.1:11444"; got != want {
		t.Errorf("expected first-wins fallback: got %q, want %q", got, want)
	}
}

func TestSRVResolvingDialer_DiscriminatorMismatch_FallsBackToTarget(t *testing.T) {
	// Discriminator hint set but no record matches. Falls back to literal
	// target rather than picking a random unmatched record (which could
	// dial a stale/wrong device on the same LAN).
	browser := &fakeSRVBrowser{
		commissionable: []*discovery.CommissionableService{
			{Discriminator: 1234, Port: 11444, Addresses: []string{"127.0.0.1"}},
		},
	}
	base := &stubBaseDialer{}
	d := newSRVResolvingDialerWithBrowser(base, browser, nopDebugf, 9999)

	_, _ = d.DialCommissioning(context.Background(), "127.0.0.1:8443")

	if got, want := base.commTargets[0], "127.0.0.1:8443"; got != want {
		t.Errorf("expected fallback when no record matches discriminator: got %q, want %q", got, want)
	}
}

// Compile-time assertion that the production browser still satisfies
// srvBrowser; if it ever drifts we want a build error here, not a
// production-time fall-through to the base dialer.
var _ srvBrowser = (*discovery.MDNSBrowser)(nil)
