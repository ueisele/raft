package transporttest

import "github.com/ueisele/raft"

// Decorator is the base interface for all transport decorators.
// It extends the Transport interface with the ability to unwrap
// to access the underlying transport or nested decorators.
type Decorator interface {
	raft.Transport
	// Unwrap returns the wrapped transport
	Unwrap() raft.Transport
}

// baseDecorator provides common functionality for all decorators.
// It implements the basic forwarding of Transport methods to the wrapped transport.
type baseDecorator struct {
	wrapped raft.Transport
}

// Unwrap returns the wrapped transport
func (d *baseDecorator) Unwrap() raft.Transport {
	return d.wrapped
}

// SetRPCHandler forwards to the wrapped transport
func (d *baseDecorator) SetRPCHandler(handler raft.RPCHandler) {
	d.wrapped.SetRPCHandler(handler)
}

// Start forwards to the wrapped transport
func (d *baseDecorator) Start() error {
	return d.wrapped.Start()
}

// Stop forwards to the wrapped transport
func (d *baseDecorator) Stop() error {
	return d.wrapped.Stop()
}

// GetAddress forwards to the wrapped transport
func (d *baseDecorator) GetAddress() string {
	return d.wrapped.GetAddress()
}
