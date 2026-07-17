package auth

import "time"

// Tokens is the result of an OAuth exchange or refresh.
type Tokens struct {
	Access  string
	Refresh string
	Expires int64 // unix milliseconds
}

// Expired reports whether the access token expires within the next minute.
func (t Tokens) Expired() bool {
	return time.Now().UnixMilli() > t.Expires-60_000
}
