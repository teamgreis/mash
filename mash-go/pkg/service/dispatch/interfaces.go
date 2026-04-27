package dispatch

import (
	"context"
	"time"

	"github.com/mash-protocol/mash-go/pkg/model"
	"github.com/mash-protocol/mash-go/pkg/subscription"
)

// SubscriptionTracker defines the subscription management operations used by the
// service layer. It is satisfied by *subscription.Manager.
type SubscriptionTracker interface {
	Subscribe(endpointID, featureID uint16, attributeIDs []uint16,
		minInterval, maxInterval time.Duration, currentValues map[uint16]any) (uint32, error)
	Unsubscribe(subscriptionID uint32) error
	NotifyChange(endpointID, featureID, attrID uint16, value any)
	NotifyChanges(endpointID, featureID uint16, changes map[uint16]any)
	ProcessNotifications()
	ClearAll()
	Count() int
	OnNotification(fn func(subscription.Notification))
}

// Compile-time check: *subscription.Manager implements SubscriptionTracker.
var _ SubscriptionTracker = (*subscription.Manager)(nil)

// DeviceModel defines the device model operations used by ProtocolHandler,
// ZoneSession, and related service-layer components. It is satisfied by
// *model.Device.
//
// DeviceService.device remains *model.Device (the public Device() accessor
// returns the concrete type for external callers like pkg/inspect). This
// interface is consumed at the ProtocolHandler/ZoneSession level where mock
// testability matters most.
type DeviceModel interface {
	DeviceID() string
	VendorID() uint32
	ProductID() uint16
	SerialNumber() string
	RootEndpoint() *model.Endpoint
	AddEndpoint(endpoint *model.Endpoint) error
	GetEndpoint(id uint8) (*model.Endpoint, error)
	Endpoints() []*model.Endpoint
	EndpointCount() int
	GetFeature(endpointID uint8, featureType model.FeatureType) (*model.Feature, error)
	ReadAttribute(endpointID uint8, featureType model.FeatureType, attrID uint16) (any, error)
	WriteAttribute(endpointID uint8, featureType model.FeatureType, attrID uint16, value any) error
	InvokeCommand(ctx context.Context, endpointID uint8, featureType model.FeatureType, cmdID uint8, params map[uint8]any) (map[uint8]any, error)
	Info() *model.DeviceInfo
	FindEndpointsByType(endpointType model.EndpointType) []*model.Endpoint
	FindEndpointsWithFeature(featureType model.FeatureType) []*model.Endpoint
}

// Compile-time check: *model.Device implements DeviceModel.
var _ DeviceModel = (*model.Device)(nil)
