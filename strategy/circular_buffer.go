package strategy

import "sync"

// CircularBuffer is a generic circular buffer implementation
type CircularBuffer[T any] struct {
	buffer []T
	size   int
	head   int
	count  int
	mu     sync.RWMutex
}

// NewCircularBuffer creates a new circular buffer with the specified capacity
func NewCircularBuffer[T any](capacity int) *CircularBuffer[T] {
	return &CircularBuffer[T]{
		buffer: make([]T, capacity),
		size:   capacity,
	}
}

// PushBack adds an item to the end of the buffer
func (cb *CircularBuffer[T]) PushBack(item T) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.buffer[cb.head] = item
	cb.head = (cb.head + 1) % cb.size
	if cb.count < cb.size {
		cb.count++
	}
}

// Size returns the current number of items in the buffer
func (cb *CircularBuffer[T]) Size() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.count
}

// Capacity returns the maximum capacity of the buffer
func (cb *CircularBuffer[T]) Capacity() int {
	return cb.size
}

// Get retrieves an item at the specified index (0-based, where 0 is the oldest item)
func (cb *CircularBuffer[T]) Get(index int) T {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var zero T
	if index < 0 || index >= cb.count {
		return zero
	}

	actualIndex := (cb.head - cb.count + index + cb.size) % cb.size
	return cb.buffer[actualIndex]
}

// GetLast returns the most recent item added to the buffer
func (cb *CircularBuffer[T]) GetLast() T {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	var zero T
	if cb.count == 0 {
		return zero
	}

	return cb.Get(cb.count - 1)
}

// ToSlice returns a slice containing all items in the buffer (oldest to newest)
func (cb *CircularBuffer[T]) ToSlice() []T {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.count == 0 {
		return []T{}
	}

	result := make([]T, cb.count)
	for i := 0; i < cb.count; i++ {
		actualIndex := (cb.head - cb.count + i + cb.size) % cb.size
		result[i] = cb.buffer[actualIndex]
	}
	return result
}

// Clear removes all items from the buffer
func (cb *CircularBuffer[T]) Clear() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.count = 0
	cb.head = 0
}

// IsFull returns true if the buffer is at maximum capacity
func (cb *CircularBuffer[T]) IsFull() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.count == cb.size
}

// IsEmpty returns true if the buffer contains no items
func (cb *CircularBuffer[T]) IsEmpty() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.count == 0
}

// ForEach applies a function to each item in the buffer (oldest to newest)
func (cb *CircularBuffer[T]) ForEach(fn func(int, T)) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	for i := 0; i < cb.count; i++ {
		actualIndex := (cb.head - cb.count + i + cb.size) % cb.size
		fn(i, cb.buffer[actualIndex])
	}
}

// GetRange returns a slice of items from startIndex to endIndex (inclusive)
func (cb *CircularBuffer[T]) GetRange(startIndex, endIndex int) []T {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if startIndex < 0 || endIndex >= cb.count || startIndex > endIndex {
		return []T{}
	}

	length := endIndex - startIndex + 1
	result := make([]T, length)

	for i := 0; i < length; i++ {
		actualIndex := (cb.head - cb.count + startIndex + i + cb.size) % cb.size
		result[i] = cb.buffer[actualIndex]
	}

	return result
}
