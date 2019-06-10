package gomesh

import (
	"context"
	"net"
	"sort"
	"sync"

	config "github.com/dynamicgo/go-config"
	"github.com/dynamicgo/injector"
	"github.com/dynamicgo/slf4go"
	"github.com/dynamicgo/xerrors"
	"github.com/dynamicgo/xerrors/apierr"
	"google.golang.org/grpc"
)

// ScopeOfAPIError .
const ScopeOfAPIError = "gomesh"

// errors
var (
	ErrInternal = apierr.WithScope(-1, "the internal error", ScopeOfAPIError)
	ErrAgent    = apierr.WithScope(-2, "agent implement not found", ScopeOfAPIError)
	ErrExists   = apierr.WithScope(-3, "target resource exists", ScopeOfAPIError)
)

// Service gomesh service root interface
type Service interface{}

// GrpcService grpc service support remote access
type GrpcService interface {
	Service
	GrpcHandle(server *grpc.Server) error
}

// Extension gomesh service extension module
type Extension interface {
	Order() int   // extension call order number
	Name() string // extension name

}

// ServiceExtension .
type ServiceExtension interface {
	Extension
	RegisterService(service Service) // register service to extension module
}

// RunnableExtension .
type RunnableExtension interface {
	Extension
	Start() error // start extension module
}

// GrpcUnaryInterceptor .
type GrpcUnaryInterceptor interface {
	Extension
	BeforeCallServiceMethod(ctx context.Context, method string) (context.Context, error)
	AfterCallServiceMethod(ctx context.Context, method string) error
}

// RemoteAgent remote service agent,using grpc as underlying  protocol
type RemoteAgent interface {
	Start(config config.Config) error
	Config(name string) (config.Config, error)
	Listen() (net.Listener, error)
	Connect(name string, options ...grpc.DialOption) (*grpc.ClientConn, error)
}

// LocalF local service factory function
type LocalF func(config config.Config) (Service, error)

// RemoteF remote service factory function
type RemoteF func(conn *grpc.ClientConn) (Service, error)

// Register .
type Register interface {
	LocalService(name string, F LocalF) error
	RemoteService(name string, F RemoteF) error
	RegisterExtension(extension Extension) error
	RegisterRemoteAgent(agent RemoteAgent) error
	Start(config config.Config) error
}

type localService struct {
	F    LocalF
	Name string
}

type remoteService struct {
	F    RemoteF
	Name string
}

type registerImpl struct {
	slf4go.Logger                                // mixin logger
	localServices         []*localService        // local services
	remoteServices        []*remoteService       // remote services
	injector              injector.Injector      // injector context
	remoteAgent           RemoteAgent            // remote service agent
	extensions            []Extension            // extensions
	grpcUnaryInterceptors []GrpcUnaryInterceptor // grcp unary interceptors
}

// NewServiceRegister create new service register object
func NewServiceRegister() Register {
	return &registerImpl{
		Logger:   slf4go.Get("mesh-service"),
		injector: injector.New(),
	}
}

func (register *registerImpl) checkServiceName(name string) error {
	for _, serviceF := range register.localServices {
		if serviceF.Name == name {
			return xerrors.Wrapf(injector.ErrExists, "service %s exists", name)

		}
	}

	for _, n := range register.remoteServices {
		if n.Name == name {
			return xerrors.Wrapf(injector.ErrExists, "service %s exists", name)

		}
	}

	return nil
}

func (register *registerImpl) RemoteService(name string, F RemoteF) error {

	if err := register.checkServiceName(name); err != nil {
		return err
	}

	f := &remoteService{
		Name: name,
		F:    F,
	}

	register.remoteServices = append(register.remoteServices, f)

	return nil
}

func (register *registerImpl) LocalService(name string, F LocalF) error {

	if err := register.checkServiceName(name); err != nil {
		return err
	}

	f := &localService{
		Name: name,
		F:    F,
	}

	register.localServices = append(register.localServices, f)

	return nil
}

func (register *registerImpl) RegisterRemoteAgent(agent RemoteAgent) error {

	if register.remoteAgent != nil {
		return xerrors.Wrapf(ErrExists, "duplicate register agent")
	}

	register.remoteAgent = agent

	return nil
}

func (register *registerImpl) RegisterExtension(extension Extension) error {

	for _, extension := range register.extensions {
		if extension.Name() == extension.Name() {
			return xerrors.Wrapf(ErrExists, "extension %s duplicate register", extension.Name())
		}
	}

	register.extensions = append(register.extensions, extension)

	return nil
}

func (register *registerImpl) sortExtension() {
	sort.Slice(register.extensions, func(i, j int) bool {
		return register.extensions[i].Order() < register.extensions[j].Order()
	})

	for _, extension := range register.extensions {
		if interceptor, ok := extension.(GrpcUnaryInterceptor); ok {
			register.grpcUnaryInterceptors = append(register.grpcUnaryInterceptors, interceptor)
		}
	}
}

func (register *registerImpl) createRemoteServices() error {
	register.DebugF("create remote services ...")

	for _, sf := range register.remoteServices {
		register.InfoF("create remote service %s", sf.Name)
		conn, err := register.remoteAgent.Connect(sf.Name)

		if err != nil {
			return xerrors.Wrapf(err, "create remote service %s connect error", sf.Name)
		}

		service, err := sf.F(conn)

		if err != nil {
			return xerrors.Wrapf(err, "create remote service %s proxy error", sf.Name)
		}

		register.injector.Register(sf.Name, service)
	}

	register.DebugF("create remote services -- completed")

	return nil
}

