package main

import (
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/agent"
)

// appendSessionLeaseHolderDetail suffixes the busy-session notice with who
// holds the lease (hostname/writer id, pid, acquisition time) so a leftover
// process is distinguishable from a real second window. A missing holder
// identity falls back to the plain notice.
func appendSessionLeaseHolderDetail(base string, err error) string {
	var leaseErr *agent.SessionLeaseError
	if !errors.As(err, &leaseErr) || leaseErr == nil || leaseErr.Info == nil {
		return base
	}
	info := leaseErr.Info
	holder := strings.TrimSpace(info.WriterID)
	if info.Hostname != "" {
		holder = info.Hostname + "/" + holder
	}
	details := fmt.Sprintf(" (held by %s, pid %d, since %s)", holder, info.PID, info.AcquiredAt.Format("2006-01-02 15:04:05"))
	if len(base)+len(details) > 400 {
		return base
	}
	return base + details
}
