package support

func FirstAvailableIndex[T any](items []T, index func(T) int) int {
	for i, item := range items {
		if index(item) != i {
			return i
		}
	}
	return len(items)
}
