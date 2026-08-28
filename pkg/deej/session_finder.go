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
