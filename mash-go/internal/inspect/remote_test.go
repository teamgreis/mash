package inspect

import (
	"context"
	"errors"
	"testing"

	"github.com/mash-protocol/mash-go/pkg/model"
)

// mockSession implements the SessionReader interface for testing.
type mockSession struct {
	readFunc    func(ctx context.Context, epID uint8, featID uint8, attrIDs []uint16) (map[uint16]any, error)
	writeFunc   func(ctx context.Context, epID uint8, featID uint8, attrs map[uint16]any) (map[uint16]any, error)
	invokeFunc  func(ctx context.Context, epID uint8, featID uint8, cmdID uint8, params map[uint8]any) (any, error)
	deviceIDVal string
}

func (m *mockSession) DeviceID() string {
	return m.deviceIDVal
}

func (m *mockSession) Read(ctx context.Context, epID uint8, featID uint8, attrIDs []uint16) (map[uint16]any, error) {
	if m.readFunc != nil {
		return m.readFunc(ctx, epID, featID, attrIDs)
	}
	return nil, nil
}

func (m *mockSession) Write(ctx context.Context, epID uint8, featID uint8, attrs map[uint16]any) (map[uint16]any, error) {
	if m.writeFunc != nil {
		return m.writeFunc(ctx, epID, featID, attrs)
	}
	return nil, nil
}

func (m *mockSession) Invoke(ctx context.Context, epID uint8, featID uint8, cmdID uint8, params map[uint8]any) (any, error) {
	if m.invokeFunc != nil {
		return m.invokeFunc(ctx, epID, featID, cmdID, params)
	}
	return nil, nil
}

func TestRemoteInspectorReadAttribute(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		setupMock func(*mockSession)
		wantValue any
		wantErr   bool
	}{
		{
			name: "read single attribute by ID",
			path: "1/2/3",
			setupMock: func(m *mockSession) {
				m.readFunc = func(_ context.Context, epID uint8, featID uint8, attrIDs []uint16) (map[uint16]any, error) {
					if epID != 1 || featID != 2 {
						t.Errorf("wrong endpoint/feature: got %d/%d, want 1/2", epID, featID)
					}
					if len(attrIDs) != 1 || attrIDs[0] != 3 {
						t.Errorf("wrong attrIDs: got %v, want [3]", attrIDs)
					}
					return map[uint16]any{3: int64(5000000)}, nil
				}
			},
			wantValue: int64(5000000),
			wantErr:   false,
		},
		{
			name: "read attribute by name",
			path: "1/measurement/acActivePower",
			setupMock: func(m *mockSession) {
				m.readFunc = func(_ context.Context, epID uint8, featID uint8, attrIDs []uint16) (map[uint16]any, error) {
					// measurement = FeatureMeasurement, acActivePower = 1
					if epID != 1 || featID != uint8(model.FeatureMeasurement) {
						t.Errorf("wrong endpoint/feature: got %d/%d, want 1/%d", epID, featID, model.FeatureMeasurement)
					}
					if len(attrIDs) != 1 || attrIDs[0] != 1 {
						t.Errorf("wrong attrIDs: got %v, want [1]", attrIDs)
					}
					return map[uint16]any{1: int64(-3500000)}, nil
				}
			},
			wantValue: int64(-3500000),
			wantErr:   false,
		},
		{
			name: "read returns error",
			path: "1/2/3",
			setupMock: func(m *mockSession) {
				m.readFunc = func(_ context.Context, _, _ uint8, _ []uint16) (map[uint16]any, error) {
					return nil, errors.New("connection closed")
				}
			},
			wantValue: nil,
			wantErr:   true,
		},
		{
			name: "attribute not in response",
			path: "1/2/99",
			setupMock: func(m *mockSession) {
				m.readFunc = func(_ context.Context, _, _ uint8, _ []uint16) (map[uint16]any, error) {
					return map[uint16]any{1: int64(100)}, nil // returns different attr
				}
			},
			wantValue: nil,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockSession{deviceIDVal: "test-device"}
			if tt.setupMock != nil {
				tt.setupMock(mock)
			}

			ri := NewRemoteInspector(mock)
			path, err := ParsePath(tt.path)
			if err != nil {
				t.Fatalf("ParsePath failed: %v", err)
			}

			value, err := ri.ReadAttribute(context.Background(), path)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadAttribute failed: %v", err)
			}

			if value != tt.wantValue {
				t.Errorf("value = %v, want %v", value, tt.wantValue)
			}
		})
	}
}

func TestRemoteInspectorReadAllAttributes(t *testing.T) {
	mock := &mockSession{
		deviceIDVal: "test-device",
		readFunc: func(_ context.Context, epID uint8, featID uint8, attrIDs []uint16) (map[uint16]any, error) {
			if epID != 1 || featID != 2 {
				t.Errorf("wrong endpoint/feature: got %d/%d, want 1/2", epID, featID)
			}
			if attrIDs != nil {
				t.Errorf("expected nil attrIDs for read-all, got %v", attrIDs)
			}
			return map[uint16]any{
				1:  int64(5000000),
				2:  int64(230000),
				10: int64(21700),
			}, nil
		},
	}

	ri := NewRemoteInspector(mock)
	attrs, err := ri.ReadAllAttributes(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("ReadAllAttributes failed: %v", err)
	}

	if len(attrs) != 3 {
		t.Errorf("len(attrs) = %d, want 3", len(attrs))
	}
	if attrs[1] != int64(5000000) {
		t.Errorf("attrs[1] = %v, want 5000000", attrs[1])
	}
}

