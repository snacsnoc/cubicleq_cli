package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/snacsnoc/cubicleq_cli/internal/config"
)

const (
	TaskStateTodo    = "todo"
	TaskStateReady   = "ready"
	TaskStateRunning = "running"
	TaskStateBlocked = "blocked"
	TaskStateReview  = "review"
	TaskStateDone    = "done"
	TaskStateFailed  = "failed"
)

type Store struct {
	db *sql.DB
}

type Task struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Description        string    `json:"description"`
	Priority           string    `json:"priority"`
	State              string    `json:"state"`
	RoleHint           string    `json:"role_hint"`
	Dependencies       []string  `json:"dependencies"`
	BlockedReason      string    `json:"blocked_reason,omitempty"`
	ValidationCommands []string  `json:"validation_commands,omitempty"`
	AssignedAgent      string    `json:"assigned_agent,omitempty"`
	WorktreePath       string    `json:"worktree_path,omitempty"`
	BranchName         string    `json:"branch_name,omitempty"`
	FilesChanged       []string  `json:"files_changed,omitempty"`
	TestResults        []string  `json:"test_results,omitempty"`
	CompletionSummary  string    `json:"completion_summary,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type Runtime struct {
	TaskID        string    `json:"task_id"`
	BranchName    string    `json:"branch_name"`
	WorktreePath  string    `json:"worktree_path"`
	SessionID     string    `json:"session_id"`
	Status        string    `json:"status"`
	PID           int       `json:"pid"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

