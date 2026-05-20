package tripcdp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type renderer struct {
	logPath       string
	logLevel      string
	jsonLines     bool
	lastProgress  string
	seenErrorOnce map[string]bool
	mu            sync.Mutex
}

func newRenderer(logFile string, logLevel string, jsonLines bool) *renderer {
	if logLevel == "" {
		logLevel = "normal"
	}
	return &renderer{logPath: logFile, logLevel: logLevel, jsonLines: jsonLines, seenErrorOnce: map[string]bool{}}
}

func (r *renderer) emit(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if event["event"] == nil {
		event["event"] = "log"
	}
	if event["ts"] == nil {
		event["ts"] = time.Now().Format("2006-01-02T15:04:05-0700")
	}
	if r.logPath == "" {
		if path, ok := event["log_file"].(string); ok && path != "" {
			r.logPath = path
		}
	}
	r.writeLog(event)
	if r.jsonLines {
		raw, _ := json.Marshal(event)
		fmt.Println(string(raw))
		return
	}
	r.writeHuman(event)
}

func (r *renderer) writeLog(event Event) {
	if r.logPath == "" {
		return
	}
	path := expandPath(r.logPath)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer handle.Close()
	raw, _ := json.Marshal(event)
	_, _ = handle.Write(append(raw, '\n'))
	if tb, ok := event["traceback"].(string); ok && tb != "" {
		_, _ = handle.WriteString(strings.TrimRight(tb, "\n") + "\n")
	}
}

func (r *renderer) writeHuman(event Event) {
	name, _ := event["event"].(string)
	if r.logLevel == "quiet" && name != "run_started" && name != "run_done" && name != "error" {
		return
	}
	switch name {
	case "run_started":
		r.out(fmt.Sprintf(
			"run started vendors=%v urls=%v workers=%v headless=%v rating_only=%v real_price=%v product_info=%v mode=%v output=%v",
			valueOr(event["vendor_count"], 1),
			valueOr(event["url_count"], 0),
			valueOr(event["workers"], 1),
			event["headless"],
			event["rating_only"],
			event["extract_real_price"],
			event["extract_product_info"],
			valueOr(event["run_mode"], "default"),
			valueOr(valueOr(event["output"], event["output_dir"]), "auto"),
		))
	case "browser_prepare", "worker_spawn_started", "worker_ready", "process_started", "runtime_init_started", "chrome_detect_started", "chrome_detect_done", "browser_start_started", "browser_start_done":
		if r.logLevel == "normal" || r.logLevel == "verbose" {
			r.out(prefixVendor(event, fmt.Sprint(valueOr(event["message"], name))))
		}
	case "vendor_started":
		if r.logLevel == "normal" || r.logLevel == "verbose" {
			r.out(fmt.Sprintf("[%v] started urls=%v", event["vendor"], valueOr(event["url_count"], 0)))
		}
	case "browser_start_failed", "chrome_detect_failed":
		r.err(fmt.Sprintf("[%v] %s stage=%v error=%q%s", event["vendor"], strings.ReplaceAll(name, "_", " "), valueOr(event["stage"], name), valueOr(event["error"], ""), logSuffix(event, r.logPath)))
	case "worker_failed":
		r.err(fmt.Sprintf("[%v] worker failed stage=%v exitcode=%v error=%q%s", event["vendor"], valueOr(event["stage"], "worker"), event["exitcode"], valueOr(event["error"], ""), logSuffix(event, r.logPath)))
	case "url_started":
		if r.logLevel == "normal" || r.logLevel == "verbose" {
			r.out(fmt.Sprintf("[%v] url started %v/%v url=%v", event["vendor"], event["url_index"], event["url_total"], event["url"]))
		}
	case "url_done":
		if r.logLevel == "normal" || r.logLevel == "verbose" {
			r.out(fmt.Sprintf("[%v] url done %v/%v rows=%v elapsed=%.1fs url=%v", event["vendor"], event["url_index"], event["url_total"], valueOr(event["rows"], 0), seconds(event["elapsed_ms"]), event["url"]))
		}
	case "url_failed":
		if r.logLevel == "normal" || r.logLevel == "verbose" {
			r.err(fmt.Sprintf("[%v] url failed %v/%v reason=%q elapsed=%.1fs url=%v", event["vendor"], event["url_index"], event["url_total"], valueOr(event["error"], ""), seconds(event["elapsed_ms"]), event["url"]))
		}
	case "risk_challenge_detected":
		r.err(fmt.Sprintf("[%v] risk challenge detected url=%v reason=%q%s", event["vendor"], event["url"], valueOr(event["message"], ""), logSuffix(event, r.logPath)))
	case "progress":
		if r.logLevel == "normal" || r.logLevel == "verbose" {
			active := joinAny(event["active"])
			signature := fmt.Sprintf("%v|%v|%v|%v|%s|%v", event["vendors_done"], event["vendor_count"], event["urls_done"], event["url_count"], active, event["failed"])
			if signature != r.lastProgress {
				r.lastProgress = signature
				if active == "" {
					active = "-"
				}
				r.out(fmt.Sprintf("progress urls=%v/%v vendors=%v/%v active=%s failed=%v", valueOr(event["urls_done"], 0), valueOr(event["url_count"], 0), valueOr(event["vendors_done"], 0), valueOr(event["vendor_count"], 0), active, valueOr(event["failed"], 0)))
			}
		}
	case "vendor_done":
		status := "done"
		if stopped, _ := event["stopped"].(bool); stopped && event["output_path"] != nil {
			status = "partial export"
		} else if success, ok := event["success"].(bool); ok && !success {
			status = "failed"
		}
		msg := fmt.Sprintf("[%v] %s urls=%v/%v rows=%v", event["vendor"], status, valueOr(event["urls_done"], event["url_count"]), valueOr(event["url_count"], 0), valueOr(event["rows"], 0))
		if event["output_path"] != nil {
			msg += fmt.Sprintf(" output=%v", event["output_path"])
		}
		if status == "failed" {
			msg += fmt.Sprintf(" reason=%q%s", valueOr(event["error"], ""), logSuffix(event, r.logPath))
			r.err(msg)
		} else {
			r.out(msg)
		}
	case "stop_requested":
		r.err(fmt.Sprintf("stop requested; waiting for partial exports grace=%vs", valueOr(event["grace_seconds"], 0)))
	case "force_stop_requested":
		r.err("force stop requested; terminating remaining browser work")
	case "partial_export_done":
		r.out(fmt.Sprintf("[%v] partial export rows=%v output=%v", event["vendor"], valueOr(event["rows"], 0), event["output_path"]))
	case "warning":
		if r.logLevel == "normal" || r.logLevel == "verbose" {
			r.out(fmt.Sprintf("warning: %v", event["message"]))
		}
	case "error":
		r.err(fmt.Sprintf("failed: %v", valueOr(event["message"], event["error"])))
	case "run_done":
		r.renderRunDone(event)
	case "log":
		if r.logLevel == "verbose" {
			r.out(prefixVendor(event, fmt.Sprint(valueOr(event["message"], ""))))
		}
	}
}