func TestRemoteInspectorWriteAttribute(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		value     any
		setupMock func(*mockSession)
		wantErr   bool
	}{
		{
			name:  "write attribute by ID",
			path:  "1/3/20",
			value: int64(5000000),
			setupMock: func(m *mockSession) {
				m.writeFunc = func(_ context.Context, epID uint8, featID uint8, attrs map[uint16]any) (map[uint16]any, error) {
					if epID != 1 || featID != 3 {
						t.Errorf("wrong endpoint/feature: got %d/%d, want 1/3", epID, featID)
					}
					if v, ok := attrs[20]; !ok || v != int64(5000000) {
						t.Errorf("wrong attrs: got %v", attrs)
					}
					return map[uint16]any{20: int64(5000000)}, nil
				}
			},
			wantErr: false,
		},
		{
			name:  "write attribute by name",
			path:  "1/energyControl/effectiveConsumptionLimit",
			value: int64(11000000),
			setupMock: func(m *mockSession) {
				m.writeFunc = func(_ context.Context, epID uint8, featID uint8, attrs map[uint16]any) (map[uint16]any, error) {
					// energyControl = FeatureEnergyControl, effectiveConsumptionLimit = 20
					if epID != 1 || featID != uint8(model.FeatureEnergyControl) {
						t.Errorf("wrong endpoint/feature: got %d/%d, want 1/%d", epID, featID, model.FeatureEnergyControl)
					}
					if v, ok := attrs[20]; !ok || v != int64(11000000) {
						t.Errorf("wrong attrs: got %v", attrs)
					}
					return map[uint16]any{20: int64(11000000)}, nil
				}
			},
			wantErr: false,
		},
		{
			name:  "write returns error",
			path:  "1/3/20",
			value: int64(5000000),
			setupMock: func(m *mockSession) {
				m.writeFunc = func(_ context.Context, _, _ uint8, _ map[uint16]any) (map[uint16]any, error) {
					return nil, errors.New("read-only attribute")
				}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockSession{deviceIDVal: "test-device"}
			if tt.setupMock != nil {
				tt.setupMock(mock)
			}

			ri := NewRemoteInspector(mock)
			path, err := ParsePath(tt.path)
			if err != nil {
				t.Fatalf("ParsePath failed: %v", err)
			}

			err = ri.WriteAttribute(context.Background(), path, tt.value)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("WriteAttribute failed: %v", err)
			}
		})
	}
}

func TestRemoteInspectorInvokeCommand(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		params     map[uint8]any
		setupMock  func(*mockSession)
		wantResult any
		wantErr    bool
	}{
		{
			name:   "invoke command by ID",
			path:   "1/3/cmd/1",
			params: map[uint8]any{1: int64(5000000)},
			setupMock: func(m *mockSession) {
				m.invokeFunc = func(_ context.Context, epID uint8, featID uint8, cmdID uint8, params map[uint8]any) (any, error) {
					if epID != 1 || featID != 3 || cmdID != 1 {
						t.Errorf("wrong endpoint/feature/command: got %d/%d/%d", epID, featID, cmdID)
					}
					if params[1] != int64(5000000) {
						t.Errorf("wrong params: got %v", params)
					}
					return map[uint8]any{1: "accepted"}, nil
				}
			},
			wantResult: map[uint8]any{1: "accepted"},
			wantErr:    false,
		},
		{
			name:   "invoke returns error",
			path:   "1/3/cmd/1",
			params: nil,
			setupMock: func(m *mockSession) {
				m.invokeFunc = func(_ context.Context, _, _ uint8, _ uint8, _ map[uint8]any) (any, error) {
					return nil, errors.New("command not supported")
				}
			},
			wantResult: nil,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockSession{deviceIDVal: "test-device"}
			if tt.setupMock != nil {
				tt.setupMock(mock)
			}

			ri := NewRemoteInspector(mock)
			path, err := ParsePath(tt.path)
			if err != nil {
				t.Fatalf("ParsePath failed: %v", err)
			}

			result, err := ri.InvokeCommand(context.Background(), path, tt.params)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("InvokeCommand failed: %v", err)
			}

			// Compare result (for map comparison)
			if tt.wantResult != nil {
				wantMap, wantOK := tt.wantResult.(map[uint8]any)
				gotMap, gotOK := result.(map[uint8]any)
				if wantOK && gotOK {
					if wantMap[1] != gotMap[1] {
						t.Errorf("result = %v, want %v", result, tt.wantResult)
					}
				}
			}
		})
	}
}

func TestRemoteInspectorDeviceID(t *testing.T) {
	mock := &mockSession{deviceIDVal: "my-evse-123"}
	ri := NewRemoteInspector(mock)

	if ri.DeviceID() != "my-evse-123" {
		t.Errorf("DeviceID() = %q, want %q", ri.DeviceID(), "my-evse-123")
	}
}
