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

	config := &transport.Config{
		ServerID:   b.serverID,
		Address:    b.address,
		RPCTimeout: int(b.rpcTimeout / time.Millisecond),
	}

	// Use custom HTTP client if provided
	trans, err := NewHTTPTransport(config, b.discovery)
	if err != nil {
		return nil, err
	}

	if b.httpClient != nil {
		trans.httpClient = b.httpClient
	}

	return trans, nil
}

// NewHTTPTransportWithStaticPeers creates a transport with static peer configuration
// This is a convenience method for the common case of static configuration
func NewHTTPTransportWithStaticPeers(
	config *transport.Config,
	peers map[int]string,
) (*HTTPTransport, error) {
	discovery := transport.NewStaticPeerDiscovery(peers)
	return NewHTTPTransport(config, discovery)
}
