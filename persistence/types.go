package persistence

// Config holds persistence configuration
type Config struct {
	// DataDir is the directory for persistent data
	DataDir string

	// ServerID identifies this server's data
	ServerID int
}

// Error types for persistence failures
type PersistenceError struct {
	Op  string // "save" or "load"
	Err error
}

func (e *PersistenceError) Error() string {
	return "persistence " + e.Op + " error: " + e.Err.Error()
}
