package runner

import (
	"crypto/tls"
	"net"
	"testing"

	"github.com/mash-protocol/mash-go/pkg/transport"
)

// fakeConn is a minimal net.Conn for testing socket close behavior.
type fakeConn struct {
	net.Conn
	closed bool
}

func (f *fakeConn) Close() error {
	f.closed = true
	return nil
}

func TestTransitionToDisconnected_ClosesSocket(t *testing.T) {
	fc := &fakeConn{}
	c := &Connection{
		conn:  fc,
		state: ConnTLSConnected,
	}
	c.transitionTo(ConnDisconnected)
	if !fc.closed {
		t.Fatal("expected socket to be closed")
	}
	// transitionTo closes but does NOT nil pointers -- goroutines may still
	// hold references to framer and need to get IO errors, not nil panics.
	if c.conn == nil {
		t.Fatal("transitionTo should not nil conn (close-but-not-nil)")
	}
	if c.state != ConnDisconnected {
		t.Fatalf("expected ConnDisconnected, got %d", c.state)
	}
}

func TestClearConnectionRefs_NilsPointers(t *testing.T) {
	fc := &fakeConn{}
	c := &Connection{
		conn:  fc,
		state: ConnDisconnected,
	}
	c.clearConnectionRefs()
	if c.conn != nil {
		t.Fatal("expected conn to be nil after clearConnectionRefs")
	}
	if c.tlsConn != nil {
		t.Fatal("expected tlsConn to be nil after clearConnectionRefs")
	}
	if c.framer != nil {
		t.Fatal("expected framer to be nil after clearConnectionRefs")
	}
}

func TestTransitionToDisconnected_Idempotent(t *testing.T) {
	c := &Connection{state: ConnDisconnected}
	// Should not panic even with nil socket.
	c.transitionTo(ConnDisconnected)
	if c.state != ConnDisconnected {
		t.Fatalf("expected ConnDisconnected, got %d", c.state)
	}
}

func TestTransitionToOperational_KeepsSocket(t *testing.T) {
	fc := &fakeConn{}
	c := &Connection{
		conn:  fc,
		state: ConnTLSConnected,
	}
	c.transitionTo(ConnOperational)
	if fc.closed {
		t.Fatal("socket should not be closed for non-disconnect transition")
	}
	if c.state != ConnOperational {
		t.Fatalf("expected ConnOperational, got %d", c.state)
	}
}

func TestIsConnected(t *testing.T) {
	c := &Connection{state: ConnDisconnected}
	if c.isConnected() {
		t.Fatal("disconnected should not be connected")
	}
	c.state = ConnTLSConnected
	if !c.isConnected() {
		t.Fatal("TLS connected should be connected")
	}
	c.state = ConnOperational
	if !c.isConnected() {
		t.Fatal("operational should be connected")
	}
}

func TestIsOperational(t *testing.T) {
	c := &Connection{state: ConnDisconnected}
	if c.isOperational() {
		t.Fatal("disconnected should not be operational")
	}
	c.state = ConnTLSConnected
	if c.isOperational() {
		t.Fatal("TLS connected should not be operational")
	}
	c.state = ConnOperational
	if !c.isOperational() {
		t.Fatal("operational should be operational")
	}
}

// TestSwapMainToOperational_DecouplesSuiteAlias is a regression test for the
// F-6 follow-up TC-TRANS-002 zone-leak.
//
// The bug: handleCommission's transition_to_operational path used to mutate
// pool.Main()'s existing Connection struct in place (assigning new tlsConn,
// framer, and state fields). When suite.Conn() aliased pool.Main() (which
// happens after borrowSuiteControlChannelIfAlive or reconnectToZone has set
// both pointers to the same Connection), the in-place mutation made the
// test's new LOCAL zone connection share the suite alias. The teardown's
// alias filter `zc == c.suite.Conn()` then misclassified the test's zone
// connection as the suite zone's, untracked it without sending RemoveZone,
// and the LOCAL zone leaked on the device -- breaking TC-TRANS-002's
// commissioning with "zone type already exists, code 10".
//
// The fix: handleCommission now calls swapMainToOperational, which installs
// a fresh Connection struct via pool.SetMain. This decouples pool.Main from
// any pre-existing alias.
//
// This test verifies the decoupling invariant: setting up the alias pattern
// pre-swap and asserting that pool.Main and suite.Conn refer to distinct
// structs after the swap.
func TestSwapMainToOperational_DecouplesSuiteAlias(t *testing.T) {
	r := newTestRunner()

	// Bug pattern: pool.Main and suite.Conn alias the same Connection struct
	// (the typical state after borrowSuiteControlChannelIfAlive or
	// reconnectToZone has set both pointers to the same Connection).
	pipeA, pipeB := net.Pipe()
	t.Cleanup(func() {
		_ = pipeA.Close()
		_ = pipeB.Close()
	})
	aliased := &Connection{
		state:         ConnOperational,
		conn:          pipeA,
		framer:        transport.NewFramer(pipeA),
		hadConnection: true,
	}
	r.pool.SetMain(aliased)
	r.suite.SetConn(aliased)

	if r.pool.Main() != r.suite.Conn() {
		t.Fatal("test setup: pool.Main and suite.Conn must alias before swap")
	}

	// Build a fake operational TLS conn. Unhandshaked is fine; the swap only
	// stores the pointer.
	opPipeA, opPipeB := net.Pipe()
	t.Cleanup(func() {
		_ = opPipeA.Close()
		_ = opPipeB.Close()
	})
	opConn := tls.Client(opPipeA, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})

	newMain := r.swapMainToOperational(opConn)

	// Decoupling invariant: pool.Main is now the fresh struct, distinct from
	// suite.Conn. Without this, the teardown alias filter would incorrectly
	// treat the test's zone connection as the suite's.
	if r.pool.Main() == r.suite.Conn() {
		t.Fatal("F-6 follow-up regression: pool.Main and suite.Conn must NOT alias after swapMainToOperational")
	}
	if r.pool.Main() != newMain {
		t.Fatal("pool.Main must be the freshly-installed Connection")
	}
	if r.suite.Conn() != aliased {
		t.Fatal("suite.Conn must continue to point to the original (now-closed) struct")
	}
	if r.suite.Conn().isConnected() {
		t.Fatal("the original aliased struct must be disconnected after swap (its socket was closed)")
	}
	if !r.pool.Main().isOperational() {
		t.Fatal("pool.Main must be in operational state after swap")
	}
}
