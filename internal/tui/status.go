package tui

import (
	"sync/atomic"
)

type bucketErrorMsg struct {
	err                  error
	kind, bucket, prefix string
	request              uint64
}

type bucketDeleteCompleteMsg struct{ message string }

func (m *bucketsModel) clearError(kind string) {
	if m.errKind == "" || m.errKind == kind {
		m.err = nil
		m.errKind = ""
	}
}

func nextRequest(counter *atomic.Uint64) uint64 {
	if counter == nil {
		return 0
	}
	return counter.Add(1)
}

func currentRequest(counter *atomic.Uint64, request uint64) bool {
	return request == 0 || counter != nil && counter.Load() == request
}

func (m bucketsModel) matchesError(msg bucketErrorMsg) bool {
	if msg.bucket != "" && msg.bucket != m.currentBucketName() {
		return false
	}
	switch msg.kind {
	case "folder-count":
		return m.mode == bucketDetail && msg.prefix == m.browsePrefix && currentRequest(m.browseRequests, msg.request)
	case "browse":
		return msg.prefix == m.browsePrefix && currentRequest(m.browseRequests, msg.request)
	case "prefixes":
		return currentRequest(m.prefixRequests, msg.request)
	case "buckets":
		return currentRequest(m.listRequests, msg.request)
	}
	return true
}
