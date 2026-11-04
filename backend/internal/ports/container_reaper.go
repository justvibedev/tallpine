package ports

// LabeledResource is a container or volume that carries the session label.
type LabeledResource struct {
	// ID is the container id, or the volume name.
	ID      string
	Name    string
	Session string
	Spare   bool
}
