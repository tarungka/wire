package pipeline

import "sync"

type Worker struct {
	pool sync.Pool
	mu   sync.Mutex
}
