package tripcdp

import "time"

type VendorState string

const (
	VendorStateActive     VendorState = "active"
	VendorStateCooling    VendorState = "cooling"
	VendorStateDegraded   VendorState = "degraded"
	VendorStateAPIBlocked VendorState = "api_blocked"
	VendorStatePaused     VendorState = "paused"
	VendorStateDone       VendorState = "done"
)

type vendorScheduleEntry struct {
	group     vendorGroup
	state     VendorState
	reason    string
	coolUntil time.Time
	started   bool
}

type vendorScheduler struct {
	entries []vendorScheduleEntry
}

func newVendorScheduler(groups []vendorGroup) *vendorScheduler {
	entries := make([]vendorScheduleEntry, 0, len(groups))
	for _, group := range groups {
		entries = append(entries, vendorScheduleEntry{group: group, state: VendorStateActive})
	}
	return &vendorScheduler{entries: entries}
}

func (s *vendorScheduler) Next(now time.Time) (vendorGroup, bool) {
	if s == nil {
		return vendorGroup{}, false
	}
	for i := range s.entries {
		entry := &s.entries[i]
		if entry.started || entry.state == VendorStateDone || entry.state == VendorStateAPIBlocked || entry.state == VendorStatePaused {
			continue
		}
		if entry.state == VendorStateCooling && now.Before(entry.coolUntil) {
			continue
		}
		if entry.state == VendorStateCooling && !now.Before(entry.coolUntil) {
			entry.state = VendorStateDegraded
		}
		entry.started = true
		return entry.group, true
	}
	return vendorGroup{}, false
}

func (s *vendorScheduler) Complete(result workerResult, options RuntimeOptions, now time.Time) {
	if s == nil {
		return
	}
	for i := range s.entries {
		entry := &s.entries[i]
		if entry.group.Vendor != result.Vendor {
			continue
		}
		entry.started = false
		if vendorAPIBlockedByResult(result, options) {
			entry.state = VendorStateAPIBlocked
			entry.reason = "vendor API block threshold reached"
			return
		}
		entry.state = VendorStateDone
		entry.reason = result.Error
		return
	}
}

func (s *vendorScheduler) SetCooling(vendor string, until time.Time, reason string) {
	if s == nil {
		return
	}
	for i := range s.entries {
		if s.entries[i].group.Vendor == vendor {
			s.entries[i].state = VendorStateCooling
			s.entries[i].coolUntil = until
			s.entries[i].reason = reason
		}
	}
}

func (s *vendorScheduler) State(vendor string) VendorState {
	if s == nil {
		return ""
	}
	for _, entry := range s.entries {
		if entry.group.Vendor == vendor {
			return entry.state
		}
	}
	return ""
}

func (s *vendorScheduler) PendingCount() int {
	if s == nil {
		return 0
	}
	count := 0
	for _, entry := range s.entries {
		if !entry.started && entry.state != VendorStateDone && entry.state != VendorStateAPIBlocked && entry.state != VendorStatePaused {
			count++
		}
	}
	return count
}