func (register *registerImpl) startGrpcServices(grpcServiceNames []string, grpcServices []GrpcService) error {

	listener, err := register.remoteAgent.Listen()

	if err != nil {
		return xerrors.Wrapf(err, "create grpc service listener error")
	}

	var server *grpc.Server

	server = grpc.NewServer(grpc.UnaryInterceptor(register.UnaryServerInterceptor))

	for i, grpcService := range grpcServices {

		register.DebugF("start grpc service %s", grpcServiceNames[i])

		if err := grpcService.GrpcHandle(server); err != nil {
			return xerrors.Wrapf(err, "call grpc service %s handle error", grpcServiceNames[i])
		}

		register.DebugF("start grpc service %s -- success", grpcServiceNames[i])
	}

	go func() {
		if err := server.Serve(listener); err != nil {
			register.ErrorF("grpc serve err %s", err)
		}
	}()

	return nil
}

func (register *registerImpl) UnaryServerInterceptor(
	ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {

	var err error

	for _, interceptor := range register.grpcUnaryInterceptors {
		ctx, err = interceptor.BeforeCallServiceMethod(ctx, info.FullMethod)

		if err != nil {
			register.ErrorF("access ctrl return error for method %s\n\t%s", info.FullMethod, err)
			err = apierr.AsGrpcError(apierr.As(err, apierr.New(-1, "UNKNOWN")))
			return nil, err
		}
	}

	resp, err := handler(ctx, req)

	if err != nil {
		register.ErrorF("call %s err %s", info.FullMethod, err)
		err = apierr.AsGrpcError(apierr.As(err, apierr.New(-1, "UNKNOWN")))
	} else {
		for _, interceptor := range register.grpcUnaryInterceptors {
			err = interceptor.AfterCallServiceMethod(ctx, info.FullMethod)

			if err != nil {
				register.ErrorF("access ctrl return error for method %s\n\t%s", info.FullMethod, err)
				err = apierr.AsGrpcError(apierr.As(err, apierr.New(-1, "UNKNOWN")))
			}
		}
	}

	return resp, err
}

func (register *registerImpl) createLocalServices(config config.Config) error {

	var services []Service
	var serviceNames []string
	var grpcServices []GrpcService
	var grpcServiceNames []string

	for _, f := range register.localServices {
		register.InfoF("create local service %s", f.Name)

		subconfig, err := register.remoteAgent.Config(f.Name)

		if err != nil {
			return xerrors.Wrapf(err, "load service %s config err", f.Name)
		}

		service, err := f.F(subconfig)

		if err != nil {
			return xerrors.Wrapf(err, "create service %s error", f.Name)
		}

		services = append(services, service)
		serviceNames = append(serviceNames, f.Name)
		register.injector.Register(f.Name, service)

		if grpcService, ok := service.(GrpcService); ok {
			register.InfoF("local service %s is a grpc service", f.Name)
			grpcServices = append(grpcServices, grpcService)
			grpcServiceNames = append(grpcServiceNames, f.Name)
		}

		for _, extension := range register.extensions {
			if serviceExtension, ok := extension.(ServiceExtension); ok {
				register.InfoF("register local service %s for extension %s", f.Name, extension.Name())
				serviceExtension.RegisterService(service)
			}
		}
	}

	for i, service := range services {
		register.DebugF("bind service %s", serviceNames[i])
		if err := register.injector.Bind(service); err != nil {
			return xerrors.Wrapf(err, "service %s bind error", serviceNames[i])
		}
	}

	for _, extension := range register.extensions {
		if runnableExtension, ok := extension.(RunnableExtension); ok {
			register.InfoF("start extension %s", extension.Name())
			if err := runnableExtension.Start(); err != nil {
				return xerrors.Wrapf(err, "start extension %s error", extension.Name())
			}
		}
	}

	return nil
}

func (register *registerImpl) Start(config config.Config) error {
	register.sortExtension()

	register.DebugF("start remote service agent")

	if err := register.remoteAgent.Start(config); err != nil {
		return xerrors.Wrapf(err, "start remote service agent error")
	}

	register.DebugF("start remote service agent -- success")

	if err := register.createRemoteServices(); err != nil {
		return err
	}

	return nil
}

var globalRegister Register
var once sync.Once

func getServiceRegister() Register {
	once.Do(func() {
		globalRegister = NewServiceRegister()
	})

	return globalRegister
}

// LocalService register local service
func LocalService(name string, F LocalF) {
	getServiceRegister().LocalService(name, F)
}

// RemoteService register remote service
func RemoteService(name string, F RemoteF) {
	getServiceRegister().RemoteService(name, F)
}

// RegisterRemoteAgent register remote service
func RegisterRemoteAgent(agent RemoteAgent) {
	getServiceRegister().RegisterRemoteAgent(agent)
}

// RegisterExtension register extension module
func RegisterExtension(extension Extension) {
	getServiceRegister().RegisterExtension(extension)
}

// Start .
func Start(config config.Config) error {
	return getServiceRegister().Start(config)
}
