package inspect

import (
	"errors"
	"fmt"

	"github.com/mash-protocol/mash-go/pkg/model"
)

// Inspector errors.
var (
	ErrEndpointNotFound  = errors.New("endpoint not found")
	ErrFeatureNotFound   = errors.New("feature not found")
	ErrAttributeNotFound = errors.New("attribute not found")
	ErrCommandNotFound   = errors.New("command not found")
	ErrNotWritable       = errors.New("attribute is not writable")
)

// Inspector provides inspection and mutation capabilities for a local device.
type Inspector struct {
	device *model.Device
}

// NewInspector creates a new Inspector for the given device.
func NewInspector(device *model.Device) *Inspector {
	return &Inspector{device: device}
}

// Device returns the underlying device model.
func (i *Inspector) Device() *model.Device {
	return i.device
}

// DeviceTree represents the complete device structure for display.
type DeviceTree struct {
	DeviceID  string
	VendorID  uint32
	ProductID uint16
	Endpoints []EndpointInfo
}

// EndpointInfo represents endpoint information for display.
type EndpointInfo struct {
	ID       uint8
	Type     model.EndpointType
	Label    string
	Features []FeatureInfo
}

// FeatureInfo represents feature information for display.
type FeatureInfo struct {
	ID         uint8
	Type       model.FeatureType
	FeatureMap uint32
	Revision   uint16
	Attributes []AttributeInfo
	Commands   []CommandInfo
}

// AttributeInfo represents attribute information for display.
type AttributeInfo struct {
	ID       uint16
	Name     string
	Value    any
	Type     model.DataType
	Access   model.Access
	Unit     string
	Nullable bool
}

// CommandInfo represents command information for display.
type CommandInfo struct {
	ID          uint8
	Name        string
	Description string
}

// InspectDevice returns a complete tree of the device structure.
func (i *Inspector) InspectDevice() *DeviceTree {
	tree := &DeviceTree{
		DeviceID:  i.device.DeviceID(),
		VendorID:  i.device.VendorID(),
		ProductID: i.device.ProductID(),
	}

	for _, ep := range i.device.Endpoints() {
		epInfo := i.inspectEndpointInternal(ep)
		tree.Endpoints = append(tree.Endpoints, epInfo)
	}

	return tree
}

// InspectEndpoint returns information about a specific endpoint.
func (i *Inspector) InspectEndpoint(epID uint8) (*EndpointInfo, error) {
	ep, err := i.device.GetEndpoint(epID)
	if err != nil {
		return nil, ErrEndpointNotFound
	}

	info := i.inspectEndpointInternal(ep)
	return &info, nil
}

// inspectEndpointInternal extracts endpoint info without error handling.
func (i *Inspector) inspectEndpointInternal(ep *model.Endpoint) EndpointInfo {
	info := EndpointInfo{
		ID:    ep.ID(),
		Type:  ep.Type(),
		Label: ep.Label(),
	}

	for _, feat := range ep.Features() {
		featInfo := i.inspectFeatureInternal(feat)
		info.Features = append(info.Features, featInfo)
	}

	return info
}

// InspectFeature returns information about a specific feature.
func (i *Inspector) InspectFeature(epID uint8, featID uint8) (*FeatureInfo, error) {
	ep, err := i.device.GetEndpoint(epID)
	if err != nil {
		return nil, ErrEndpointNotFound
	}

	feat, err := ep.GetFeatureByID(featID)
	if err != nil {
		return nil, ErrFeatureNotFound
	}

	info := i.inspectFeatureInternal(feat)
	return &info, nil
}

// inspectFeatureInternal extracts feature info without error handling.
func (i *Inspector) inspectFeatureInternal(feat *model.Feature) FeatureInfo {
	info := FeatureInfo{
		ID:         uint8(feat.Type()),
		Type:       feat.Type(),
		FeatureMap: feat.FeatureMap(),
		Revision:   feat.Revision(),
	}

	// Get all attributes
	for _, attrID := range feat.AttributeList() {
		attr, err := feat.GetAttribute(attrID)
		if err != nil {
			continue
		}
		meta := attr.Metadata()
		attrInfo := AttributeInfo{
			ID:       meta.ID,
			Name:     meta.Name,
			Value:    attr.Value(),
			Type:     meta.Type,
			Access:   meta.Access,
			Unit:     meta.Unit,
			Nullable: meta.Nullable,
		}
		info.Attributes = append(info.Attributes, attrInfo)
	}

	// Get all commands
	for _, cmdID := range feat.CommandList() {
		cmd, err := feat.GetCommand(cmdID)
		if err != nil {
			continue
		}
		meta := cmd.Metadata()
		cmdInfo := CommandInfo{
			ID:          meta.ID,
			Name:        meta.Name,
			Description: meta.Description,
		}
		info.Commands = append(info.Commands, cmdInfo)
	}

	return info
}

// ReadAttribute reads an attribute value using a path.
func (i *Inspector) ReadAttribute(path *Path) (any, *model.AttributeMetadata, error) {
	ep, err := i.device.GetEndpoint(path.EndpointID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: endpoint %d", ErrEndpointNotFound, path.EndpointID)
	}

	feat, err := ep.GetFeatureByID(path.FeatureID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: feature %d", ErrFeatureNotFound, path.FeatureID)
	}

	attr, err := feat.GetAttribute(path.AttributeID)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: attribute %d", ErrAttributeNotFound, path.AttributeID)
	}

	meta := attr.Metadata()
	return attr.Value(), meta, nil
}

