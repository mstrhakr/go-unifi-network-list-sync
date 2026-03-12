package syncer

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ferventgeek/go-unifi-network-list-sync/internal/store"
	"github.com/ferventgeek/go-unifi-network-list-sync/internal/unifi"
)

// SyncResult captures the outcome of a single sync execution.
type SyncResult struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	ChangesMade int    `json:"changes_made"`
	Details     string `json:"details"`
}

// Syncer executes DNS-to-UniFi firewall group sync operations.
type Syncer struct {
	running sync.Map // prevents concurrent runs of the same job
}

func New() *Syncer {
	return &Syncer{}
}

// Run executes a sync job, logging results to the store.
func (s *Syncer) Run(db *store.Store, jobID int64) SyncResult {
	if _, loaded := s.running.LoadOrStore(jobID, true); loaded {
		return SyncResult{Status: "skipped", Message: "Job is already running"}
	}
	defer s.running.Delete(jobID)

	job, err := db.GetJob(jobID)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("load job: %v", err)}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	runLog := &store.RunLog{
		JobID:     jobID,
		StartedAt: now,
		Status:    "running",
	}
	logID, _ := db.CreateRunLog(runLog)

	result := s.execute(db, job)

	finished := time.Now().UTC().Format(time.RFC3339)
	runLog.ID = logID
	runLog.FinishedAt = &finished
	runLog.Status = result.Status
	runLog.Message = result.Message
	runLog.ChangesMade = result.ChangesMade
	runLog.Details = result.Details
	db.UpdateRunLog(runLog)
	db.UpdateJobLastRun(jobID, finished, result.Status+": "+result.Message)

	log.Printf("Job %d (%s): %s - %s", jobID, job.Name, result.Status, result.Message)
	return result
}

func (s *Syncer) execute(db *store.Store, job *store.SyncJob) SyncResult {
	hostIPs, err := ResolveHostnames(job.Hostnames)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("DNS resolution: %v", err)}
	}

	ctrl, err := db.GetController(job.ControllerID)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("load controller: %v", err)}
	}

	client, err := unifi.NewClient(ctrl.URL, ctrl.Site, ctrl.Username, ctrl.Password)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("UniFi login: %v", err)}
	}

	nl, err := client.GetNetworkList(job.NetworkListID)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("get network list: %v", err)}
	}

	newIPs := SortedIPs(hostIPs)
	oldIPs := make([]string, len(nl.GroupMembers))
	copy(oldIPs, nl.GroupMembers)
	sort.Strings(oldIPs)

	added, removed, kept := DiffIPs(oldIPs, newIPs)

	if len(added) == 0 && len(removed) == 0 {
		return SyncResult{
			Status:  "success",
			Message: fmt.Sprintf("No changes needed (%d IPs match)", len(kept)),
		}
	}

	nl.GroupMembers = newIPs
	if err := client.UpdateNetworkList(nl); err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("update network list: %v", err)}
	}

	details := FormatDiff(added, removed, kept, hostIPs)
	return SyncResult{
		Status:      "success",
		Message:     fmt.Sprintf("Updated: +%d -%d (=%d kept)", len(added), len(removed), len(kept)),
		ChangesMade: len(added) + len(removed),
		Details:     details,
	}
}

// ResolveHostnames resolves a newline-separated list of hostnames to IPv4 addresses.
func ResolveHostnames(hostnamesText string) (map[string]string, error) {
	result := make(map[string]string)
	lines := strings.Split(hostnamesText, "\n")
	var errors []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ips, err := net.LookupHost(line)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", line, err))
			continue
		}
		for _, ip := range ips {
			if parsed := net.ParseIP(ip); parsed != nil && parsed.To4() != nil {
				result[ip] = line
			}
		}
	}

	if len(result) == 0 {
		errMsg := "no IPs resolved from hostname list"
		if len(errors) > 0 {
			errMsg += ": " + strings.Join(errors, "; ")
		}
		return nil, fmt.Errorf("%s", errMsg)
	}
	return result, nil
}

// SortedIPs returns the IPs from a host-IP map sorted numerically.
func SortedIPs(hostIPs map[string]string) []string {
	ips := make([]string, 0, len(hostIPs))
	for ip := range hostIPs {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool {
		a := net.ParseIP(ips[i]).To4()
		b := net.ParseIP(ips[j]).To4()
		if a == nil || b == nil {
			return ips[i] < ips[j]
		}
		return bytes.Compare(a, b) < 0
	})
	return ips
}

// DiffIPs computes added, removed, and kept sets between old and new IP lists.
func DiffIPs(oldIPs, newIPs []string) (added, removed, kept []string) {
	oldSet := make(map[string]bool, len(oldIPs))
	for _, ip := range oldIPs {
		oldSet[ip] = true
	}
	newSet := make(map[string]bool, len(newIPs))
	for _, ip := range newIPs {
		newSet[ip] = true
	}
	for _, ip := range newIPs {
		if oldSet[ip] {
			kept = append(kept, ip)
		} else {
			added = append(added, ip)
		}
	}
	for _, ip := range oldIPs {
		if !newSet[ip] {
			removed = append(removed, ip)
		}
	}
	return
}

// FormatDiff produces a human-readable diff summary.
func FormatDiff(added, removed, kept []string, hostIPs map[string]string) string {
	var b strings.Builder
	for _, ip := range added {
		fmt.Fprintf(&b, "+ %s (%s)\n", ip, hostIPs[ip])
	}
	for _, ip := range removed {
		host := hostIPs[ip]
		if host == "" {
			host = "unknown"
		}
		fmt.Fprintf(&b, "- %s (%s)\n", ip, host)
	}
	for _, ip := range kept {
		fmt.Fprintf(&b, "  %s (%s)\n", ip, hostIPs[ip])
	}
	return b.String()
}
