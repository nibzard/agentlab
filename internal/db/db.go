package db

// Store is a placeholder for the persistence layer.
type Store struct {
	Path string
}

func Open(path string) (*Store, error) {
	return &Store{Path: path}, nil
}
