package gomesh

import (
	"sync"

	"github.com/dynamicgo/go-config"
	extend "github.com/dynamicgo/go-config-extend"
	"github.com/dynamicgo/injector"
	"github.com/dynamicgo/slf4go"
	"github.com/dynamicgo/xerrors"
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
type Service interface{}

// Runnable gomesh service base interface has nothing
type Runnable interface {
	Service
	Start() error
}

// ServiceRegisterEntry .
type ServiceRegisterEntry struct {
	Name    string  // service name
	Service Service // service impl
}

// MeshBuilder .
type MeshBuilder interface {
	RegisterService(extensionName string, serviceName string) error
	RegisterExtension(extension Extension) error
	Start(config config.Config) error
	FindService(name string, service interface{})
}

// Extension gomesh service handle extension
type Extension interface {
	Name() string // extension name
	Begin(config config.Config, builder MeshBuilder) error
	CreateSerivce(serviceName string, config config.Config) (Service, error)
	End() error
}

type meshBuilderImpl struct {
	slf4go.Logger                        // mixin logger
	injector        injector.Injector    // injector context
	registers       map[string]string    // registers services
	orderServices   []string             //order service name
	extensions      map[string]Extension // extensions
	orderExtensions []Extension          // order extension names
}

func newMeshBuilder() MeshBuilder {
	return &meshBuilderImpl{
		Logger:     slf4go.Get("gomesh"),
		registers:  make(map[string]string),
		extensions: make(map[string]Extension),
		injector:   injector.New(),
	}
}

func (builder *meshBuilderImpl) RegisterService(extensionName string, serviceName string) error {

	_, ok := builder.registers[serviceName]

	if ok {
		return xerrors.Wrapf(ErrExists, "service %s exists", serviceName)
	}

	if _, ok := builder.extensions[extensionName]; !ok {
		return xerrors.Wrapf(ErrNotFound, "extension %s not found", extensionName)
	}

	builder.registers[serviceName] = extensionName
	builder.orderServices = append(builder.orderServices, serviceName)

	return nil
}

func (builder *meshBuilderImpl) RegisterExtension(extension Extension) error {

	_, ok := builder.extensions[extension.Name()]

	if ok {
		return xerrors.Wrapf(ErrExists, "extension %s exists", extension.Name())
	}

	builder.extensions[extension.Name()] = extension
	builder.orderExtensions = append(builder.orderExtensions, extension)

	return nil
}

func (builder *meshBuilderImpl) FindService(name string, service interface{}) {
	builder.injector.Get(name, service)
}

func (builder *meshBuilderImpl) Start(config config.Config) error {

	for _, extension := range builder.extensions {
		subconfig, err := extend.SubConfig(config, "gomesh", "extension", extension.Name())

		if err != nil {
			return xerrors.Wrapf(err, "get config gomesh.extension.%s error", extension.Name())
		}

		builder.DebugF("call extension %s initialize routine", extension.Name())

		if err := extension.Begin(subconfig, builder); err != nil {
			return xerrors.Wrapf(err, "start extension %s error", extension.Name())
		}

		builder.DebugF("call extension %s initialize routine -- success", extension.Name())
	}

	var services []ServiceRegisterEntry

	for _, serviceName := range builder.orderServices {
		subconfig, err := extend.SubConfig(config, "gomesh", "service", serviceName)

		if err != nil {
			return xerrors.Wrapf(err, "get config gomesh.service.%s error", serviceName)
		}

		extension := builder.extensions[builder.registers[serviceName]]

		builder.DebugF("create service %s by extension %s", serviceName, extension.Name())

		service, err := extension.CreateSerivce(serviceName, subconfig)

		if err != nil {
			return xerrors.Wrapf(err, "create service %s by extension %s error", serviceName, extension.Name())
		}

		builder.DebugF("create service %s by extension %s -- success", serviceName, extension.Name())

		builder.injector.Register(serviceName, service)

		services = append(services, ServiceRegisterEntry{Name: serviceName, Service: service})
	}

	for _, entry := range services {

		builder.DebugF("bind service %s", entry.Name)

		if err := builder.injector.Bind(entry.Service); err != nil {
			return xerrors.Wrapf(err, "service %s bind error", entry.Name)
		}

		builder.DebugF("bind service %s -- success", entry.Name)
	}

	for _, extension := range builder.extensions {

		builder.DebugF("call extension %s finally routine", extension.Name())

		if err := extension.End(); err != nil {
			return xerrors.Wrapf(err, "extension %s finally routine error", extension.Name())
		}

		builder.DebugF("call extension %s finally routine -- success", extension.Name())
	}

	for _, entry := range services {
		if runnable, ok := entry.Service.(Runnable); ok {
			builder.DebugF("start runnable service %s", entry.Name)
			if err := runnable.Start(); err != nil {
				return xerrors.Wrapf(err, "start service %s error", entry.Name)
			}
			builder.DebugF("start runnable service %s -- success", entry.Name)
		}
	}

	return nil
}

var meshBuilder MeshBuilder
var once sync.Once

// Builder get mesh builder instance
func Builder() MeshBuilder {
	once.Do(func() {
		meshBuilder = newMeshBuilder()
	})

	return meshBuilder
}
