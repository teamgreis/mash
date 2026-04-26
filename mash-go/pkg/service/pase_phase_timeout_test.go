package service

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// readPASEResponse reads a length-prefixed PASE message from the connection
// and asserts it is a PASEResponse (not a CommissioningError).
func readPASEResponse(t *testing.T, conn net.Conn) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	length := make([]byte, 4)
	if _, err := io.ReadFull(conn, length); err != nil {
		t.Fatalf("read length: %v", err)
	}

	msgLen := binary.BigEndian.Uint32(length)
	data := make([]byte, msgLen)
	if _, err := io.ReadFull(conn, data); err != nil {
		t.Fatalf("read data: %v", err)
	}

	msg, err := commissioning.DecodePASEMessage(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	switch msg.(type) {
	case *commissioning.PASEResponse:
		// Expected — device is proceeding with PASE handshake.
	case *commissioning.CommissioningError:
		t.Fatalf("device sent CommissioningError instead of PASEResponse: %+v", msg)
	default:
		t.Fatalf("expected *PASEResponse, got %T", msg)
	}
}

// sendRealPASERequest sends a PASERequest with a valid SPAKE2+ public value
// derived from the given setup code. This ensures the device's ProcessClientValue
// succeeds and proceeds to send PASEResponse (rather than rejecting with
// CommissioningError for invalid public key).
func sendRealPASERequest(t *testing.T, conn net.Conn, setupCode string) {
	t.Helper()

	code := commissioning.MustParseSetupCode(setupCode)
	// Discriminator and ClientIdentity per DEC-084: only ClientIdentity must
	// equal the protocol-constant for the device to proceed past §4.2 check;
	// discriminator is irrelevant for this timeout test.
	spake, err := commissioning.NewSPAKE2PlusClient(code, 0)
	if err != nil {
		t.Fatalf("create SPAKE2+ client: %v", err)
	}

	req := &commissioning.PASERequest{
		MsgType:        commissioning.MsgPASERequest,
		PublicValue:    spake.PublicValue(),
		ClientIdentity: commissioning.ControllerIdentity,
	}
	data, err := cbor.Marshal(req)
	if err != nil {
		t.Fatalf("marshal PASERequest: %v", err)
	}

	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	if _, err := conn.Write(length); err != nil {
		t.Fatalf("write length: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write data: %v", err)
	}
}

// TestDeviceService_NoPerPhasePASETimeout demonstrates that the device uses a
// single overall HandshakeTimeout instead of per-phase timeouts.
//
// Root cause of TC-PASE-003: The spec requires 30s per-phase timeouts (e.g.,
// PASE_WAIT_V: waiting for PASEConfirm after sending PASEResponse). The device
// uses a single HandshakeTimeout for the entire handshake. When a client sends
// PASERequest but never sends PASEConfirm, the device should close the
// connection after a per-phase timeout (≤1s in this test), but instead waits
// the full HandshakeTimeout.
//
// RED: device waits full HandshakeTimeout (3s) instead of per-phase timeout.
func TestDeviceService_NoPerPhasePASETimeout(t *testing.T) {
	svc := startCappedDevice(t, 2)
	addr := svc.CommissioningAddr()

	// Use a short per-phase timeout so the test completes quickly.
	// The overall HandshakeTimeout is intentionally large to confirm
	// it's the per-phase timeout that fires, not the overall one.
	svc.config.HandshakeTimeout = 10 * time.Second
	svc.config.PASEPhaseTimeout = 500 * time.Millisecond

	// Step 1: TLS connect + send PASERequest with valid SPAKE2+ public value.
	conn := dialCommissioning(t, addr)
	defer conn.Close()
	sendRealPASERequest(t, conn, svc.config.SetupCode)

	// Step 2: Read PASEResponse — device has sent its Y value and is now
	// blocking on readMessageWithContext waiting for PASEConfirm.
	readPASEResponse(t, conn)

	// Step 3: Stall — don't send PASEConfirm. Measure how long until the
	// device closes the connection (EOF or reset).
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	_, _ = conn.Read(buf) // blocks until server closes or deadline expires
	elapsed := time.Since(start)

	// The device should close the connection within PASEPhaseTimeout (500ms)
	// plus some slack. If the overall HandshakeTimeout fires instead, elapsed
	// will be ~10s, clearly indicating the per-phase timeout isn't working.
	if elapsed > 2*time.Second {
		t.Errorf("device took %v to timeout waiting for PASEConfirm; "+
			"expected per-phase timeout ~500ms, but device used full HandshakeTimeout", elapsed)
	}
}
