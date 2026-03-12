package syncer

import (
	"bytes"
	"fmt"
	"log"
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
	servers, err := db.ListEnabledDNSServerAddresses()
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("load DNS servers: %v", err)}
	}
	if len(servers) == 0 {
		return SyncResult{Status: "error", Message: "no DNS servers configured: add at least one enabled DNS server"}
	}
	hostIPs, err := ResolveHostnames(job.Hostnames, servers)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("DNS resolution: %v", err)}
	}

	now := time.Now().UTC()
	retentionCutoff := now.Add(-observedIPRetention(job)).Format(time.RFC3339)
	if err := db.UpsertObservedIPs(job.ID, hostIPs, now.Format(time.RFC3339)); err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("record observed IPs: %v", err)}
	}
	if err := db.DeleteExpiredObservedIPs(job.ID, retentionCutoff); err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("cleanup observed IPs: %v", err)}
	}
	observedIPs, err := db.ListObservedIPs(job.ID, retentionCutoff)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("load observed IPs: %v", err)}
	}
	hostIPs = mergeResolvedIPs(observedIPs, hostIPs)

	ctrl, err := db.GetController(job.ControllerID)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("load controller: %v", err)}
	}

	client, err := unifi.NewClient(ctrl.URL, ctrl.Site, ctrl.APIKey, ctrl.SkipTLSVerify)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("UniFi API client: %v", err)}
	}

	nl, err := client.GetNetworkList(job.NetworkListID)
	if err != nil {
		return SyncResult{Status: "error", Message: fmt.Sprintf("get network list: %v", err)}
	}

	newIPs := SortedIPs(hostIPs)
	oldIPs := ExtractIPsFromItems(nl.Items)
	sort.Strings(oldIPs)

	added, removed, kept := DiffIPs(oldIPs, newIPs)

	if len(added) == 0 && len(removed) == 0 {
		return SyncResult{
			Status:  "success",
			Message: fmt.Sprintf("No changes needed (%d IPs match)", len(kept)),
		}
	}

	nl.Items = IPsToItems(newIPs)
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

// SortedIPs returns IPv4 addresses and IPv4 CIDRs from a host-IP map in a stable order.
func SortedIPs(hostIPs map[string]string) []string {
	ips := make([]string, 0, len(hostIPs))
	for ip := range hostIPs {
		ips = append(ips, ip)
	}
	sort.Slice(ips, func(i, j int) bool {
		leftKey, leftPrefix, leftOK := ipSortKey(ips[i])
		rightKey, rightPrefix, rightOK := ipSortKey(ips[j])
		switch {
		case leftOK && rightOK:
			if compare := bytes.Compare(leftKey, rightKey); compare != 0 {
				return compare < 0
			}
			return leftPrefix < rightPrefix
		case leftOK:
			return true
		case rightOK:
			return false
		default:
			return ips[i] < ips[j]
		}
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

// ExtractIPsFromItems extracts IP address strings from traffic matching list items.
func ExtractIPsFromItems(items []unifi.TrafficMatchItem) []string {
	var ips []string
	for _, item := range items {
		switch item.Type {
		case "IP_ADDRESS", "SUBNET":
			ips = append(ips, item.Value)
		}
	}
	return ips
}

// IPsToItems converts a sorted list of IPs into traffic matching list items.
func IPsToItems(ips []string) []unifi.TrafficMatchItem {
	items := make([]unifi.TrafficMatchItem, len(ips))
	for i, ip := range ips {
		if strings.Contains(ip, "/") {
			items[i] = unifi.TrafficMatchItem{Type: "SUBNET", Value: ip}
		} else {
			items[i] = unifi.TrafficMatchItem{Type: "IP_ADDRESS", Value: ip}
		}
	}
	return items
}

func mergeResolvedIPs(observed, current map[string]string) map[string]string {
	merged := make(map[string]string, len(observed)+len(current))
	for ip, source := range observed {
		merged[ip] = source
	}
	for ip, source := range current {
		for _, part := range strings.Split(source, ", ") {
			addResolvedSource(merged, ip, part)
		}
	}
	return merged
}

func observedIPRetention(job *store.SyncJob) time.Duration {
	ttlHours := store.DefaultObservedIPTTLHours
	if job != nil && job.ObservedIPTTLHours > 0 {
		ttlHours = job.ObservedIPTTLHours
	}
	return time.Duration(ttlHours) * time.Hour
}
