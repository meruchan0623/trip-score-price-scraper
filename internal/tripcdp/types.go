package tripcdp

import "time"

const (
	defaultVendor = "手动输入"
	maxWorkers    = 5
)

type ScrapeItem struct {
	URL          string `json:"url"`
	Vendor       string `json:"vendor"`
	SourceColumn *int   `json:"source_column,omitempty"`
	SourceRow    *int   `json:"source_row,omitempty"`
	InputOrder   int    `json:"input_order"`
}

type BrowserOptions struct {
	Browser              string
	Headless             bool
	SessionFile          string
	ChromePath           string
	ChromeVersionMain    string
	DriverExecutablePath string
	UserDataDir          string
	Language             string
	Timeout              time.Duration
	MemoryGuardEnabled   bool
	MemoryLimitMB        int
	RestartEvery         int
}

type DelayRange struct {
	Min time.Duration
	Max time.Duration
}

type DelayOptions struct {
	Profile    string
	Page       DelayRange
	Scroll     DelayRange
	SKUClick   DelayRange
	SKUPrice   DelayRange
	SKUBetween DelayRange
}

type TaskRequest struct {
	InputPaths          []string
	URLsFile            string
	ExcelInput          string
	OutputPath          string
	OutputDir           string
	LogFile             string
	SessionFileExplicit bool
	Workers             int
	StartupStagger      time.Duration
	StopGraceSeconds    int
	RatingOnly          bool
	ExtractRealPrice    bool
	ExtractProductInfo  bool
	DelayOptions        DelayOptions
	BrowserOptions      BrowserOptions
	CompareVendor       string // If set, run vendor comparison against this base vendor after scrape
}

type TaskResult struct {
	Success        bool
	OutputPath     string
	OutputPaths    []string
	ProcessedCount int
	Warnings       []string
	FailedVendors  []string
	LogFile        string
	Error          string
	ExitCode       int
}

type Event map[string]any

type Reporter func(Event)

type vendorGroup struct {
	Vendor     string
	FileVendor string
	Items      []ScrapeItem
}
