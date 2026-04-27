package model

import (
	"context"
	"errors"
	"slices"
	"sync"
)

// Feature errors.
var (
	ErrAttributeNotFound = errors.New("attribute not found")
	ErrFeatureReadOnly   = errors.New("feature is read-only")
)

// ReadHook is called during context-aware reads to allow features to override
// attribute values based on caller context (e.g., returning per-zone values).
// It returns (value, true) to override the attribute, or (nil, false) to fall
// through to the stored value.
type ReadHook func(ctx context.Context, attrID uint16) (any, bool)

// Feature represents a feature instance containing attributes and commands.
type Feature struct {
	mu sync.RWMutex

	// Type is the feature type identifier.
	featureType FeatureType

	// Revision is the feature implementation revision.
	revision uint16

	// FeatureMap is the capability bitmap for this feature.
	featureMap uint32

	// Attributes indexed by ID.
	attributes map[uint16]*Attribute

	// Commands indexed by ID.
	commands map[uint8]*Command

	// Subscribers for change notifications.
	subscribers []FeatureSubscriber

	// readHook is an optional hook for context-aware attribute reads.
	readHook ReadHook
}

// FeatureSubscriber is notified when attributes change.
type FeatureSubscriber interface {
	// OnAttributeChanged is called when an attribute value changes.
	OnAttributeChanged(featureType FeatureType, attrID uint16, value any)
}

// NewFeature creates a new feature of the given type.
func NewFeature(featureType FeatureType, revision uint16) *Feature {
	f := &Feature{
		featureType: featureType,
		revision:    revision,
		attributes:  make(map[uint16]*Attribute),
		commands:    make(map[uint8]*Command),
	}

	// Add global attributes
	f.addGlobalAttributes()

	return f
}

// addGlobalAttributes adds the standard global attributes.
func (f *Feature) addGlobalAttributes() {
	// featureMap
	f.attributes[AttrIDFeatureMap] = NewAttribute(&AttributeMetadata{
		ID:          AttrIDFeatureMap,
		Name:        "featureMap",
		Type:        DataTypeUint32,
		Access:      AccessReadOnly,
		Description: "Feature capability bitmap",
		Default:     uint32(0),
	})

	// attributeList (dynamically computed)
	f.attributes[AttrIDAttributeList] = NewAttribute(&AttributeMetadata{
		ID:          AttrIDAttributeList,
		Name:        "attributeList",
		Type:        DataTypeArray,
		Access:      AccessRead,
		Description: "List of supported attribute IDs",
	})

	// commandList (dynamically computed)
	f.attributes[AttrIDCommandList] = NewAttribute(&AttributeMetadata{
		ID:          AttrIDCommandList,
		Name:        "commandList",
		Type:        DataTypeArray,
		Access:      AccessRead,
		Description: "List of supported command IDs",
	})
}

// Type returns the feature type.
func (f *Feature) Type() FeatureType {
	return f.featureType
}

// Revision returns the feature revision.
func (f *Feature) Revision() uint16 {
	return f.revision
}

// FeatureMap returns the feature capability bitmap.
func (f *Feature) FeatureMap() uint32 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.featureMap
}

// SetFeatureMap sets the feature capability bitmap.
func (f *Feature) SetFeatureMap(bitmap uint32) {
	f.mu.Lock()
	f.featureMap = bitmap
	f.mu.Unlock()

	// Update the featureMap attribute
	if attr, exists := f.attributes[AttrIDFeatureMap]; exists {
		attr.SetValueInternal(bitmap)
	}
}

// AddAttribute adds an attribute to the feature.
func (f *Feature) AddAttribute(attr *Attribute) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attributes[attr.ID()] = attr
}

// GetAttribute returns an attribute by ID.
func (f *Feature) GetAttribute(id uint16) (*Attribute, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	attr, exists := f.attributes[id]
	if !exists {
		return nil, ErrAttributeNotFound
	}
	return attr, nil
}

// ReadAttribute reads an attribute value by ID.
func (f *Feature) ReadAttribute(id uint16) (any, error) {
	// Handle dynamic attributes
	if id == AttrIDAttributeList {
		return f.AttributeList(), nil
	}
	if id == AttrIDCommandList {
		return f.CommandList(), nil
	}

	attr, err := f.GetAttribute(id)
	if err != nil {
		return nil, err
	}

	if !attr.Metadata().Access.CanRead() {
		return nil, ErrAttributeNotFound // Treat non-readable as not found
	}

	return attr.Value(), nil
}

// WriteAttribute writes an attribute value by ID.
func (f *Feature) WriteAttribute(id uint16, value any) error {
	// Global attributes are not writable
	if id >= AttrIDGlobalBase {
		return ErrFeatureReadOnly
	}

	attr, err := f.GetAttribute(id)
	if err != nil {
		return err
	}

	if err := attr.SetValue(value); err != nil {
		return err
	}

	// Notify subscribers
	f.notifyAttributeChanged(id, value)

	return nil
}

// SetAttributeInternal sets an attribute value without checking write access.
// Used by device implementations to update read-only attributes (e.g., measurements).
func (f *Feature) SetAttributeInternal(id uint16, value any) error {
	// Global attributes are still not writable
	if id >= AttrIDGlobalBase {
		return ErrFeatureReadOnly
	}

	attr, err := f.GetAttribute(id)
	if err != nil {
		return err
	}

	if err := attr.SetValueInternal(value); err != nil {
		return err
	}

	// Notify subscribers
	f.notifyAttributeChanged(id, value)

	return nil
}

