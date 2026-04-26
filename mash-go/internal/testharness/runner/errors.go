package runner

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/mash-protocol/mash-go/pkg/commissioning"
)

// ErrorCategory classifies errors for retry decisions.
type ErrorCategory int

const (
	// ErrCatInfrastructure means network/timing issues that may resolve on retry.
	ErrCatInfrastructure ErrorCategory = iota
	// ErrCatDevice means the device rejected the request (don't retry).
	ErrCatDevice
	// ErrCatProtocol means a protocol violation (don't retry).
	ErrCatProtocol
)

func (c ErrorCategory) String() string {
	switch c {
	case ErrCatInfrastructure:
		return "infrastructure"
	case ErrCatDevice:
		return "device"
	case ErrCatProtocol:
		return "protocol"
	default:
		return "unknown"
	}
}

// ClassifiedError wraps an error with a category for retry decisions.
type ClassifiedError struct {
	Category ErrorCategory
	Err      error
}

func (e *ClassifiedError) Error() string { return e.Err.Error() }
func (e *ClassifiedError) Unwrap() error { return e.Err }

// TeardownError identifies a strict teardown failure step and cause.
type TeardownError struct {
	Step  string
	Cause error
}

func (e *TeardownError) Error() string {
	if e == nil {
		return "teardown error"
	}
	if e.Step == "" {
		return fmt.Sprintf("teardown: %v", e.Cause)
	}
	return fmt.Sprintf("teardown[%s]: %v", e.Step, e.Cause)
}

func (e *TeardownError) Unwrap() error { return e.Cause }

// Infrastructure wraps an error as an infrastructure (retryable) error.
func Infrastructure(err error) error {
	return &ClassifiedError{Category: ErrCatInfrastructure, Err: err}
}

// Device wraps an error as a device (non-retryable) error.
func Device(err error) error {
	return &ClassifiedError{Category: ErrCatDevice, Err: err}
}

// Protocol wraps an error as a protocol (non-retryable) error.
func Protocol(err error) error {
	return &ClassifiedError{Category: ErrCatProtocol, Err: err}
}

// Category extracts the error category. Returns ErrCatProtocol for
// unclassified errors (conservative: don't retry unknown errors).
func Category(err error) ErrorCategory {
	var ce *ClassifiedError
	if errors.As(err, &ce) {
		return ce.Category
	}
	return ErrCatProtocol
}

// classifyPASEError wraps a PASE error with the appropriate category based on
// the error code and message content.
func classifyPASEError(err error) error {
	if err == nil {
		return nil
	}

	// Check for network-level errors first.
	if isIOError(err) {
		return Infrastructure(err)
	}

	// Parse PASE error code from the error string.
	code, hasCode := extractPASEErrorCode(err.Error())
	if hasCode {
		switch code {
		case commissioning.ErrCodeBusy: // 5: cooldown or already in progress
			return Infrastructure(err)
		case commissioning.ErrCodeAuthFailed: // 1
			return Device(err)
		case 2: // legacy ErrCodeConfirmFailed — pre-DEC-047 mash-go emitted
			// this on MAC mismatch where spec §2.5 mandates ErrCodeAuthFailed.
			// Kept for backward-compat classification; new devices send 1.
			return Device(err)
		case commissioning.ErrCodeCSRFailed: // 3
			return Device(err)
		case commissioning.ErrCodeCertInstallFailed: // 4
			return Device(err)
		case commissioning.ErrCodeZoneTypeExists: // 10
			return Device(err)
		}
	}

	// Check for known infrastructure patterns in the message.
	msg := err.Error()
	if strings.Contains(msg, "zone slots full") {
		return Device(err)
	}
	if strings.Contains(msg, "cooldown active") ||
		strings.Contains(msg, "commissioning already in progress") {
		return Infrastructure(err)
	}

	// Unclassified PASE error: conservative (don't retry).
	return Protocol(err)
}

// isIOError returns true for IO/network-level errors that indicate
// infrastructure problems.
func isIOError(err error) bool {
	if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "closed pipe") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "deadline exceeded")
}
