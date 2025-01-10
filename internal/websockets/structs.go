package websockets

import "time"

type DepthUpdate struct {
	Symbol       string `json:"s"`
	Exchange     string `json:"exchange"`
	RawTimestamp int64  `json:"timestamp"`
	Timestamp    time.Time
	Bids         [][]string `json:"bids"`
	Asks         [][]string `json:"asks"`
}
