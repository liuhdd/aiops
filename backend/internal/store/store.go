package store

import (
	"database/sql"
	"errors"
	"fmt"
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
	// SQLite 单写者语义：默认连接池在并发写时返回 SQLITE_BUSY。
	// 流水线在取设备锁之前会 SaveRun（见 plan Task 5），需在进程内串行化 DB 访问。
	db.SetMaxOpenConns(1)
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
		`CREATE TABLE IF NOT EXISTS scratchpad_entries (
			id TEXT PRIMARY KEY, run_id TEXT NOT NULL, step TEXT NOT NULL,
			content TEXT NOT NULL, tool_name TEXT, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS tool_events (
			id TEXT PRIMARY KEY, run_id TEXT NOT NULL, tool_name TEXT NOT NULL,
			args_json TEXT NOT NULL, result_json TEXT NOT NULL, status TEXT NOT NULL,
			duration_ms INTEGER, created_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS long_term_memory (
			id TEXT PRIMARY KEY, device TEXT NOT NULL, metric TEXT NOT NULL,
			root_cause TEXT NOT NULL, confidence REAL, actions_json TEXT,
			run_id TEXT NOT NULL, created_at DATETIME)`,
		`CREATE INDEX IF NOT EXISTS idx_scratch_run ON scratchpad_entries(run_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_run ON tool_events(run_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_ltm_dev_metric ON long_term_memory(device, metric, created_at)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	if err := ensureColumn(s.db, "alerts", "scope", "scope TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(s.db, "pipeline_runs", "scope", "scope TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(s.db, "stage_results", "scope", "scope TEXT"); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS device_context (
		device_id TEXT PRIMARY KEY,
		state_json TEXT NOT NULL DEFAULT '{}',
		updated_by TEXT,
		updated_at DATETIME)`); err != nil {
		return err
	}
	return nil
}

func ensureColumn(db *sql.DB, table, column, ddl string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", table, ddl))
	return err
}

