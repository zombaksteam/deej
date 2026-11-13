package deej

// SessionFinder represents an entity that can find all current audio sessions
type SessionFinder interface {
	GetAllSessions() ([]Session, error)

	Release() error
}

// SessionEventsNotifier represents an entity that can notify when audio sessions change or are created
type SessionEventsNotifier interface {
	SubscribeToSessionEvents() <-chan struct{}
}

// SessionChangesChecker represents an entity that can perform a lightweight check for audio session changes
type SessionChangesChecker interface {
	HasSessionChanges() bool
}
