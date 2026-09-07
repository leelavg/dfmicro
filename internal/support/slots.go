package support

// FirstAvailableIndex returns the lowest non-negative index not occupied by
// any item.
func FirstAvailableIndex[T any](items []T, index func(T) int) int {
	occupied := make(map[int]struct{}, len(items))
	for _, item := range items {
		if slot := index(item); slot >= 0 {
			occupied[slot] = struct{}{}
		}
	}

	for slot := 0; ; slot++ {
		if _, ok := occupied[slot]; !ok {
			return slot
		}
	}
}
