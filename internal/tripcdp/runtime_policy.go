package tripcdp

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type RuntimeOptions struct {
	DeadlineHHMM                string
	URLTotalTimeout             time.Duration
	NavigationTimeout           time.Duration
	WorkerStallTimeout          time.Duration
	MaxQualityRetriesPerURL     int
	MaxBrowserRestartsPerVendor int
	VendorAPIBlockRatio         float64
	VendorAPIBlockMinAttempts   int
	GlobalAPIBlockRatio         float64
	GlobalAPIBlockMinProcessed  int
	FastURLThreshold            time.Duration
	RiskToSafeThreshold         int
	CheckpointEveryURLs         int
}

func defaultRuntimeOptions() RuntimeOptions {
	return RuntimeOptions{
		DeadlineHHMM:                "0530",
		URLTotalTimeout:             300 * time.Second,
		NavigationTimeout:           120 * time.Second,
		WorkerStallTimeout:          10 * time.Minute,
		MaxQualityRetriesPerURL:     2,
		MaxBrowserRestartsPerVendor: 2,
		VendorAPIBlockRatio:         0.70,
		VendorAPIBlockMinAttempts:   10,
		GlobalAPIBlockRatio:         0.60,
		GlobalAPIBlockMinProcessed:  50,
		FastURLThreshold:            30 * time.Second,
		RiskToSafeThreshold:         10,
		CheckpointEveryURLs:         10,
	}
}

func parseLocalDeadline(value string, now time.Time) (time.Time, bool, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "none" || value == "off" {
		return time.Time{}, false, nil
	}
	value = strings.ReplaceAll(value, ":", "")
	if len(value) != 4 {
		return time.Time{}, false, fmt.Errorf("deadline must be HHMM, HH:MM, empty, none, or off")
	}
	hour, err := strconv.Atoi(value[:2])
	if err != nil {
		return time.Time{}, false, fmt.Errorf("deadline hour must be numeric")
	}
	minute, err := strconv.Atoi(value[2:])
	if err != nil {
		return time.Time{}, false, fmt.Errorf("deadline minute must be numeric")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, false, fmt.Errorf("deadline must be a valid local time")
	}
	deadline := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !deadline.After(now) {
		deadline = deadline.Add(24 * time.Hour)
	}
	return deadline, true, nil
}

func ratioReached(count int, total int, ratio float64, minTotal int) bool {
	if total < minTotal || total <= 0 {
		return false
	}
	return float64(count)/float64(total) >= ratio
}
