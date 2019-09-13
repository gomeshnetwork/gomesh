package gomesh

import (
	"fmt"
	"sync/atomic"

	"github.com/dynamicgo/go-config"
	extend "github.com/dynamicgo/go-config-extend"
	"github.com/dynamicgo/go-config/source"
	"github.com/dynamicgo/injector"
	"github.com/dynamicgo/slf4go"
	"github.com/dynamicgo/xerrors"
)

type moduleBuilderImpl struct {
	module       Module
	serviceNames []string
	services     []Service
}

func newModuleBuidler() *moduleBuilderImpl {
	return &moduleBuilderImpl{}
}

func (moduleBuilder *moduleBuilderImpl) RegisterService(name string) {
	moduleBuilder.serviceNames = append(moduleBuilder.serviceNames, name)
}

type meshImpl struct {
	slf4go.Logger
	injector injector.Injector    // injector context
	init     atomic.Value         // started
	builders []*moduleBuilderImpl // module list
}

// New create new mesh instance
func New() Mesh {
	mesh := &meshImpl{
		Logger:   slf4go.Get("gomesh"),
		injector: injector.New(),
	}

	mesh.init.Store(false)

	return mesh
}

func (mesh *meshImpl) Module(module Module) ModuleBuilder {
	builder := newModuleBuidler()

	builder.module = module

	mesh.builders = append(mesh.builders, builder)

	mesh.injector.Register(fmt.Sprintf("%s-%p", module.Name(), module), module)

	return builder
}

func (mesh *meshImpl) Services(serviceSlice interface{}) {
	if !mesh.init.Load().(bool) {
		return
	}

	mesh.injector.Find(serviceSlice)
}

func (mesh *meshImpl) ServiceByName(name string, service interface{}) {
	if !mesh.init.Load().(bool) {
		return
	}

	mesh.injector.Get(name, service)
}

func (mesh *meshImpl) Start(loaders ...ConfigLoader) error {
	// first load configs

	sources := []source.Source{}

	for _, loader := range loaders {
		sources = append(sources, loader.Load()...)
	}

	config := config.NewConfig()

	err := config.Load(sources...)

	if err != nil {
		return xerrors.Wrapf(err, "load config error")
	}

	builders := mesh.builders

	for _, builder := range builders {

		subconfig, err := extend.SubConfig(config, "gomesh", "module", builder.module.Name())

		if err != nil {
			return xerrors.Wrapf(err, "get config of module %s error", builder.module.Name())
		}

		builder.module.Config(subconfig)

		err = builder.module.BeginCreateService()

		if err != nil {
			return xerrors.Wrapf(err, "call module %s BeginCreateService error", builder.module.Name())
		}

		for _, name := range builder.serviceNames {

			subconfig, err := extend.SubConfig(config, "gomesh", "service", name)

			if err != nil {
				return xerrors.Wrapf(err, "get config of service %s error", name)
			}

			service, err := builder.module.CreateService(name, subconfig)

			if err != nil {
				return xerrors.Wrapf(err, "create service %s error")
			}

			mesh.injector.Register(name, service)

			builder.services = append(builder.services, service)
		}

		err = builder.module.EndCreateService()

		if err != nil {
			return xerrors.Wrapf(err, "call module %s EndCreateService error", builder.module.Name())
		}
	}

	// inject services
	for _, builder := range builders {

		mesh.DebugF("injector module %s", builder.module.Name())

		if err := mesh.injector.Bind(builder.module); err != nil {
			return xerrors.Wrapf(err, "inject module %s error", builder.module.Name())
		}

		mesh.DebugF("injector module %s -- success", builder.module.Name())

		for i, service := range builder.services {
			mesh.DebugF("injector service %s", builder.serviceNames[i])

			if err := mesh.injector.Bind(service); err != nil {
				return xerrors.Wrapf(err, "inject service %s error", builder.serviceNames[i])
			}

			mesh.DebugF("injector service %s -- success", builder.serviceNames[i])
		}
	}

	// setup service
	for _, builder := range builders {

		err := builder.module.BeginSetupService()

		if err != nil {
			return xerrors.Wrapf(err, "call module %s BeginSetupService error", builder.module.Name())
		}

		for _, service := range builder.services {

			err := builder.module.SetupService(service)

			if err != nil {
				return xerrors.Wrapf(err, "create service %s error")
			}
		}

		err = builder.module.EndSetupService()

		if err != nil {
			return xerrors.Wrapf(err, "call module %s EndSetupService error", builder.module.Name())
		}
	}

	// start service
	for _, builder := range builders {

		err := builder.module.BeginStartService()

		if err != nil {
			return xerrors.Wrapf(err, "call module %s BeginStartService error", builder.module.Name())
		}

		for _, service := range builder.services {

			runnable, ok := service.(Runnable)

			if ok {
				if err := runnable.Start(); err != nil {
					return err
				}
			}

			err := builder.module.StartService(service)

			if err != nil {
				return xerrors.Wrapf(err, "create service %s error")
			}
		}

		err = builder.module.EndStarService()

		if err != nil {
			return xerrors.Wrapf(err, "call module %s EndStarService error", builder.module.Name())
		}
	}

	mesh.init.Store(true)

	return nil
}