type Blocker struct {
	TaskID    string    `json:"task_id"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Review struct {
	TaskID          string    `json:"task_id"`
	Status          string    `json:"status"`
	SummaryPath     string    `json:"summary_path"`
	DiffSummaryPath string    `json:"diff_summary_path"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type TaskArtifact struct {
	TaskID string `json:"task_id"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
}

type ValidationRun struct {
	ID         int64     `json:"id"`
	TaskID     string    `json:"task_id"`
	Command    string    `json:"command"`
	ExitCode   int       `json:"exit_code"`
	Status     string    `json:"status"`
	StdoutPath string    `json:"stdout_path"`
	StderrPath string    `json:"stderr_path"`
	Summary    string    `json:"summary"`
	CreatedAt  time.Time `json:"created_at"`
}

type Event struct {
	ID        int64     `json:"id"`
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

func Open(root string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", config.DBPath(root))
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) InitSchema() error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			description TEXT NOT NULL,
			priority TEXT NOT NULL,
			state TEXT NOT NULL,
			role_hint TEXT NOT NULL,
			blocked_reason TEXT NOT NULL DEFAULT '',
			validation_commands TEXT NOT NULL DEFAULT '[]',
			assigned_agent TEXT NOT NULL DEFAULT '',
			worktree_path TEXT NOT NULL DEFAULT '',
			branch_name TEXT NOT NULL DEFAULT '',
			files_changed TEXT NOT NULL DEFAULT '[]',
			test_results TEXT NOT NULL DEFAULT '[]',
			completion_summary TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS task_dependencies (
			task_id TEXT NOT NULL,
			depends_on TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (task_id, depends_on)
		);`,
		`CREATE TABLE IF NOT EXISTS task_artifacts (
			task_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			path TEXT NOT NULL,
			PRIMARY KEY (task_id, kind)
		);`,
		`CREATE TABLE IF NOT EXISTS runtimes (
			task_id TEXT PRIMARY KEY,
			branch_name TEXT NOT NULL,
			worktree_path TEXT NOT NULL,
			session_id TEXT NOT NULL,
			status TEXT NOT NULL,
			pid INTEGER NOT NULL,
			last_heartbeat TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS blockers (
			task_id TEXT PRIMARY KEY,
			reason TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS validation_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			command TEXT NOT NULL,
			exit_code INTEGER NOT NULL,
			status TEXT NOT NULL,
			stdout_path TEXT NOT NULL,
			stderr_path TEXT NOT NULL,
			summary TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS reviews (
			task_id TEXT PRIMARY KEY,
			status TEXT NOT NULL,
			summary_path TEXT NOT NULL,
			diff_summary_path TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			type TEXT NOT NULL,
			payload TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func NewTaskID() string {
	return fmt.Sprintf("t-%d", time.Now().UTC().UnixNano())
}

func (s *Store) AddTask(task Task) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	deps, err := validateDependenciesTx(tx, task.ID, task.Dependencies)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(
		`INSERT INTO tasks (id, title, description, priority, state, role_hint, blocked_reason, validation_commands, assigned_agent, worktree_path, branch_name, files_changed, test_results, completion_summary, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', '', '', '[]', '[]', '', ?, ?)`,
		task.ID, task.Title, task.Description, normalizePriority(task.Priority), task.State, task.RoleHint, task.BlockedReason, toJSON(task.ValidationCommands), task.CreatedAt.Format(time.RFC3339), task.UpdatedAt.Format(time.RFC3339),
	); err != nil {
		return err
	}
	for i, dep := range deps {
		if _, err := tx.Exec(`INSERT INTO task_dependencies (task_id, depends_on, position) VALUES (?, ?, ?)`, task.ID, dep, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetTaskDependencies(id string, deps []string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ?`, id).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return fmt.Errorf("task %s not found", id)
	}
	deps, err = validateDependenciesTx(tx, id, deps)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM task_dependencies WHERE task_id = ?`, id); err != nil {
		return err
	}
	for i, dep := range deps {
		if _, err := tx.Exec(`INSERT INTO task_dependencies (task_id, depends_on, position) VALUES (?, ?, ?)`, id, dep, i); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`UPDATE tasks SET updated_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListTasks() ([]Task, error) {
	rows, err := s.db.Query(`SELECT id, title, description, priority, state, role_hint, blocked_reason, validation_commands, assigned_agent, worktree_path, branch_name, files_changed, test_results, completion_summary, created_at, updated_at FROM tasks ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		task.Dependencies, _ = s.getDependencies(task.ID)
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *Store) GetTask(id string) (Task, error) {
	row := s.db.QueryRow(`SELECT id, title, description, priority, state, role_hint, blocked_reason, validation_commands, assigned_agent, worktree_path, branch_name, files_changed, test_results, completion_summary, created_at, updated_at FROM tasks WHERE id = ?`, id)
	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	task.Dependencies, _ = s.getDependencies(task.ID)
	return task, nil
}

func (s *Store) SetTaskState(id, state, blockedReason string) error {
	_, err := s.db.Exec(`UPDATE tasks SET state = ?, blocked_reason = ?, updated_at = ? WHERE id = ?`, state, blockedReason, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) SetTaskValidationCommands(id string, commands []string) error {
	_, err := s.db.Exec(`UPDATE tasks SET validation_commands = ?, updated_at = ? WHERE id = ?`, toJSON(commands), time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) ResolveBlocker(id string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM blockers WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = '', assigned_agent = '', updated_at = ? WHERE id = ?`, TaskStateTodo, time.Now().UTC().Format(time.RFC3339), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RetryTask(id string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM blockers WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM validation_runs WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM task_artifacts WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = '', assigned_agent = '', branch_name = '', worktree_path = '', files_changed = '[]', test_results = '[]', completion_summary = '', updated_at = ? WHERE id = ?`, TaskStateTodo, time.Now().UTC().Format(time.RFC3339), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PromoteReadyTasks() error {
	autoRows, err := s.db.Query(`
		SELECT id
		FROM tasks
		WHERE state = ?
		  AND blocked_reason = ?`, TaskStateBlocked, "no validation configured")
	if err != nil {
		return err
	}
	var autoResolveIDs []string
	for autoRows.Next() {
		var id string
		if err := autoRows.Scan(&id); err != nil {
			autoRows.Close()
			return err
		}
		autoResolveIDs = append(autoResolveIDs, id)
	}
	if err := autoRows.Err(); err != nil {
		autoRows.Close()
		return err
	}
	if err := autoRows.Close(); err != nil {
		return err
	}
	for _, id := range autoResolveIDs {
		if err := s.ResolveBlocker(id); err != nil {
			return err
		}
		if err := s.RecordEvent(id, "blocker_auto_resolved_validation_optional", map[string]any{
			"reason": "no validation configured",
		}); err != nil {
			return err
		}
	}

	rows, err := s.db.Query(`SELECT id FROM tasks WHERE state = ?`, TaskStateTodo)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ready, err := s.dependenciesSatisfied(id)
		if err != nil {
			return err
		}
		if ready {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if err := s.SetTaskState(id, TaskStateReady, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) HasRunnableTasks() (bool, error) {
	ready, err := s.ListReadyTasks(1)
	if err != nil {
		return false, err
	}
	if len(ready) > 0 {
		return true, nil
	}

	rows, err := s.db.Query(`
		SELECT id
		FROM tasks
		WHERE state = ?
		  AND NOT EXISTS (
		    SELECT 1 FROM runtimes
		    WHERE runtimes.task_id = tasks.id
		      AND runtimes.status IN ('launching', 'running', 'completed')
		  )
		ORDER BY created_at`, TaskStateTodo)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return false, err
		}
		ready, err := s.dependenciesSatisfied(id)
		if err != nil {
			return false, err
		}
		if ready {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) ListReadyTasks(limit int) ([]Task, error) {
	rows, err := s.db.Query(`
		SELECT id, title, description, priority, state, role_hint, blocked_reason, validation_commands, assigned_agent, worktree_path, branch_name, files_changed, test_results, completion_summary, created_at, updated_at
		FROM tasks
		WHERE state = ?
		  AND NOT EXISTS (
		    SELECT 1 FROM runtimes
		    WHERE runtimes.task_id = tasks.id
		      AND runtimes.status IN ('launching', 'running', 'completed')
		  )
		ORDER BY CASE priority WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, created_at
		LIMIT ?`, TaskStateReady, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		task.Dependencies, _ = s.getDependencies(task.ID)
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) ListActiveRuntimes() ([]Runtime, error) {
	rows, err := s.db.Query(`SELECT task_id, branch_name, worktree_path, session_id, status, pid, last_heartbeat FROM runtimes WHERE status IN ('launching', 'running', 'completed') ORDER BY task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Runtime
	for rows.Next() {
		rt, err := scanRuntime(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

func (s *Store) ListLiveRuntimes() ([]Runtime, error) {
	rows, err := s.db.Query(`SELECT task_id, branch_name, worktree_path, session_id, status, pid, last_heartbeat FROM runtimes WHERE status IN ('launching', 'running') ORDER BY task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Runtime
	for rows.Next() {
		rt, err := scanRuntime(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

func (s *Store) ListRuntimes() ([]Runtime, error) {
	rows, err := s.db.Query(`SELECT task_id, branch_name, worktree_path, session_id, status, pid, last_heartbeat FROM runtimes ORDER BY task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Runtime
	for rows.Next() {
		rt, err := scanRuntime(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

func (s *Store) UpsertRuntime(rt Runtime) error {
	_, err := s.db.Exec(`
		INSERT INTO runtimes (task_id, branch_name, worktree_path, session_id, status, pid, last_heartbeat)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			branch_name = excluded.branch_name,
			worktree_path = excluded.worktree_path,
			session_id = excluded.session_id,
			status = excluded.status,
			pid = excluded.pid,
			last_heartbeat = excluded.last_heartbeat`,
		rt.TaskID, rt.BranchName, rt.WorktreePath, rt.SessionID, rt.Status, rt.PID, rt.LastHeartbeat.Format(time.RFC3339))
	return err
}

func (s *Store) DeleteRuntime(taskID string) error {
	_, err := s.db.Exec(`DELETE FROM runtimes WHERE task_id = ?`, taskID)
	return err
}

func (s *Store) RecordEvent(taskID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO events (task_id, type, payload, created_at) VALUES (?, ?, ?, ?)`, taskID, eventType, string(raw), time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) ListEvents(taskID string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT id, task_id, type, payload, created_at FROM events WHERE task_id = ? ORDER BY id DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var event Event
		var created string
		if err := rows.Scan(&event.ID, &event.TaskID, &event.Type, &event.Payload, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) ClaimTask(taskID, agent string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(`UPDATE tasks SET state = ?, assigned_agent = ?, updated_at = ? WHERE id = ? AND state = ?`, TaskStateRunning, agent, now, taskID, TaskStateReady)
	if err != nil {
		return err
	}
	if err := requireRowsAffected(res, "task is not ready to be claimed"); err != nil {
		return err
	}
	res, err = tx.Exec(`UPDATE runtimes SET status = ?, last_heartbeat = ? WHERE task_id = ? AND status = ?`, "running", now, taskID, "launching")
	if err != nil {
		return err
	}
	if err := requireRowsAffected(res, "runtime is no longer claimable"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordHeartbeat(taskID string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var taskState string
	if err := tx.QueryRow(`SELECT state FROM tasks WHERE id = ?`, taskID).Scan(&taskState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("task not found")
		}
		return err
	}
	if taskState != TaskStateRunning {
		return errors.New("task is not running")
	}
	res, err := tx.Exec(`UPDATE runtimes SET status = ?, last_heartbeat = ? WHERE task_id = ? AND status = ?`, "running", time.Now().UTC().Format(time.RFC3339), taskID, "running")
	if err != nil {
		return err
	}
	if err := requireRowsAffected(res, "runtime is no longer active"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) BlockTask(taskID, reason string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	var taskState string
	if err := tx.QueryRow(`SELECT state FROM tasks WHERE id = ?`, taskID).Scan(&taskState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("task not found")
		}
		return err
	}
	if taskState != TaskStateRunning {
		return errors.New("task is not running")
	}
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(task_id) DO UPDATE SET reason = excluded.reason, updated_at = excluded.updated_at`, taskID, reason, now, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = ?, assigned_agent = '', updated_at = ? WHERE id = ?`, TaskStateBlocked, reason, now, taskID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ? AND status IN ('running', 'completed', 'launching')`, taskID)
	if err != nil {
		return err
	}
	if err := requireRowsAffected(res, "runtime is no longer active"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CompleteTask(taskID, summary string, filesChanged, testResults []string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE tasks SET completion_summary = ?, files_changed = ?, test_results = ?, updated_at = ? WHERE id = ? AND state = ?`, summary, toJSON(filesChanged), toJSON(testResults), time.Now().UTC().Format(time.RFC3339), taskID, TaskStateRunning)
	if err != nil {
		return err
	}
	if err := requireRowsAffected(res, "task is not running"); err != nil {
		return err
	}
	res, err = tx.Exec(`UPDATE runtimes SET status = ? WHERE task_id = ? AND status = ?`, "completed", taskID, "running")
	if err != nil {
		return err
	}
	if err := requireRowsAffected(res, "runtime is no longer active"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MarkTaskRuntime(taskID, branchName, worktreePath string) error {
	_, err := s.db.Exec(`UPDATE tasks SET branch_name = ?, worktree_path = ?, updated_at = ? WHERE id = ?`, branchName, worktreePath, time.Now().UTC().Format(time.RFC3339), taskID)
	return err
}

func (s *Store) UpsertTaskArtifact(taskID, kind, path string) error {
	_, err := s.db.Exec(`INSERT INTO task_artifacts (task_id, kind, path) VALUES (?, ?, ?) ON CONFLICT(task_id, kind) DO UPDATE SET path = excluded.path`, taskID, kind, path)
	return err
}

func (s *Store) ListTaskArtifacts(taskID string) ([]TaskArtifact, error) {
	rows, err := s.db.Query(`SELECT task_id, kind, path FROM task_artifacts WHERE task_id = ? ORDER BY kind`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskArtifact
	for rows.Next() {
		var artifact TaskArtifact
		if err := rows.Scan(&artifact.TaskID, &artifact.Kind, &artifact.Path); err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, rows.Err()
}

func (s *Store) ListTaskWorktreePaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT worktree_path FROM tasks WHERE TRIM(worktree_path) != '' ORDER BY worktree_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, rows.Err()
}

func (s *Store) ListBlockers() ([]Blocker, error) {
	rows, err := s.db.Query(`SELECT task_id, reason, created_at, updated_at FROM blockers ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Blocker
	for rows.Next() {
		var blk Blocker
		var created, updated string
		if err := rows.Scan(&blk.TaskID, &blk.Reason, &created, &updated); err != nil {
			return nil, err
		}
		blk.CreatedAt, _ = time.Parse(time.RFC3339, created)
		blk.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, blk)
	}
	return out, rows.Err()
}

func (s *Store) InsertValidationRun(run ValidationRun) error {
	_, err := s.db.Exec(`INSERT INTO validation_runs (task_id, command, exit_code, status, stdout_path, stderr_path, summary, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		run.TaskID, run.Command, run.ExitCode, run.Status, run.StdoutPath, run.StderrPath, run.Summary, run.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) ListValidationRuns(taskID string) ([]ValidationRun, error) {
	rows, err := s.db.Query(`SELECT id, task_id, command, exit_code, status, stdout_path, stderr_path, summary, created_at FROM validation_runs WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ValidationRun
	for rows.Next() {
		var run ValidationRun
		var created string
		if err := rows.Scan(&run.ID, &run.TaskID, &run.Command, &run.ExitCode, &run.Status, &run.StdoutPath, &run.StderrPath, &run.Summary, &created); err != nil {
			return nil, err
		}
		run.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) FinalizeReview(taskID, summaryPath, diffPath string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`DELETE FROM blockers WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO reviews (task_id, status, summary_path, diff_summary_path, created_at, updated_at) VALUES (?, 'ready', ?, ?, ?, ?) ON CONFLICT(task_id) DO UPDATE SET status = 'ready', summary_path = excluded.summary_path, diff_summary_path = excluded.diff_summary_path, updated_at = excluded.updated_at`, taskID, summaryPath, diffPath, now, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO task_artifacts (task_id, kind, path) VALUES (?, 'review_summary', ?) ON CONFLICT(task_id, kind) DO UPDATE SET path = excluded.path`, taskID, summaryPath); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO task_artifacts (task_id, kind, path) VALUES (?, 'diff_summary', ?) ON CONFLICT(task_id, kind) DO UPDATE SET path = excluded.path`, taskID, diffPath); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = '', assigned_agent = '', updated_at = ? WHERE id = ?`, TaskStateReview, now, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetReviewStatus(taskID, status string) error {
	_, err := s.db.Exec(`UPDATE reviews SET status = ?, updated_at = ? WHERE task_id = ?`, status, time.Now().UTC().Format(time.RFC3339), taskID)
	return err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

func (s *Store) FailTask(taskID, reason string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`DELETE FROM blockers WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = ?, assigned_agent = '', updated_at = ? WHERE id = ?`, TaskStateFailed, reason, now, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReleaseTask(taskID, reason string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(reason) == "" {
		reason = "released by worker"
	}
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ?`, taskID)
	if err != nil {
		return err
	}
	if err := requireRowsAffected(res, "runtime is no longer active"); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO blockers (task_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(task_id) DO UPDATE SET reason = excluded.reason, updated_at = excluded.updated_at`, taskID, reason, now, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET state = ?, assigned_agent = '', blocked_reason = ?, updated_at = ? WHERE id = ?`, TaskStateBlocked, reason, now, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RejectReview(taskID, targetState string) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM blockers WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = '', assigned_agent = '', updated_at = ? WHERE id = ?`, targetState, now, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AcceptReview(taskID string, clearWorktree bool) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`DELETE FROM reviews WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM blockers WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM runtimes WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if clearWorktree {
		if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = '', assigned_agent = '', worktree_path = '', updated_at = ? WHERE id = ?`, TaskStateDone, now, taskID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE tasks SET state = ?, blocked_reason = '', assigned_agent = '', updated_at = ? WHERE id = ?`, TaskStateDone, now, taskID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListReviews() ([]Review, error) {
	rows, err := s.db.Query(`SELECT task_id, status, summary_path, diff_summary_path, created_at, updated_at FROM reviews ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Review
	for rows.Next() {
		var review Review
		var created, updated string
		if err := rows.Scan(&review.TaskID, &review.Status, &review.SummaryPath, &review.DiffSummaryPath, &created, &updated); err != nil {
			return nil, err
		}
		review.CreatedAt, _ = time.Parse(time.RFC3339, created)
		review.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, review)
	}
	return out, rows.Err()
}

func (s *Store) GetReview(taskID string) (Review, error) {
	var review Review
	var created, updated string
	err := s.db.QueryRow(`SELECT task_id, status, summary_path, diff_summary_path, created_at, updated_at FROM reviews WHERE task_id = ?`, taskID).
		Scan(&review.TaskID, &review.Status, &review.SummaryPath, &review.DiffSummaryPath, &created, &updated)
	if err != nil {
		return Review{}, err
	}
	review.CreatedAt, _ = time.Parse(time.RFC3339, created)
	review.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return review, nil
}

func (s *Store) ActiveTaskCount() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE state IN (?, ?)`, TaskStateReady, TaskStateRunning).Scan(&count)
	return count, err
}

func (s *Store) getDependencies(id string) ([]string, error) {
	rows, err := s.db.Query(`SELECT depends_on FROM task_dependencies WHERE task_id = ? ORDER BY position, depends_on`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deps []string
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return nil, err
		}
		deps = append(deps, dep)
	}
	return deps, rows.Err()
}

func (s *Store) dependenciesSatisfied(taskID string) (bool, error) {
	rows, err := s.db.Query(`SELECT depends_on FROM task_dependencies WHERE task_id = ?`, taskID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var dep string
		if err := rows.Scan(&dep); err != nil {
			return false, err
		}
		var state string
		if err := s.db.QueryRow(`SELECT state FROM tasks WHERE id = ?`, dep).Scan(&state); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, fmt.Errorf("dependency %s does not exist", dep)
			}
			return false, err
		}
		if state != TaskStateReview && state != TaskStateDone {
			return false, nil
		}
	}
	return true, rows.Err()
}

func validateDependenciesTx(tx *sql.Tx, taskID string, deps []string) ([]string, error) {
	deps, err := canonicalizeDependencies(taskID, deps)
	if err != nil {
		return nil, err
	}
	for _, dep := range deps {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = ?`, dep).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, fmt.Errorf("dependency %s not found", dep)
		}
	}
	if err := rejectDependencyCyclesTx(tx, taskID, deps); err != nil {
		return nil, err
	}
	return deps, nil
}

func canonicalizeDependencies(taskID string, deps []string) ([]string, error) {
	if len(deps) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(deps))
	seen := make(map[string]struct{}, len(deps))
	for _, dep := range deps {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			return nil, errors.New("dependency ids must not be empty")
		}
		if dep == taskID {
			return nil, errors.New("task cannot depend on itself")
		}
		if _, ok := seen[dep]; ok {
			return nil, fmt.Errorf("duplicate dependency %s", dep)
		}
		seen[dep] = struct{}{}
		out = append(out, dep)
	}
	return out, nil
}

func rejectDependencyCyclesTx(tx *sql.Tx, taskID string, deps []string) error {
	for _, dep := range deps {
		reaches, err := dependencyReachesTaskTx(tx, dep, taskID)
		if err != nil {
			return err
		}
		if reaches {
			return fmt.Errorf("dependency cycle detected involving %s", taskID)
		}
	}
	return nil
}

func dependencyReachesTaskTx(tx *sql.Tx, start, target string) (bool, error) {
	queue := []string{start}
	seen := map[string]struct{}{start: {}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == target {
			return true, nil
		}
		rows, err := tx.Query(`SELECT depends_on FROM task_dependencies WHERE task_id = ? ORDER BY position`, current)
		if err != nil {
			return false, err
		}
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				rows.Close()
				return false, err
			}
			if _, ok := seen[dep]; ok {
				continue
			}
			seen[dep] = struct{}{}
			queue = append(queue, dep)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return false, err
		}
		rows.Close()
	}
	return false, nil
}

func requireRowsAffected(res sql.Result, reason string) error {
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New(reason)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(scn scanner) (Task, error) {
	var task Task
	var validationCommands, filesChanged, testResults string
	var created, updated string
	err := scn.Scan(
		&task.ID, &task.Title, &task.Description, &task.Priority, &task.State, &task.RoleHint, &task.BlockedReason,
		&validationCommands, &task.AssignedAgent, &task.WorktreePath, &task.BranchName,
		&filesChanged, &testResults, &task.CompletionSummary, &created, &updated,
	)
	if err != nil {
		return Task{}, err
	}
	task.ValidationCommands = fromJSON(validationCommands)
	task.FilesChanged = fromJSON(filesChanged)
	task.TestResults = fromJSON(testResults)
	task.CreatedAt, _ = time.Parse(time.RFC3339, created)
	task.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	return task, nil
}

func scanRuntime(scn scanner) (Runtime, error) {
	var rt Runtime
	var heartbeat string
	if err := scn.Scan(&rt.TaskID, &rt.BranchName, &rt.WorktreePath, &rt.SessionID, &rt.Status, &rt.PID, &heartbeat); err != nil {
		return Runtime{}, err
	}
	rt.LastHeartbeat, _ = time.Parse(time.RFC3339, heartbeat)
	return rt, nil
}

func toJSON[T any](v []T) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func fromJSON(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func normalizePriority(priority string) string {
	switch strings.ToLower(strings.TrimSpace(priority)) {
	case "high":
		return "high"
	case "low":
		return "low"
	default:
		return "medium"
	}
}

func ArtifactPath(root, taskID, name string) string {
	return filepath.Join(config.RunsDir(root), taskID, name)
}
