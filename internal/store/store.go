package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Controller represents a saved set of UniFi controller credentials.
type Controller struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Username  string `json:"username"`
	Password  string `json:"password,omitempty"`
	Site      string `json:"site"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// SyncJob represents a configured sync job.
type SyncJob struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	ControllerID   int64   `json:"controller_id"`
	NetworkListID  string  `json:"network_list_id"`
	Hostnames      string  `json:"hostnames"`
	Schedule       string  `json:"schedule"`
	Enabled        bool    `json:"enabled"`
	LastRunAt      *string `json:"last_run_at"`
	LastResult     *string `json:"last_result"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ControllerName string  `json:"controller_name,omitempty"`
}

// RunLog represents a single execution record for a sync job.
type RunLog struct {
	ID          int64   `json:"id"`
	JobID       int64   `json:"job_id"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
	Status      string  `json:"status"`
	Message     string  `json:"message"`
	ChangesMade int     `json:"changes_made"`
	Details     string  `json:"details"`
}

// Store provides SQLite-backed persistence for sync jobs and run logs.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database and runs migrations.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS controllers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL,
			username TEXT NOT NULL,
			password TEXT NOT NULL,
			site TEXT NOT NULL DEFAULT 'default',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS sync_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			controller_id INTEGER NOT NULL,
			network_list_id TEXT NOT NULL,
			hostnames TEXT NOT NULL,
			schedule TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			last_run_at TEXT,
			last_result TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY (controller_id) REFERENCES controllers(id)
		);
		CREATE TABLE IF NOT EXISTS run_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id INTEGER NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			status TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			changes_made INTEGER NOT NULL DEFAULT 0,
			details TEXT NOT NULL DEFAULT '',
			FOREIGN KEY (job_id) REFERENCES sync_jobs(id) ON DELETE CASCADE
		);
	`)
	return err
}

// ---------- Controller CRUD ----------

func (s *Store) ListControllers() ([]Controller, error) {
	rows, err := s.db.Query(`
		SELECT id, name, url, username, password, site, created_at, updated_at
		FROM controllers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Controller
	for rows.Next() {
		var c Controller
		if err := rows.Scan(&c.ID, &c.Name, &c.URL, &c.Username, &c.Password,
			&c.Site, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetController(id int64) (*Controller, error) {
	var c Controller
	err := s.db.QueryRow(`
		SELECT id, name, url, username, password, site, created_at, updated_at
		FROM controllers WHERE id = ?`, id).Scan(
		&c.ID, &c.Name, &c.URL, &c.Username, &c.Password,
		&c.Site, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CreateController(c *Controller) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	if c.Site == "" {
		c.Site = "default"
	}
	result, err := s.db.Exec(`
		INSERT INTO controllers (name, url, username, password, site, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.URL, c.Username, c.Password, c.Site, now, now)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) UpdateController(c *Controller) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE controllers SET name=?, url=?, username=?, password=?, site=?, updated_at=?
		WHERE id=?`,
		c.Name, c.URL, c.Username, c.Password, c.Site, now, c.ID)
	return err
}

func (s *Store) DeleteController(id int64) error {
	_, err := s.db.Exec("DELETE FROM controllers WHERE id = ?", id)
	return err
}

// ---------- SyncJob CRUD ----------

func (s *Store) ListJobs() ([]SyncJob, error) {
	rows, err := s.db.Query(`
		SELECT j.id, j.name, j.controller_id, j.network_list_id,
			j.hostnames, j.schedule, j.enabled,
			j.last_run_at, j.last_result, j.created_at, j.updated_at,
			COALESCE(c.name, '')
		FROM sync_jobs j
		LEFT JOIN controllers c ON c.id = j.controller_id
		ORDER BY j.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []SyncJob
	for rows.Next() {
		var j SyncJob
		var enabled int
		if err := rows.Scan(&j.ID, &j.Name, &j.ControllerID, &j.NetworkListID,
			&j.Hostnames, &j.Schedule, &enabled,
			&j.LastRunAt, &j.LastResult, &j.CreatedAt, &j.UpdatedAt,
			&j.ControllerName); err != nil {
			return nil, err
		}
		j.Enabled = enabled != 0
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Store) GetJob(id int64) (*SyncJob, error) {
	var j SyncJob
	var enabled int
	err := s.db.QueryRow(`
		SELECT j.id, j.name, j.controller_id, j.network_list_id,
			j.hostnames, j.schedule, j.enabled,
			j.last_run_at, j.last_result, j.created_at, j.updated_at,
			COALESCE(c.name, '')
		FROM sync_jobs j
		LEFT JOIN controllers c ON c.id = j.controller_id
		WHERE j.id = ?`, id).Scan(
		&j.ID, &j.Name, &j.ControllerID, &j.NetworkListID,
		&j.Hostnames, &j.Schedule, &enabled,
		&j.LastRunAt, &j.LastResult, &j.CreatedAt, &j.UpdatedAt,
		&j.ControllerName)
	if err != nil {
		return nil, err
	}
	j.Enabled = enabled != 0
	return &j, nil
}

func (s *Store) CreateJob(j *SyncJob) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := s.db.Exec(`
		INSERT INTO sync_jobs (name, controller_id, network_list_id,
			hostnames, schedule, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		j.Name, j.ControllerID, j.NetworkListID,
		j.Hostnames, j.Schedule, boolToInt(j.Enabled),
		now, now)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) UpdateJob(j *SyncJob) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`
		UPDATE sync_jobs SET name=?, controller_id=?, network_list_id=?,
			hostnames=?, schedule=?, enabled=?, updated_at=?
		WHERE id=?`,
		j.Name, j.ControllerID, j.NetworkListID,
		j.Hostnames, j.Schedule,
		boolToInt(j.Enabled), now, j.ID)
	return err
}

func (s *Store) DeleteJob(id int64) error {
	_, err := s.db.Exec("DELETE FROM sync_jobs WHERE id = ?", id)
	return err
}

func (s *Store) UpdateJobLastRun(id int64, runAt string, result string) error {
	_, err := s.db.Exec(`
		UPDATE sync_jobs SET last_run_at=?, last_result=?, updated_at=?
		WHERE id=?`, runAt, result, runAt, id)
	return err
}

func (s *Store) CreateRunLog(l *RunLog) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO run_logs (job_id, started_at, status, message, changes_made, details)
		VALUES (?, ?, ?, ?, ?, ?)`,
		l.JobID, l.StartedAt, l.Status, l.Message, l.ChangesMade, l.Details)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) UpdateRunLog(l *RunLog) error {
	_, err := s.db.Exec(`
		UPDATE run_logs SET finished_at=?, status=?, message=?, changes_made=?, details=?
		WHERE id=?`,
		l.FinishedAt, l.Status, l.Message, l.ChangesMade, l.Details, l.ID)
	return err
}

func (s *Store) GetRunLogs(jobID int64, limit int) ([]RunLog, error) {
	rows, err := s.db.Query(`
		SELECT id, job_id, started_at, finished_at, status, message, changes_made, details
		FROM run_logs WHERE job_id = ? ORDER BY started_at DESC LIMIT ?`,
		jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []RunLog
	for rows.Next() {
		var l RunLog
		if err := rows.Scan(&l.ID, &l.JobID, &l.StartedAt, &l.FinishedAt,
			&l.Status, &l.Message, &l.ChangesMade, &l.Details); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
