package support

import (
	"io"
	"log/slog"
	"time"
)

func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				if t, ok := attr.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, t.UTC().Format("2006-01-02T15:04:05Z"))
				}
			}
			return attr
		},
	}))
}
