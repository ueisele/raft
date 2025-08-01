package http

import (
	"log"
	"sync"
)

// deprecationWarnings tracks whether we've shown deprecation warnings
var deprecationWarnings struct {
	once sync.Once
	shown bool
}

// showDeprecationWarning displays a deprecation warning once per program execution
// This will be uncommented in Phase 2 (v0.10.0)
func showDeprecationWarning() {
	/*
	deprecationWarnings.once.Do(func() {
		log.Printf("DEPRECATION WARNING: NewHTTPTransport() without discovery parameter is deprecated.\n" +
			"Please migrate to NewHTTPTransportWithDiscovery() or NewHTTPTransportWithStaticPeers().\n" +
			"The old constructor will require a discovery parameter in v1.0.0.\n" +
			"See MIGRATION_PEERDISCOVERY.md for migration guide.")
		deprecationWarnings.shown = true
	})
	*/
}

// Example of how to add deprecation warning in Phase 2:
// Add this line at the beginning of NewHTTPTransport():
// showDeprecationWarning()