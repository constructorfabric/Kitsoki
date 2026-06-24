// Package harrec provides a bounded, concurrency-safe ring-buffer recorder for
// recent /rpc request+response pairs, rendered as a HAR 1.2 archive.
package harrec

import (
	"sort"
	"sync"
	"time"

	"kitsoki/internal/runstatus/harscrub"
)

// CreatorName is the HAR creator name stamped onto every snapshot.
const CreatorName = "kitsoki-runstatus"

// harVersion is the HAR spec version produced by Snapshot.
const harVersion = "1.2"

// entry is the internal captured record for a single request/response pair.
type entry struct {
	method      string
	url         string
	reqHeaders  map[string]string
	reqBody     []byte
	status      int
	respHeaders map[string]string
	respBody    []byte
	startedUTC  time.Time
	durMs       float64
	seq         uint64 // monotonic; preserves oldest->newest ordering
}

// Recorder is a bounded ring buffer of the last N request/response pairs.
// It is safe for concurrent use by multiple goroutines.
type Recorder struct {
	mu       sync.Mutex
	buf      []entry // ring storage, len == capacity once full
	next     int     // index of next write slot
	count    int     // number of live entries (<= capacity)
	capacity int
	seq      uint64 // monotonic sequence counter
}

// New returns a Recorder retaining at most capacity most-recent entries.
// A capacity <= 0 is treated as 1 to keep the buffer usable.
func New(capacity int) *Recorder {
	if capacity <= 0 {
		capacity = 1
	}
	return &Recorder{
		buf:      make([]entry, capacity),
		capacity: capacity,
	}
}

// Record captures a single request/response pair, dropping the oldest entry
// when the buffer is full. The header maps and body slices are copied so the
// caller may safely mutate or reuse them after the call returns.
func (r *Recorder) Record(method, url string, reqHeaders map[string]string, reqBody []byte, status int, respHeaders map[string]string, respBody []byte, startedUTC time.Time, durMs float64) {
	e := entry{
		method:      method,
		url:         url,
		reqHeaders:  copyHeaders(reqHeaders),
		reqBody:     copyBytes(reqBody),
		status:      status,
		respHeaders: copyHeaders(respHeaders),
		respBody:    copyBytes(respBody),
		startedUTC:  startedUTC,
		durMs:       durMs,
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.seq++
	e.seq = r.seq
	r.buf[r.next] = e
	r.next = (r.next + 1) % r.capacity
	if r.count < r.capacity {
		r.count++
	}
}

// Depth reports the number of live entries and the configured capacity, so the
// archive/caller can log the retention horizon.
func (r *Recorder) Depth() (count, capacity int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.count, r.capacity
}

// Snapshot builds a HAR 1.2 archive from the buffered entries, ordered
// oldest->newest. The returned archive owns its own slices; it shares no
// mutable state with the Recorder and is safe to retain after further Record
// calls.
//
// The returned type is harscrub.Har — the canonical HAR 1.2 type owned by the
// sibling anonymizer package — so a Snapshot can be passed straight into
// harscrub.Scrub before it is marshalled to disk.
func (r *Recorder) Snapshot() *harscrub.Har {
	r.mu.Lock()
	live := make([]entry, 0, r.count)
	for i := 0; i < r.count; i++ {
		// Walk from oldest slot to newest. When full, oldest is at r.next.
		var idx int
		if r.count == r.capacity {
			idx = (r.next + i) % r.capacity
		} else {
			idx = i
		}
		live = append(live, r.buf[idx])
	}
	r.mu.Unlock()

	// Defensive ordering: sort by monotonic seq oldest->newest. The walk above
	// already yields this order, but sorting makes it robust to future changes.
	sort.Slice(live, func(a, b int) bool { return live[a].seq < live[b].seq })

	entries := make([]harscrub.Entry, 0, len(live))
	for _, e := range live {
		entries = append(entries, e.toHAR())
	}

	return &harscrub.Har{
		Log: harscrub.Log{
			Version: harVersion,
			Creator: harscrub.Creator{
				Name:    CreatorName,
				Version: "1",
			},
			Entries: entries,
		},
	}
}

// toHAR renders an internal entry as a harscrub.Entry.
func (e entry) toHAR() harscrub.Entry {
	var postData *harscrub.PostData
	if len(e.reqBody) > 0 {
		postData = &harscrub.PostData{
			MimeType: e.reqHeaders["Content-Type"],
			Text:     string(e.reqBody),
		}
	}

	var content *harscrub.Content
	if len(e.respBody) > 0 {
		content = &harscrub.Content{
			Size:     len(e.respBody),
			MimeType: e.respHeaders["Content-Type"],
			Text:     string(e.respBody),
		}
	}

	return harscrub.Entry{
		StartedDateTime: e.startedUTC.UTC().Format(time.RFC3339Nano),
		Time:            e.durMs,
		Request: harscrub.Request{
			Method:      e.method,
			URL:         e.url,
			Headers:     headerSlice(e.reqHeaders),
			QueryString: []harscrub.NameValue{},
			PostData:    postData,
		},
		Response: harscrub.Response{
			Status:  e.status,
			Headers: headerSlice(e.respHeaders),
			Content: content,
		},
	}
}

func copyHeaders(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

// headerSlice renders a header map as a deterministically ordered HAR slice.
func headerSlice(h map[string]string) []harscrub.NameValue {
	out := make([]harscrub.NameValue, 0, len(h))
	for k, v := range h {
		out = append(out, harscrub.NameValue{Name: k, Value: v})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}
