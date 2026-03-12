package web

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ferventgeek/go-unifi-network-list-sync/internal/scheduler"
	"github.com/ferventgeek/go-unifi-network-list-sync/internal/store"
	"github.com/ferventgeek/go-unifi-network-list-sync/internal/syncer"
	"github.com/ferventgeek/go-unifi-network-list-sync/internal/unifi"
)

// Handler wires HTTP routes to the store, syncer, and scheduler.
type Handler struct {
	store     *store.Store
	syncer    *syncer.Syncer
	scheduler *scheduler.Scheduler
}

// NewHandler registers all API routes and the embedded UI file server.
func NewHandler(s *store.Store, syn *syncer.Syncer, sched *scheduler.Scheduler, uiFS fs.FS) http.Handler {
	h := &Handler{
		store:     s,
		syncer:    syn,
		scheduler: sched,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/controllers", h.listControllers)
	mux.HandleFunc("POST /api/controllers", h.createController)
	mux.HandleFunc("GET /api/controllers/{id}", h.getController)
	mux.HandleFunc("PUT /api/controllers/{id}", h.updateController)
	mux.HandleFunc("DELETE /api/controllers/{id}", h.deleteController)
	mux.HandleFunc("GET /api/controllers/{id}/network-lists", h.listNetworkLists)
	mux.HandleFunc("POST /api/controllers/test", h.testController)

	mux.HandleFunc("GET /api/jobs", h.listJobs)
	mux.HandleFunc("POST /api/jobs", h.createJob)
	mux.HandleFunc("GET /api/jobs/{id}", h.getJob)
	mux.HandleFunc("PUT /api/jobs/{id}", h.updateJob)
	mux.HandleFunc("DELETE /api/jobs/{id}", h.deleteJob)
	mux.HandleFunc("POST /api/jobs/{id}/run", h.runJob)
	mux.HandleFunc("GET /api/jobs/{id}/logs", h.getJobLogs)
	mux.HandleFunc("POST /api/resolve", h.resolveHostnames)
	mux.Handle("GET /", http.FileServer(http.FS(uiFS)))

	return loggingMiddleware(mux)
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	bytes   int
	preview []byte
}

const errorBodyPreviewMax = 512

func (rw *statusRecorder) WriteHeader(statusCode int) {
	rw.status = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *statusRecorder) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytes += n
	if len(rw.preview) < errorBodyPreviewMax {
		remaining := errorBodyPreviewMax - len(rw.preview)
		if remaining > len(b) {
			remaining = len(b)
		}
		rw.preview = append(rw.preview, b[:remaining]...)
	}
	return n, err
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}

		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		}

		switch {
		case status >= 500:
			if len(rec.preview) > 0 {
				attrs = append(attrs, "response_preview", strings.TrimSpace(string(rec.preview)))
			}
			slog.Error("HTTP request failed", attrs...)
		case status >= 400:
			if len(rec.preview) > 0 {
				attrs = append(attrs, "response_preview", strings.TrimSpace(string(rec.preview)))
			}
			slog.Warn("HTTP request error", attrs...)
		default:
			slog.Info("HTTP request", attrs...)
		}
	})
}

// ---------- Controller Handlers ----------

func (h *Handler) listControllers(w http.ResponseWriter, r *http.Request) {
	ctrls, err := h.store.ListControllers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range ctrls {
		if ctrls[i].APIKey != "" {
			ctrls[i].APIKey = "••••••••"
		}
	}
	if ctrls == nil {
		ctrls = []store.Controller{}
	}
	writeJSON(w, http.StatusOK, ctrls)
}

func (h *Handler) createController(w http.ResponseWriter, r *http.Request) {
	var c store.Controller
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if c.Name == "" || c.URL == "" || c.APIKey == "" {
		writeError(w, http.StatusBadRequest, "missing required fields")
		return
	}
	id, err := h.store.CreateController(&c)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.ID = id
	c.APIKey = "••••••••"
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) getController(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid controller ID")
		return
	}
	c, err := h.store.GetController(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "controller not found")
		return
	}
	if c.APIKey != "" {
		c.APIKey = "••••••••"
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) updateController(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid controller ID")
		return
	}
	var c store.Controller
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	// If API key is blank or the redacted placeholder, keep existing
	if c.APIKey == "" || c.APIKey == "••••••••" {
		existing, err := h.store.GetController(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "controller not found")
			return
		}
		c.APIKey = existing.APIKey
	}
	c.ID = id
	if err := h.store.UpdateController(&c); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	c.APIKey = "••••••••"
	writeJSON(w, http.StatusOK, c)
}

func (h *Handler) deleteController(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid controller ID")
		return
	}
	if err := h.store.DeleteController(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) testController(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL           string `json:"url"`
		Site          string `json:"site"`
		APIKey        string `json:"api_key"`
		SkipTLSVerify bool   `json:"skip_tls_verify"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.URL == "" || req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "url and api_key are required")
		return
	}
	client, err := unifi.NewClient(req.URL, req.Site, req.APIKey, req.SkipTLSVerify)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	lists, err := client.ListNetworkLists()
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":            true,
		"network_lists": len(lists),
	})
}

func (h *Handler) listNetworkLists(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid controller ID")
		return
	}
	ctrl, err := h.store.GetController(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "controller not found")
		return
	}
	client, err := unifi.NewClient(ctrl.URL, ctrl.Site, ctrl.APIKey, ctrl.SkipTLSVerify)
	if err != nil {
		writeError(w, http.StatusBadGateway, "UniFi API key invalid: "+err.Error())
		return
	}
	lists, err := client.ListNetworkLists()
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch network lists: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lists)
}

// ---------- Job Handlers ----------

func (h *Handler) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.store.ListJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []store.SyncJob{}
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request) {
	var job store.SyncJob
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if job.Name == "" || job.ControllerID == 0 || job.NetworkListID == "" || job.Hostnames == "" {
		writeError(w, http.StatusBadRequest, "missing required fields")
		return
	}

	id, err := h.store.CreateJob(&job)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.scheduler.Reload(id)

	job.ID = id
	writeJSON(w, http.StatusCreated, job)
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	job, err := h.store.GetJob(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) updateJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	var job store.SyncJob
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	job.ID = id
	if err := h.store.UpdateJob(&job); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	h.scheduler.Reload(id)

	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) deleteJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	h.scheduler.Remove(id)

	if err := h.store.DeleteJob(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) runJob(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	if _, err := h.store.GetJob(id); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	go h.syncer.Run(h.store, id)

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h *Handler) getJobLogs(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job ID")
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	logs, err := h.store.GetRunLogs(id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []store.RunLog{}
	}
	writeJSON(w, http.StatusOK, logs)
}

func (h *Handler) resolveHostnames(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Hostnames string `json:"hostnames"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	hostIPs, err := syncer.ResolveHostnames(input.Hostnames)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	ips := syncer.SortedIPs(hostIPs)
	type resolvedIP struct {
		IP       string `json:"ip"`
		Hostname string `json:"hostname"`
	}
	result := make([]resolvedIP, 0, len(ips))
	for _, ip := range ips {
		result = append(result, resolvedIP{IP: ip, Hostname: hostIPs[ip]})
	}
	writeJSON(w, http.StatusOK, result)
}

func parseID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