// ReadAllAttributes returns all readable attribute values.
func (f *Feature) ReadAllAttributes() map[uint16]any {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make(map[uint16]any)
	for id, attr := range f.attributes {
		if attr.Metadata().Access.CanRead() {
			// Handle dynamic attributes
			if id == AttrIDAttributeList {
				result[id] = f.attributeListUnlocked()
			} else if id == AttrIDCommandList {
				result[id] = f.commandListUnlocked()
			} else {
				result[id] = attr.Value()
			}
		}
	}
	return result
}

// SetReadHook sets an optional hook for context-aware attribute reads.
func (f *Feature) SetReadHook(hook ReadHook) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readHook = hook
}

// ReadAttributeWithContext reads an attribute value by ID, passing context to
// the read hook if one is set. The hook may override the returned value based
// on the caller's context (e.g., zone identity).
func (f *Feature) ReadAttributeWithContext(ctx context.Context, id uint16) (any, error) {
	// Handle dynamic attributes (not overridable by hooks)
	if id == AttrIDAttributeList {
		return f.AttributeList(), nil
	}
	if id == AttrIDCommandList {
		return f.CommandList(), nil
	}

	attr, err := f.GetAttribute(id)
	if err != nil {
		return nil, err
	}

	if !attr.Metadata().Access.CanRead() {
		return nil, ErrAttributeNotFound
	}

	// Try hook first
	f.mu.RLock()
	hook := f.readHook
	f.mu.RUnlock()

	if hook != nil {
		if val, ok := hook(ctx, id); ok {
			return val, nil
		}
	}

	return attr.Value(), nil
}

// ReadAllAttributesWithContext returns all readable attribute values, passing
// context to the read hook for each attribute.
func (f *Feature) ReadAllAttributesWithContext(ctx context.Context) map[uint16]any {
	f.mu.RLock()
	hook := f.readHook
	attrs := make(map[uint16]*Attribute, len(f.attributes))
	for id, attr := range f.attributes {
		attrs[id] = attr
	}
	f.mu.RUnlock()

	result := make(map[uint16]any)
	for id, attr := range attrs {
		if !attr.Metadata().Access.CanRead() {
			continue
		}

		// Handle dynamic attributes
		if id == AttrIDAttributeList {
			result[id] = f.AttributeList()
			continue
		}
		if id == AttrIDCommandList {
			result[id] = f.CommandList()
			continue
		}

		// Try hook first
		if hook != nil {
			if val, ok := hook(ctx, id); ok {
				result[id] = val
				continue
			}
		}

		result[id] = attr.Value()
	}
	return result
}

// AttributeList returns the list of supported attribute IDs.
func (f *Feature) AttributeList() []uint16 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.attributeListUnlocked()
}

func (f *Feature) attributeListUnlocked() []uint16 {
	ids := make([]uint16, 0, len(f.attributes))
	for id, attr := range f.attributes {
		if attr.Metadata().Access.CanRead() {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// AddCommand adds a command to the feature.
func (f *Feature) AddCommand(cmd *Command) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[cmd.ID()] = cmd
}

// GetCommand returns a command by ID.
func (f *Feature) GetCommand(id uint8) (*Command, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	cmd, exists := f.commands[id]
	if !exists {
		return nil, ErrCommandNotFound
	}
	return cmd, nil
}

// InvokeCommand invokes a command by ID.
//
// params and the response map are integer-keyed per
// docs/interaction-model.md §7.1 / §7.2 (Issue #11 fix).
func (f *Feature) InvokeCommand(ctx context.Context, id uint8, params map[uint8]any) (map[uint8]any, error) {
	cmd, err := f.GetCommand(id)
	if err != nil {
		return nil, err
	}
	return cmd.Invoke(ctx, params)
}

// CommandList returns the list of supported command IDs.
func (f *Feature) CommandList() []uint8 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.commandListUnlocked()
}

func (f *Feature) commandListUnlocked() []uint8 {
	ids := make([]uint8, 0, len(f.commands))
	for id := range f.commands {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Subscribe adds a subscriber for change notifications.
func (f *Feature) Subscribe(sub FeatureSubscriber) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribers = append(f.subscribers, sub)
}

// Unsubscribe removes a subscriber.
func (f *Feature) Unsubscribe(sub FeatureSubscriber) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for i, s := range f.subscribers {
		if s == sub {
			f.subscribers = append(f.subscribers[:i], f.subscribers[i+1:]...)
			return
		}
	}
}

// notifyAttributeChanged notifies all subscribers of an attribute change.
func (f *Feature) notifyAttributeChanged(attrID uint16, value any) {
	f.mu.RLock()
	subs := make([]FeatureSubscriber, len(f.subscribers))
	copy(subs, f.subscribers)
	f.mu.RUnlock()

	for _, sub := range subs {
		sub.OnAttributeChanged(f.featureType, attrID, value)
	}
}

// GetDirtyAttributes returns attributes that have changed since the last report.
func (f *Feature) GetDirtyAttributes() map[uint16]any {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make(map[uint16]any)
	for id, attr := range f.attributes {
		if attr.IsDirty() {
			result[id] = attr.Value()
		}
	}
	return result
}

// ClearDirtyAttributes clears the dirty flag on all attributes.
func (f *Feature) ClearDirtyAttributes() {
	f.mu.RLock()
	defer f.mu.RUnlock()

	for _, attr := range f.attributes {
		attr.ClearDirty()
	}
}
