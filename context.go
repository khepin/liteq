package liteq

import "time"

type ctxVal string

const (
	CtxJobRemainingAttempts ctxVal = "remaining_attempts"
	CtxJobCreatedAt         ctxVal = "created_at"
	CtxJobLastUpdated       ctxVal = "last_updated"
)

func timeFromUnix(unixTime int64) time.Time {
	return time.Unix(unixTime, 0)
}
