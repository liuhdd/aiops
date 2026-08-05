package store

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"

	"aiops/internal/model"
)

type Store struct {
	db *sql.DB
}

func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS alerts (
			id TEXT PRIMARY KEY, device TEXT, metric TEXT, severity TEXT,
			message TEXT, status TEXT, fired_at DATETIME, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS pipeline_runs (
			id TEXT PRIMARY KEY, alert_id TEXT, status TEXT, iteration INTEGER,
			max_iteration INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS stage_results (
			id TEXT PRIMARY KEY, run_id TEXT, stage_id TEXT, stage_name TEXT,
			status TEXT, iteration INTEGER, output TEXT, error TEXT,
			started_at DATETIME, ended_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY, role TEXT, content TEXT, alert_id TEXT,
			created_at DATETIME)`,
		`CREATE INDEX IF NOT EXISTS idx_stage_run ON stage_results(run_id, stage_id, iteration)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveAlert(a model.Alert) error {
	_, err := s.db.Exec(`INSERT INTO alerts (id, device, metric, severity, message, status, fired_at, created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		a.ID, a.Device, a.Metric, a.Severity, a.Message, a.Status, a.FiredAt, a.CreatedAt)
	return err
}

func (s *Store) GetAlert(id string) (model.Alert, error) {
	var a model.Alert
	err := s.db.QueryRow(`SELECT id, device, metric, severity, message, status, fired_at, created_at
		FROM alerts WHERE id = ?`, id).
		Scan(&a.ID, &a.Device, &a.Metric, &a.Severity, &a.Message, &a.Status, &a.FiredAt, &a.CreatedAt)
	return a, err
}

func (s *Store) ListAlerts() ([]model.Alert, error) {
	rows, err := s.db.Query(`SELECT id, device, metric, severity, message, status, fired_at, created_at
		FROM alerts ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Alert
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.Device, &a.Metric, &a.Severity, &a.Message, &a.Status, &a.FiredAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) SaveRun(r model.PipelineRun) error {
	_, err := s.db.Exec(`INSERT INTO pipeline_runs (id, alert_id, status, iteration, max_iteration, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)`,
		r.ID, r.AlertID, r.Status, r.Iteration, r.MaxIteration, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *Store) GetRun(runID string) (model.PipelineRun, error) {
	var r model.PipelineRun
	err := s.db.QueryRow(`SELECT id, alert_id, status, iteration, max_iteration, created_at, updated_at
		FROM pipeline_runs WHERE id = ?`, runID).
		Scan(&r.ID, &r.AlertID, &r.Status, &r.Iteration, &r.MaxIteration, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}

func (s *Store) UpdateRunStatus(runID, status string) error {
	res, err := s.db.Exec(`UPDATE pipeline_runs SET status = ?, updated_at = ? WHERE id = ?`, status, time.Now(), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("run not found")
	}
	return nil
}

func (s *Store) SaveStage(st model.StageResult) error {
	_, err := s.db.Exec(`INSERT INTO stage_results (id, run_id, stage_id, stage_name, status, iteration, output, error, started_at, ended_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		st.ID, st.RunID, st.StageID, st.StageName, st.Status, st.Iteration, st.Output, st.Error, st.StartedAt, st.EndedAt)
	return err
}

func (s *Store) ListStages(runID string) ([]model.StageResult, error) {
	rows, err := s.db.Query(`SELECT id, run_id, stage_id, stage_name, status, iteration, output, error, started_at, ended_at
		FROM stage_results WHERE run_id = ? ORDER BY iteration ASC, rowid ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.StageResult
	for rows.Next() {
		var st model.StageResult
		if err := rows.Scan(&st.ID, &st.RunID, &st.StageID, &st.StageName, &st.Status, &st.Iteration, &st.Output, &st.Error, &st.StartedAt, &st.EndedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) GetLatestStage(runID, stageID string) (model.StageResult, bool, error) {
	var st model.StageResult
	err := s.db.QueryRow(`SELECT id, run_id, stage_id, stage_name, status, iteration, output, error, started_at, ended_at
		FROM stage_results WHERE run_id = ? AND stage_id = ? ORDER BY iteration DESC, rowid DESC LIMIT 1`,
		runID, stageID).
		Scan(&st.ID, &st.RunID, &st.StageID, &st.StageName, &st.Status, &st.Iteration, &st.Output, &st.Error, &st.StartedAt, &st.EndedAt)
	if err == sql.ErrNoRows {
		return st, false, nil
	}
	if err != nil {
		return st, false, err
	}
	return st, true, nil
}

func (s *Store) SaveMessage(m model.Message) error {
	_, err := s.db.Exec(`INSERT INTO messages (id, role, content, alert_id, created_at) VALUES (?,?,?,?,?)`,
		m.ID, m.Role, m.Content, m.AlertID, m.CreatedAt)
	return err
}

func (s *Store) ListMessages() ([]model.Message, error) {
	rows, err := s.db.Query(`SELECT id, role, content, alert_id, created_at FROM messages ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.AlertID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