func (r *renderer) renderRunDone(event Event) {
	success, _ := event["success"].(bool)
	outputPaths := stringSlice(event["output_paths"])
	processed := valueOr(event["processed_count"], 0)
	if success {
		if len(outputPaths) > 0 {
			r.out(fmt.Sprintf("captured %v rows -> %d files", processed, len(outputPaths)))
			for _, path := range outputPaths {
				r.out("  " + path)
			}
			return
		}
		r.out(fmt.Sprintf("captured %v rows -> %v", processed, event["output_path"]))
		return
	}
	if len(outputPaths) > 0 {
		r.out(fmt.Sprintf("captured %v rows before failure -> %d files", processed, len(outputPaths)))
		for _, path := range outputPaths {
			r.out("  " + path)
		}
	} else if event["output_path"] != nil && fmt.Sprint(processed) != "0" {
		r.out(fmt.Sprintf("captured %v rows before failure -> %v", processed, event["output_path"]))
	}
	r.err(fmt.Sprintf("failed: %v%s", valueOr(event["error"], ""), logSuffix(event, r.logPath)))
}

func (r *renderer) out(message string) {
	fmt.Fprintln(os.Stdout, message)
}

func (r *renderer) err(message string) {
	fmt.Fprintln(os.Stderr, message)
}

func prefixVendor(event Event, message string) string {
	if vendor, ok := event["vendor"].(string); ok && vendor != "" {
		return "[" + vendor + "] " + message
	}
	return message
}

func logSuffix(event Event, fallback string) string {
	if path, ok := event["log_file"].(string); ok && path != "" {
		return "; log=" + path
	}
	if fallback != "" {
		return "; log=" + fallback
	}
	return ""
}

func valueOr(value any, fallback any) any {
	if value == nil || value == "" {
		return fallback
	}
	return value
}

func seconds(value any) float64 {
	switch v := value.(type) {
	case int:
		return float64(v) / 1000
	case int64:
		return float64(v) / 1000
	case float64:
		return v / 1000
	default:
		return 0
	}
}

func joinAny(value any) string {
	switch v := value.(type) {
	case []string:
		return strings.Join(v, ",")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ",")
	default:
		return ""
	}
}

func stringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}
