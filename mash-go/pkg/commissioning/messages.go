package commissioning

import (
	"errors"
)

// Commissioning message types.
const (
	// MsgPASERequest initiates PASE (SPAKE2+) exchange.
	MsgPASERequest uint8 = 1

	// MsgPASEResponse contains server's public value.
	MsgPASEResponse uint8 = 2

	// MsgPASEConfirm contains client's confirmation.
	MsgPASEConfirm uint8 = 3

	// MsgPASEComplete contains server's confirmation and success status.
	MsgPASEComplete uint8 = 4

	// MsgCSRRequest requests a Certificate Signing Request from the device.
	MsgCSRRequest uint8 = 10

	// MsgCSRResponse contains the device's CSR.
	MsgCSRResponse uint8 = 11

	// MsgCertInstall installs the operational certificate on the device.
	MsgCertInstall uint8 = 12

	// MsgCertInstallResponse confirms certificate installation.
	MsgCertInstallResponse uint8 = 13

	// MsgCommissioningComplete indicates commissioning is finished.
	MsgCommissioningComplete uint8 = 20

	// MsgCommissioningError indicates an error occurred.
	MsgCommissioningError uint8 = 255
)

// Commissioning error codes.
//
// DEC-047 / spec §2.5 mandate a single generic auth-failure code
// (ErrCodeAuthFailed = 1) for every SPAKE2+ failure mode, to prevent
// structural / timing oracles. The pre-DEC-047 codes ErrCodeInvalidPublicKey
// (= 1, never disambiguable from AuthFailed) and ErrCodeConfirmFailed (= 2,
// MAC-mismatch leak) were removed in the Issue #6 fix. The legacy numeric
// value 2 is still recognised on the wire by the runner classifier for
// backward-compat with pre-fix devices, but no in-tree code emits it.
const (
	ErrCodeSuccess           uint8 = 0
	ErrCodeAuthFailed        uint8 = 1   // DEC-047: every SPAKE2+ failure
	ErrCodeCSRFailed         uint8 = 3
	ErrCodeCertInstallFailed uint8 = 4
	ErrCodeBusy              uint8 = 5   // Device busy, retry later (DEC-063)
	ErrCodeZoneTypeExists    uint8 = 10  // Device already has a zone of this type
	ErrCodeInternalError     uint8 = 255
)

// ErrorCodeString returns a human-readable string for a commissioning error code.
func ErrorCodeString(code uint8) string {
	switch code {
	case ErrCodeSuccess:
		return "success"
	case ErrCodeAuthFailed:
		// DEC-047: Generic error message for authentication failures.
		// Intentionally vague to prevent information leakage.
		return "authentication failed"
	case ErrCodeCSRFailed:
		return "CSR generation failed"
	case ErrCodeCertInstallFailed:
		return "certificate installation failed"
	case ErrCodeBusy:
		return "device busy"
	case ErrCodeZoneTypeExists:
		return "zone type already exists"
	case ErrCodeInternalError:
		return "internal error"
	default:
		return "unknown error (" + itoa(code) + ")"
	}
}

// itoa converts a uint8 to a string without importing strconv.
func itoa(n uint8) string {
	if n == 0 {
		return "0"
	}
	var buf [3]byte // max 255
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// Message errors.
var (
	ErrInvalidMessage = errors.New("invalid commissioning message")
)

// PASERequest is the initial SPAKE2+ message from controller to device.
// CBOR: { 1: msgType, 2: publicValue, 3: clientIdentity }
type PASERequest struct {
	MsgType        uint8  `cbor:"1,keyasint"`
	PublicValue    []byte `cbor:"2,keyasint"` // pA
	ClientIdentity []byte `cbor:"3,keyasint"`
}

// PASEResponse is the device's response containing its public value.
// CBOR: { 1: msgType, 2: publicValue }
type PASEResponse struct {
	MsgType     uint8  `cbor:"1,keyasint"`
	PublicValue []byte `cbor:"2,keyasint"` // pB
}

// PASEConfirm contains the client's confirmation MAC.
// CBOR: { 1: msgType, 2: confirmation }
type PASEConfirm struct {
	MsgType      uint8  `cbor:"1,keyasint"`
	Confirmation []byte `cbor:"2,keyasint"`
}

// PASEComplete contains the server's confirmation and status.
// CBOR: { 1: msgType, 2: confirmation, 3: errorCode }
type PASEComplete struct {
	MsgType      uint8  `cbor:"1,keyasint"`
	Confirmation []byte `cbor:"2,keyasint"`
	ErrorCode    uint8  `cbor:"3,keyasint"`
}

// CSRRequest requests a Certificate Signing Request from the device.
// CBOR: { 1: msgType, 2: nonce }
type CSRRequest struct {
	MsgType uint8  `cbor:"1,keyasint"`
	Nonce   []byte `cbor:"2,keyasint"` // Random nonce to prevent replay
}

// CSRResponse contains the device's CSR.
// CBOR: { 1: msgType, 2: csr, 3: errorCode }
type CSRResponse struct {
	MsgType   uint8  `cbor:"1,keyasint"`
	CSR       []byte `cbor:"2,keyasint"` // DER-encoded PKCS#10 CSR
	ErrorCode uint8  `cbor:"3,keyasint"`
}

// CertInstall delivers the operational certificate to the device.
// CBOR: { 1: msgType, 2: operationalCert, 3: caCert, 4: zoneType, 5: zonePriority }
type CertInstall struct {
	MsgType         uint8  `cbor:"1,keyasint"`
	OperationalCert []byte `cbor:"2,keyasint"` // DER-encoded X.509 certificate
	CACert          []byte `cbor:"3,keyasint"` // Zone CA certificate
	ZoneType        uint8  `cbor:"4,keyasint"` // ZoneType enum value
	ZonePriority    uint8  `cbor:"5,keyasint"` // Priority within this zone type
}

// CertInstallResponse confirms certificate installation.
// CBOR: { 1: msgType, 2: errorCode }
type CertInstallResponse struct {
	MsgType   uint8 `cbor:"1,keyasint"`
	ErrorCode uint8 `cbor:"2,keyasint"`
}

// CommissioningComplete indicates successful commissioning.
// CBOR: { 1: msgType }
type CommissioningComplete struct {
	MsgType uint8 `cbor:"1,keyasint"`
}

// CommissioningError indicates a commissioning error.
// CBOR: { 1: msgType, 2: errorCode, 3: message, 4: retryAfter }
type CommissioningError struct {
	MsgType    uint8  `cbor:"1,keyasint"`
	ErrorCode  uint8  `cbor:"2,keyasint"`
	Message    string `cbor:"3,keyasint,omitempty"`
	RetryAfter uint32 `cbor:"4,keyasint"` // Retry hint in milliseconds (DEC-063); 0 = no retry window
}