// ReadAllAttributes reads all attribute values for a feature.
func (i *Inspector) ReadAllAttributes(epID uint8, featID uint8) (map[uint16]any, error) {
	ep, err := i.device.GetEndpoint(epID)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint %d", ErrEndpointNotFound, epID)
	}

	feat, err := ep.GetFeatureByID(featID)
	if err != nil {
		return nil, fmt.Errorf("%w: feature %d", ErrFeatureNotFound, featID)
	}

	// Use the feature's ReadAllAttributes method
	return feat.ReadAllAttributes(), nil
}

// WriteAttribute writes an attribute value using a path.
func (i *Inspector) WriteAttribute(path *Path, value any) error {
	ep, err := i.device.GetEndpoint(path.EndpointID)
	if err != nil {
		return fmt.Errorf("%w: endpoint %d", ErrEndpointNotFound, path.EndpointID)
	}

	feat, err := ep.GetFeatureByID(path.FeatureID)
	if err != nil {
		return fmt.Errorf("%w: feature %d", ErrFeatureNotFound, path.FeatureID)
	}

	// Use the feature's WriteAttribute which enforces access control
	if err := feat.WriteAttribute(path.AttributeID, value); err != nil {
		return err
	}

	return nil
}

// InvokeCommand invokes a command using a path.
//
// params is integer-keyed per docs/interaction-model.md §7.1 (Issue #11 fix).
func (i *Inspector) InvokeCommand(path *Path, params map[uint8]any) (any, error) {
	ep, err := i.device.GetEndpoint(path.EndpointID)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint %d", ErrEndpointNotFound, path.EndpointID)
	}

	feat, err := ep.GetFeatureByID(path.FeatureID)
	if err != nil {
		return nil, fmt.Errorf("%w: feature %d", ErrFeatureNotFound, path.FeatureID)
	}

	// TODO: Look up command handler and invoke
	// For now, return error as commands aren't implemented yet
	_ = feat
	return nil, fmt.Errorf("%w: command %d (not implemented)", ErrCommandNotFound, path.CommandID)
}

// FormatDeviceTree formats the device tree for display.
func (i *Inspector) FormatDeviceTree(tree *DeviceTree, formatter *Formatter) string {
	if formatter == nil {
		formatter = NewFormatter()
	}

	var result string

	// Header
	result += fmt.Sprintf("Device: %s\n", tree.DeviceID)
	result += fmt.Sprintf("Vendor: 0x%04X  Product: 0x%04X\n", tree.VendorID, tree.ProductID)
	result += "---\n"

	// Endpoints
	for _, ep := range tree.Endpoints {
		result += i.formatEndpoint(&ep, formatter, 0)
	}

	return result
}

// FormatEndpoint formats an endpoint for display.
func (i *Inspector) FormatEndpoint(ep *EndpointInfo, formatter *Formatter) string {
	if formatter == nil {
		formatter = NewFormatter()
	}
	return i.formatEndpoint(ep, formatter, 0)
}

func (i *Inspector) formatEndpoint(ep *EndpointInfo, f *Formatter, depth int) string {
	var result string

	// Endpoint header
	header := fmt.Sprintf("Endpoint %d: %s", ep.ID, FormatEndpointType(uint8(ep.Type)))
	if ep.Label != "" {
		header += fmt.Sprintf(" (%s)", ep.Label)
	}
	result += f.Indent(depth, header) + "\n"

	// Features
	for _, feat := range ep.Features {
		result += i.formatFeature(&feat, f, depth+1)
	}

	return result
}

// FormatFeature formats a feature for display.
func (i *Inspector) FormatFeature(feat *FeatureInfo, formatter *Formatter) string {
	if formatter == nil {
		formatter = NewFormatter()
	}
	return i.formatFeature(feat, formatter, 0)
}

func (i *Inspector) formatFeature(feat *FeatureInfo, f *Formatter, depth int) string {
	var result string

	// Feature header
	header := fmt.Sprintf("%s (ID: %d, Rev: %d)", FormatFeatureType(feat.ID), feat.ID, feat.Revision)
	result += f.Indent(depth, header) + "\n"

	// DEC-074: render featureMap bits as raw hex. Bits encode capability
	// variants within a feature (PICS conformance points F00-F1F), not
	// feature presence; symbolic rendering conflated those semantics.
	if feat.FeatureMap != 0 {
		result += f.Indent(depth+1, fmt.Sprintf("Capabilities: 0x%04X", feat.FeatureMap)) + "\n"
	}

	// Attributes
	for _, attr := range feat.Attributes {
		attrStr := i.formatAttributeInfo(&attr, feat.Type, f)
		result += f.Indent(depth+1, attrStr) + "\n"
	}

	// Commands
	for _, cmd := range feat.Commands {
		cmdStr := fmt.Sprintf("[cmd %d] %s", cmd.ID, cmd.Name)
		result += f.Indent(depth+1, cmdStr) + "\n"
	}

	return result
}

func (i *Inspector) formatAttributeInfo(attr *AttributeInfo, featType model.FeatureType, f *Formatter) string {
	// Get attribute name
	name := attr.Name
	if name == "" {
		name = GetAttributeName(uint8(featType), attr.ID)
		if name == "" {
			name = fmt.Sprintf("attr_%d", attr.ID)
		}
	}

	// Format value
	valueStr := f.FormatValue(attr.Value, attr.Unit)

	if f.ShowIDs {
		return fmt.Sprintf("[%d] %s = %s", attr.ID, name, valueStr)
	}
	return fmt.Sprintf("%s = %s", name, valueStr)
}
