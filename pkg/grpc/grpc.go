package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/channel"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"k8s.io/klog/v2"

	"github.com/OpenFunction/dapr-proxy/pkg/lb"
)

type endpoint string

func (e endpoint) String() string {
	return string(e)
}

const (
	// needed to load balance requests for target services with multiple endpoints, ie. multiple instances.
	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
	dialTimeout       = time.Second * 30
)

// ClientConnCloser combines grpc.ClientConnInterface and io.Closer
// to cover the methods used from *grpc.ClientConn.
type ClientConnCloser interface {
	grpc.ClientConnInterface
	io.Closer
}

// Manager is a wrapper around gRPC connection pooling.
type Manager struct {
	host           string
	port           int
	sslEnabled     bool
	balancer       lb.Balancer
	AppClient      ClientConnCloser
	lock           *sync.RWMutex
	connectionPool *connectionPool
	auth           security.Authenticator
	mode           modes.DaprMode
}

func NewGRPCManager(host string, port int, sslEnabled bool) *Manager {
	return &Manager{
		host:           host,
		port:           port,
		sslEnabled:     sslEnabled,
		balancer:       lb.New(),
		lock:           &sync.RWMutex{},
		connectionPool: newConnectionPool(),
		mode:           modes.KubernetesMode,
	}
}

// SetAuthenticator sets the gRPC manager a tls authenticator context.
func (g *Manager) SetAuthenticator(auth security.Authenticator) {
	g.auth = auth
}

// CreateGRPCChannel currently unused
func CreateGRPCChannel(host string, port int, conn *grpc.ClientConn) (channel.AppChannel, error) {
	ch := CreateLocalChannel(host, port, conn)
	return ch, nil
}

// StartEndpointsDetection update function endpoints
func (g *Manager) StartEndpointsDetection() {
	go func() {
		for {
			endpoints := make(map[lb.Endpoint]bool)
			hosts, err := net.LookupHost(g.host)
			if err == nil {
				for _, host := range hosts {
					ep := endpoint(fmt.Sprintf("%s:%d", host, g.port))
					endpoints[ep] = true
				}
			} else {
				klog.V(4).Info(err)
			}

			oldEndpoints := g.balancer.All()
			for _, oldEndpoint := range oldEndpoints {
				if _, ok := endpoints[oldEndpoint]; !ok {
					g.balancer.Remove(oldEndpoint)
				}
			}

			for ep := range endpoints {
				conn, teardown, err := g.getGRPCConnection(context.TODO(), ep.String(), "", "", true, false, g.sslEnabled)
				teardown()
				state := conn.GetState()
				if err != nil {
					klog.Error(err)
				} else if state == connectivity.Ready || state == connectivity.Idle {
					g.balancer.Add(ep)
				} else {
					g.balancer.Remove(ep)
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
}

func (g *Manager) GetGRPCConnection() (*grpc.ClientConn, func(), error) {
	address, _ := g.balancer.Next(lb.DummyFactor)
	if address == nil {
		return nil, func() {}, errors.Errorf("No available endpoints")
	}

	conn, teardown, err := g.getGRPCConnection(context.TODO(), address.String(), "", "", true, false, false)
	if err != nil {
		return nil, teardown, errors.Errorf("error establishing connection to app grpc on address %s: %s", address, err)
	}

	return conn, func() {
		teardown()
		state := conn.GetState()
		if state != connectivity.Ready && state != connectivity.Idle {
			g.balancer.Remove(address)
			g.lock.Lock()
			defer g.lock.Unlock()
			g.connectionPool.Remove(address.String())
			teardown()
		}
	}, nil
}

// getGRPCConnection returns a new grpc connection for a given address and inits one if doesn't exist.
func (g *Manager) getGRPCConnection(ctx context.Context, address, id string, namespace string, skipTLS, recreateIfExists, sslEnabled bool, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(), error) {
	releaseFactory := func(conn *grpc.ClientConn) func() {
		return func() {
			g.connectionPool.Release(conn)
		}
	}

	// share pooled connection
	if !recreateIfExists {
		g.lock.RLock()
		if conn, ok := g.connectionPool.Share(address); ok {
			g.lock.RUnlock()

			teardown := releaseFactory(conn)
			return conn, teardown, nil
		}
		g.lock.RUnlock()

		g.lock.RLock()
		// read the value once again, as a concurrent writer could create it
		if conn, ok := g.connectionPool.Share(address); ok {
			g.lock.RUnlock()

			teardown := releaseFactory(conn)
			return conn, teardown, nil
		}
		g.lock.RUnlock()
	}

	opts := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(grpcServiceConfig),
	}

	if diag.DefaultGRPCMonitoring.IsEnabled() {
		opts = append(opts, grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()))
	}

	transportCredentialsAdded := false
	if !skipTLS && g.auth != nil {
		signedCert := g.auth.GetCurrentSignedCert()
		cert, err := tls.X509KeyPair(signedCert.WorkloadCert, signedCert.PrivateKeyPem)
		if err != nil {
			return nil, func() {}, errors.Errorf("error generating x509 Key Pair: %s", err)
		}

		var serverName string
		if id != "cluster.local" {
			serverName = fmt.Sprintf("%s.%s.svc.cluster.local", id, namespace)
		}

		// nolint:gosec
		ta := credentials.NewTLS(&tls.Config{
			ServerName:   serverName,
			Certificates: []tls.Certificate{cert},
			RootCAs:      signedCert.TrustChain,
		})
		opts = append(opts, grpc.WithTransportCredentials(ta))
		transportCredentialsAdded = true
	}

	ctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	dialPrefix := GetDialAddressPrefix(g.mode)
	if sslEnabled {
		// nolint:gosec
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true,
		})))
		transportCredentialsAdded = true
	}

	if !transportCredentialsAdded {
		opts = append(opts, grpc.WithInsecure())
	}

	opts = append(opts, customOpts...)
	conn, err := grpc.DialContext(ctx, dialPrefix+address, opts...)
	if err != nil {
		return nil, func() {}, err
	}

	teardown := releaseFactory(conn)
	g.lock.Lock()
	defer g.lock.Unlock()
	g.connectionPool.Register(address, conn)

	return conn, teardown, nil
}

