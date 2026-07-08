package support

import (
	"io"
	"os"
	"time"
)

var spinnerFrames = [...]string{
	"\r[.    ]",
	"\r[..   ]",
	"\r[...  ]",
	"\r[.... ]",
	"\r[.....]",
}

func Spinner(stop <-chan struct{}, interval time.Duration) {
	var i int
	for {
		select {
		case <-stop:
			io.WriteString(os.Stdout, "\r      \r")
			return
		default:
			io.WriteString(os.Stdout, spinnerFrames[i])

			i++
			if i == len(spinnerFrames) {
				i = 0
			}
			time.Sleep(interval)
		}
	}
}
