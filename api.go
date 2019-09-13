package gomesh

import (
	"github.com/dynamicgo/go-config"
	"github.com/dynamicgo/go-config/source"
	"github.com/dynamicgo/xerrors/apierr"
)

// ScopeOfAPIError .
const errorScope = "gomesh"

// errors
var (
	ErrInternal = apierr.WithScope(-1, "the internal error", errorScope)
	ErrAgent    = apierr.WithScope(-2, "agent implement not found", errorScope)
	ErrExists   = apierr.WithScope(-3, "target resource exists", errorScope)
	ErrNotFound = apierr.WithScope(-3, "target resource not found", errorScope)
)

// Service gomesh service base interface has nothing
type Service interface {
	Start(config config.Config) error
}

// ServiceRegisterEntry .
type ServiceRegisterEntry struct {
	Name    string  // service name
	Service Service // service impl
}

// Mesh golang service mesh object, handle the service inject and extension module
type Mesh interface {
	Module(module Module) ModuleBuilder
	Services(serviceSlice interface{})
	ServiceByName(name string, service interface{})
	Start(loaders ...ConfigLoader) error
}

// ModuleF module create factory
type ModuleF func(builder ModuleBuilder) (Module, error)

// ModuleBuilder .
type ModuleBuilder interface {
	RegisterService(name string)
}

// Module . service register module
type Module interface {
	Service
	Name() string
	BeginCreateService() error
	CreateService(name string) (Service, error)
	EndCreateService() error
	BeginSetupService() error
	SetupService(service Service) error
	EndSetupService() error
	BeginStartService() error
	StartService(service Service, config config.Config) error
	EndStarService() error
}

// ConfigLoader config loader
type ConfigLoader interface {
	Load() []source.Source
}