type connectionPool struct {
	pool           map[string]*grpc.ClientConn
	referenceCount map[*grpc.ClientConn]int
	referenceLock  *sync.RWMutex
}

func newConnectionPool() *connectionPool {
	return &connectionPool{
		pool:           map[string]*grpc.ClientConn{},
		referenceCount: map[*grpc.ClientConn]int{},
		referenceLock:  &sync.RWMutex{},
	}
}

func (p *connectionPool) Register(address string, conn *grpc.ClientConn) {
	if oldConn, ok := p.pool[address]; ok {
		// oldConn is not used by pool anymore
		p.Release(oldConn)
	}

	p.pool[address] = conn
	// conn is used by caller and pool
	// NOTE: pool should also increment referenceCount not to close the pooled connection

	p.referenceLock.Lock()
	defer p.referenceLock.Unlock()
	p.referenceCount[conn] = 2
}

func (p *connectionPool) Remove(address string) {
	if conn, ok := p.pool[address]; ok {
		delete(p.pool, address)
		conn.Close()
	}
}

func (p *connectionPool) Share(address string) (*grpc.ClientConn, bool) {
	conn, ok := p.pool[address]
	if !ok {
		return nil, false
	}

	p.referenceLock.Lock()
	defer p.referenceLock.Unlock()

	p.referenceCount[conn]++
	return conn, true
}

func (p *connectionPool) Release(conn *grpc.ClientConn) {
	p.referenceLock.Lock()
	defer p.referenceLock.Unlock()

	if _, ok := p.referenceCount[conn]; !ok {
		return
	}

	p.referenceCount[conn]--

	// for concurrent use, connection is closed after all callers release it
	if p.referenceCount[conn] <= 0 {
		conn.Close()
		delete(p.referenceCount, conn)
	}
}

// GetDialAddressPrefix returns a dial prefix for a gRPC client connections
// For a given DaprMode.
func GetDialAddressPrefix(mode modes.DaprMode) string {
	if runtime.GOOS == "windows" {
		return ""
	}

	switch mode {
	case modes.KubernetesMode:
		return "dns:///"
	default:
		return ""
	}
}
