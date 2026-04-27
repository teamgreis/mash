package features

import (
	"github.com/mash-protocol/mash-go/pkg/model"
)

// SetEndpoints sets the endpoint structure.
func (d *DeviceInfo) SetEndpoints(endpoints []*model.EndpointInfo) error {
	attr, err := d.GetAttribute(DeviceInfoAttrEndpoints)
	if err != nil {
		return err
	}
	return attr.SetValueInternal(endpoints)
}

// SetUseCases sets the use case declarations.
func (d *DeviceInfo) SetUseCases(useCases []*model.UseCaseDecl) error {
	attr, err := d.GetAttribute(DeviceInfoAttrUseCases)
	if err != nil {
		return err
	}
	return attr.SetValueInternal(useCases)
}

// UseCases returns the use case declarations.
func (d *DeviceInfo) UseCases() []*model.UseCaseDecl {
	val, _ := d.ReadAttribute(DeviceInfoAttrUseCases)
	if ucs, ok := val.([]*model.UseCaseDecl); ok {
		return ucs
	}
	return nil
}

// WriteLocation sets the installation location via external write (ACL-enforced).
func (d *DeviceInfo) WriteLocation(location string) error {
	return d.WriteAttribute(DeviceInfoAttrLocation, location)
}

// WriteLabel sets the user-assigned name via external write (ACL-enforced).
func (d *DeviceInfo) WriteLabel(label string) error {
	return d.WriteAttribute(DeviceInfoAttrLabel, label)
}

