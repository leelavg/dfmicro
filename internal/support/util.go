package support

func Must[T any](value T, err error) T {
	MustOK(err)
	return value
}

func MustOK(err error) {
	if err != nil {
		panic(err)
	}
}