func (s *Store) SaveAlert(a model.Alert) error {
	scopeVal := "device:" + a.Device
	_, err := s.db.Exec(`INSERT INTO alerts (id, device, metric, severity, message, status, fired_at, created_at, scope)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Device, a.Metric, a.Severity, a.Message, a.Status, a.FiredAt, a.CreatedAt, scopeVal)
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

func (s *Store) ListAlertsByDevice(device string) ([]model.Alert, error) {
	rows, err := s.db.Query(`SELECT id, device, metric, severity, message, status, fired_at, created_at
		FROM alerts WHERE device = ? ORDER BY created_at DESC`, device)
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
	_, err := s.db.Exec(`INSERT INTO pipeline_runs (id, alert_id, status, iteration, max_iteration, created_at, updated_at, scope)
		VALUES (?,?,?,?,?,?,?,?)`,
		r.ID, r.AlertID, r.Status, r.Iteration, r.MaxIteration, r.CreatedAt, r.UpdatedAt, "task:"+r.ID)
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
	_, err := s.db.Exec(`INSERT INTO stage_results (id, run_id, stage_id, stage_name, status, iteration, output, error, started_at, ended_at, scope)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			run_id=excluded.run_id, stage_id=excluded.stage_id, stage_name=excluded.stage_name,
			status=excluded.status, iteration=excluded.iteration, output=excluded.output,
			error=excluded.error, started_at=excluded.started_at, ended_at=excluded.ended_at,
			scope=excluded.scope`,
		st.ID, st.RunID, st.StageID, st.StageName, st.Status, st.Iteration, st.Output, st.Error, st.StartedAt, st.EndedAt, "task:"+st.RunID)
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

func (s *Store) SaveDeviceContext(dc model.DeviceContext) error {
	_, err := s.db.Exec(`INSERT INTO device_context (device_id, state_json, updated_by, updated_at)
		VALUES (?,?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET
			state_json=excluded.state_json, updated_by=excluded.updated_by, updated_at=excluded.updated_at`,
		dc.DeviceID, dc.StateJSON, dc.UpdatedBy, dc.UpdatedAt)
	return err
}

func (s *Store) GetDeviceContext(deviceID string) (model.DeviceContext, error) {
	var dc model.DeviceContext
	err := s.db.QueryRow(`SELECT device_id, state_json, updated_by, updated_at
		FROM device_context WHERE device_id = ?`, deviceID).
		Scan(&dc.DeviceID, &dc.StateJSON, &dc.UpdatedBy, &dc.UpdatedAt)
	return dc, err
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

// ---- 上下文工程持久化 ----

func (s *Store) InsertScratchpad(e model.ScratchpadEntry) error {
	_, err := s.db.Exec(`INSERT INTO scratchpad_entries (id, run_id, step, content, tool_name, created_at) VALUES (?,?,?,?,?,?)`,
		e.ID, e.RunID, e.Step, e.Content, e.ToolName, e.CreatedAt)
	return err
}

func (s *Store) ListScratchpad(runID string) ([]model.ScratchpadEntry, error) {
	rows, err := s.db.Query(`SELECT id, run_id, step, content, tool_name, created_at FROM scratchpad_entries WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ScratchpadEntry
	for rows.Next() {
		var e model.ScratchpadEntry
		if err := rows.Scan(&e.ID, &e.RunID, &e.Step, &e.Content, &e.ToolName, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) InsertToolEvent(ev model.ToolEvent) error {
	_, err := s.db.Exec(`INSERT INTO tool_events (id, run_id, tool_name, args_json, result_json, status, duration_ms, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		ev.ID, ev.RunID, ev.ToolName, ev.ArgsJSON, ev.ResultJSON, ev.Status, ev.DurationMS, ev.CreatedAt)
	return err
}

func (s *Store) ListToolEvents(runID string) ([]model.ToolEvent, error) {
	rows, err := s.db.Query(`SELECT id, run_id, tool_name, args_json, result_json, status, duration_ms, created_at FROM tool_events WHERE run_id = ? ORDER BY created_at ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ToolEvent
	for rows.Next() {
		var ev model.ToolEvent
		if err := rows.Scan(&ev.ID, &ev.RunID, &ev.ToolName, &ev.ArgsJSON, &ev.ResultJSON, &ev.Status, &ev.DurationMS, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *Store) AddLongTermMemory(m model.LongTermMemory) error {
	_, err := s.db.Exec(`INSERT INTO long_term_memory (id, device, metric, root_cause, confidence, actions_json, run_id, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		m.ID, m.Device, m.Metric, m.RootCause, m.Confidence, m.ActionsJSON, m.RunID, m.CreatedAt)
	return err
}

func (s *Store) ListLongTermMemory(device, metric string) ([]model.LongTermMemory, error) {
	rows, err := s.db.Query(`SELECT id, device, metric, root_cause, confidence, actions_json, run_id, created_at FROM long_term_memory WHERE device = ? AND metric = ? ORDER BY created_at DESC`, device, metric)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.LongTermMemory
	for rows.Next() {
		var m model.LongTermMemory
		if err := rows.Scan(&m.ID, &m.Device, &m.Metric, &m.RootCause, &m.Confidence, &m.ActionsJSON, &m.RunID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) RecentMessages(n int) ([]model.Message, error) {
	rows, err := s.db.Query(`SELECT id, role, content, alert_id, created_at FROM messages ORDER BY created_at DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var desc []model.Message
	for rows.Next() {
		var m model.Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.AlertID, &m.CreatedAt); err != nil {
			return nil, err
		}
		desc = append(desc, m)
	}
	// 反转回时间升序
	out := make([]model.Message, 0, len(desc))
	for i := len(desc) - 1; i >= 0; i-- {
		out = append(out, desc[i])
	}
	return out, rows.Err()
}
