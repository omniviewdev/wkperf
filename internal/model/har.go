package model

import (
	"encoding/json"
	"math"
	"time"
)

// HAR records request bounds on the recording clock. Negative latency and
// transfer values denote missing measurements; zero is a valid measurement.
type HAR struct {
	Start, End    float64
	Timed         bool
	LatencyMS     float64
	TransferBytes float64
}

// HAR reads a network record without modifying its original export fields.
func (r *Record) HAR() HAR {
	var entry struct {
		StartedDateTime string  `json:"startedDateTime"`
		Time            float64 `json:"time"`
		Response        struct {
			TransferSize float64 `json:"_transferSize"`
			HeadersSize  float64 `json:"headersSize"`
			BodySize     float64 `json:"bodySize"`
		} `json:"response"`
	}
	entry.Time = -1
	entry.Response.TransferSize = -1
	entry.Response.HeadersSize = -1
	entry.Response.BodySize = -1
	if len(r.Entry) > 0 {
		// A wrong type in one field does not invalidate the other fields.
		if err := json.Unmarshal(r.Entry, &entry); err != nil {
			if _, ok := err.(*json.UnmarshalTypeError); !ok {
				entry.Time, entry.Response.TransferSize, entry.Response.HeadersSize, entry.Response.BodySize = -1, -1, -1, -1
			}
		}
	}
	transfer := entry.Response.TransferSize
	if transfer < 0 && entry.Response.HeadersSize >= 0 && entry.Response.BodySize >= 0 {
		transfer = entry.Response.HeadersSize + entry.Response.BodySize
	}
	if math.IsInf(transfer, 0) {
		transfer = -1
	}
	h := HAR{LatencyMS: entry.Time, TransferBytes: transfer}
	if r.StartTime != 0 || r.EndTime != 0 {
		h.Start, h.End = r.StartTime, r.EndTime
		h.Timed = finiteHAR(h.Start) && finiteHAR(h.End)
	} else if r.ArchiveStartTime != 0 && finiteHAR(r.ArchiveStartTime) && entry.StartedDateTime != "" {
		if at, err := time.Parse(time.RFC3339Nano, entry.StartedDateTime); err == nil {
			// Match WebKit's wall clock to recording clock conversion.
			h.Start = float64(at.Unix()) + float64(at.Nanosecond())/1e9 - r.ArchiveStartTime
			h.End = h.Start
			if h.LatencyMS > 0 {
				h.End += h.LatencyMS / 1000
			}
			h.Timed = finiteHAR(h.Start) && finiteHAR(h.End)
		}
	}
	return h
}

func finiteHAR(v float64) bool { return !math.IsInf(v, 0) && !math.IsNaN(v) }
