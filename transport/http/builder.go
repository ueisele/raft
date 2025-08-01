package http

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ueisele/raft/transport"
)

// Builder provides a fluent interface for constructing HTTPTransport instances
type Builder struct {
	serverID   int
	address    string
	rpcTimeout time.Duration
	discovery  transport.PeerDiscovery
	httpClient *http.Client
}

// NewBuilder creates a new HTTPTransport builder
func NewBuilder(serverID int, address string) *Builder {
	return &Builder{
		serverID:   serverID,
		address:    address,
		rpcTimeout: 5 * time.Second, // Default timeout
	}
}

// WithTimeout sets the RPC timeout
func (b *Builder) WithTimeout(timeout time.Duration) *Builder {
	b.rpcTimeout = timeout
	return b
}

// WithDiscovery sets the peer discovery mechanism
func (b *Builder) WithDiscovery(discovery transport.PeerDiscovery) *Builder {
	b.discovery = discovery
	return b
}

// WithHTTPClient sets a custom HTTP client
func (b *Builder) WithHTTPClient(client *http.Client) *Builder {
	b.httpClient = client
	return b
}

// Build creates the HTTPTransport instance
func (b *Builder) Build() (*HTTPTransport, error) {
	if b.discovery == nil {
		return nil, fmt.Errorf("peer discovery mechanism is required")
	}

	httpClient := b.httpClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: b.rpcTimeout,
		}
	}

	transport := &HTTPTransport{
		serverID:   b.serverID,
		address:    b.address,
		httpClient: httpClient,
		discovery:  b.discovery,
	}

	return transport, nil
}

// NewHTTPTransportWithDiscovery creates a new HTTP transport with discovery
// This is the recommended way to create a transport for production use
func NewHTTPTransportWithDiscovery(
	config *transport.Config,
	discovery transport.PeerDiscovery,
) (*HTTPTransport, error) {
	if discovery == nil {
		return nil, fmt.Errorf("discovery mechanism is required")
	}

	transport := &HTTPTransport{
		serverID: config.ServerID,
		address:  config.Address,
		httpClient: &http.Client{
			Timeout: time.Duration(config.RPCTimeout) * time.Millisecond,
		},
		discovery: discovery,
	}

	return transport, nil
}

// NewHTTPTransportWithStaticPeers creates a transport with static peer configuration
// This is a convenience method for the common case of static configuration
func NewHTTPTransportWithStaticPeers(
	config *transport.Config,
	peers map[int]string,
) (*HTTPTransport, error) {
	discovery := transport.NewStaticPeerDiscovery(peers)
	return NewHTTPTransportWithDiscovery(config, discovery)
}