package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"local-symphony/internal/core"
	"local-symphony/internal/db"
)

type Store struct {
	RepoRoot      string
	ProjectDBPath string
	AppDBPath     string
	ProjectID     string
	IssuePrefix   string
	Project       *db.DB
	App           *db.DB
}

const SupportedSchemaVersion = "1"

type SchemaVersionStatus struct {
	Version string
	Status  string
}

type CreateIssueInput struct {
	Title              string
	Description        string
	AcceptanceCriteria []string
	Priority           int
	Labels             []string
	CreatedByType      string
	CreatedByRunID     *string
}

type ListIssueOptions struct {
	States         []string
	Query          string
	Labels         []string
	DispatchPaused *bool
	Limit          int
	Offset         int
	Sort           string
}

const maxIssueListLimit = 201

type TxRunner interface {
	Exec(sql string, args ...any) error
	Query(sql string, args ...any) ([]map[string]db.Value, error)
	QueryOne(sql string, args ...any) (map[string]db.Value, error)
}

type sqlRunner = TxRunner

func (s *Store) WithProjectTx(fn func(TxRunner) error) error {
	return s.Project.WithTx(func(tx *db.Tx) error {
		return fn(tx)
	})
}

type ArtifactRecord struct {
	ID             string  `json:"id"`
	IssueID        *string `json:"issue_id"`
	RunID          *string `json:"run_id"`
	ReviewPacketID *string `json:"review_packet_id"`
	Kind           string  `json:"kind"`
	Path           string  `json:"path"`
	MimeType       *string `json:"mime_type"`
	SizeBytes      int64   `json:"size_bytes"`
	SHA256         *string `json:"sha256"`
	Redacted       bool    `json:"redacted"`
	Description    *string `json:"description"`
	CreatedAt      string  `json:"created_at"`
}

type Approval struct {
	ID            string  `json:"id"`
	RunID         string  `json:"run_id"`
	IssueID       string  `json:"issue_id"`
	Kind          string  `json:"kind"`
	Status        string  `json:"status"`
	ActionSummary string  `json:"action_summary"`
	RiskLevel     string  `json:"risk_level"`
	PolicyMatch   string  `json:"policy_match"`
	RequestedAt   string  `json:"requested_at"`
	CreatedAt     string  `json:"created_at"`
	TimeoutMS     *int64  `json:"timeout_ms"`
	ExpiresAt     *string `json:"expires_at"`
	ResolvedAt    *string `json:"resolved_at"`
	Reason        *string `json:"reason"`
}

type CreateApprovalRequestInput struct {
	RunID         string
	IssueID       string
	Kind          string
	ActionSummary string
	RiskLevel     string
	PolicyMatch   string
	RequestID     string
	TimeoutMS     int64
}

func ResolveProjectRoot(project string) (string, error) {
	if project == "" {
		project = "."
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", err
	}
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		abs = filepath.Dir(abs)
	}
	cur := abs
	for {
		if _, err := os.Stat(filepath.Join(cur, ".symphony", "project.db")); err == nil {
			return cur, nil
		}
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, nil
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return abs, nil
}

func ProjectIDForRoot(repoRoot string) string {
	abs, _ := filepath.Abs(repoRoot)
	h := sha256.Sum256([]byte(abs))
	return "prj_" + hex.EncodeToString(h[:8])
}

func InitProject(repoRoot, issuePrefix string) (*Store, error) {
	if issuePrefix == "" {
		issuePrefix = "LOC"
	}
	issuePrefix = strings.ToUpper(strings.TrimSpace(issuePrefix))
	if issuePrefix == "" {
		issuePrefix = "LOC"
	}
	root, err := ResolveProjectRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	s := &Store{RepoRoot: root, ProjectDBPath: filepath.Join(root, ".symphony", "project.db"), AppDBPath: db.AppDBPath(), ProjectID: ProjectIDForRoot(root), IssuePrefix: issuePrefix}
	projectDBExists := fileExists(s.ProjectDBPath)
	appDBExists := fileExists(s.AppDBPath)
	if projectDBExists {
		if err := validateExistingSchemaVersion(s.ProjectDBPath); err != nil {
			return nil, err
		}
	}
	if appDBExists {
		if err := validateExistingSchemaVersion(s.AppDBPath); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".symphony", "artifacts"), 0o755); err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			s.Close()
		}
	}()
	if s.Project, err = db.Open(s.ProjectDBPath); err != nil {
		return nil, err
	}
	if s.App, err = db.Open(s.AppDBPath); err != nil {
		return nil, err
	}
	appSchema, err := db.ReadSchema(root, "db/schema/v1_app.sql")
	if err != nil {
		return nil, err
	}
	projSchema, err := db.ReadSchema(root, "db/schema/v1_project.sql")
	if err != nil {
		return nil, err
	}
	if err := s.App.ExecScript(appSchema); err != nil {
		return nil, err
	}
	if err := s.Project.ExecScript(projSchema); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion(s.App, s.AppDBPath); err != nil {
		return nil, err
	}
	if err := validateSchemaVersion(s.Project, s.ProjectDBPath); err != nil {
		return nil, err
	}
	now := core.Now()
	name := filepath.Base(root)
	if err := s.Project.Exec(`INSERT OR IGNORE INTO project_info(id,name,repo_root,issue_prefix,created_at,updated_at) VALUES(?,?,?,?,?,?)`, s.ProjectID, name, root, issuePrefix, now, now); err != nil {
		return nil, err
	}
	if err := s.Project.Exec(`UPDATE project_info SET issue_prefix=?, updated_at=? WHERE id=?`, issuePrefix, now, s.ProjectID); err != nil {
		return nil, err
	}
	if err := s.upsertAppProjectMetadata(name, issuePrefix, now); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, "WORKFLOW.md")); errors.Is(err, fs.ErrNotExist) {
		body, readErr := os.ReadFile(filepath.Join(root, "examples", "WORKFLOW.default.md"))
		if readErr != nil {
			body = []byte(defaultWorkflow())
		}
		if writeErr := os.WriteFile(filepath.Join(root, "WORKFLOW.md"), body, 0o644); writeErr != nil {
			return nil, writeErr
		}
	}
	cleanup = false
	return s, nil
}

func Open(repoRoot string) (*Store, error) {
	root, err := ResolveProjectRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	s := &Store{RepoRoot: root, ProjectDBPath: filepath.Join(root, ".symphony", "project.db"), AppDBPath: db.AppDBPath(), ProjectID: ProjectIDForRoot(root), IssuePrefix: "LOC"}
	if _, err := os.Stat(s.ProjectDBPath); err != nil {
		return nil, core.NewError(core.ErrInvalidRequest, "project is not initialized; run symphony init", nil)
	}
	appDBExists := fileExists(s.AppDBPath)
	if err := validateExistingSchemaVersion(s.ProjectDBPath); err != nil {
		return nil, err
	}
	if appDBExists {
		if err := validateExistingSchemaVersion(s.AppDBPath); err != nil {
			return nil, err
		}
	}
	if s.Project, err = db.Open(s.ProjectDBPath); err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			s.Close()
		}
	}()
	if s.App, err = db.Open(s.AppDBPath); err != nil {
		return nil, err
	}
	if !appDBExists {
		appSchema, err := db.ReadSchema(root, "db/schema/v1_app.sql")
		if err != nil {
			return nil, err
		}
		if err := s.App.ExecScript(appSchema); err != nil {
			return nil, err
		}
	}
	if err := validateSchemaVersion(s.App, s.AppDBPath); err != nil {
		return nil, err
	}
	row, err := s.Project.QueryOne(`SELECT id, issue_prefix FROM project_info LIMIT 1`)
	if err != nil {
		return nil, fmt.Errorf("read project_info: %w", err)
	}
	s.ProjectID = row["id"].String()
	s.IssuePrefix = row["issue_prefix"].String()
	if !appDBExists {
		if err := s.upsertAppProjectMetadata(filepath.Base(root), s.IssuePrefix, core.Now()); err != nil {
			return nil, err
		}
	}
	cleanup = false
	return s, nil
}

func (s *Store) upsertAppProjectMetadata(name, issuePrefix, now string) error {
	workflowPath := filepath.Join(s.RepoRoot, "WORKFLOW.md")
	if err := s.App.Exec(`INSERT OR IGNORE INTO projects(id,name,repo_root,project_db_path,workflow_path,issue_prefix,created_at,updated_at,last_opened_at) VALUES(?,?,?,?,?,?,?,?,?)`, s.ProjectID, name, s.RepoRoot, s.ProjectDBPath, workflowPath, issuePrefix, now, now, now); err != nil {
		return err
	}
	return s.App.Exec(`UPDATE projects SET project_db_path=?, workflow_path=?, issue_prefix=?, updated_at=?, last_opened_at=? WHERE id=?`, s.ProjectDBPath, workflowPath, issuePrefix, now, now, s.ProjectID)
}

func (s *Store) Close() {
	if s.Project != nil {
		_ = s.Project.Close()
	}
	if s.App != nil {
		_ = s.App.Close()
	}
}

func (s *Store) DatabaseSchemaVersions() (SchemaVersionStatus, SchemaVersionStatus) {
	return schemaVersionStatus(s.App), schemaVersionStatus(s.Project)
}

func schemaVersionStatus(database *db.DB) SchemaVersionStatus {
	version, detected, err := readSchemaVersion(database)
	if err != nil {
		status := "unknown"
		if strings.HasPrefix(detected, "missing_") {
			status = "missing"
		}
		return SchemaVersionStatus{Version: detected, Status: status}
	}
	status := "unsupported"
	if isSupportedSchemaVersion(version) {
		status = "supported"
	}
	return SchemaVersionStatus{Version: version, Status: status}
}

func validateExistingSchemaVersion(dbPath string) error {
	database, err := db.OpenReadOnly(dbPath)
	if err != nil {
		return err
	}
	defer database.Close()
	return validateSchemaVersion(database, dbPath)
}

func validateSchemaVersion(database *db.DB, dbPath string) error {
	version, detected, err := readSchemaVersion(database)
	if err != nil {
		return unsupportedDBVersionError(dbPath, detected)
	}
	if !isSupportedSchemaVersion(version) {
		return unsupportedDBVersionError(dbPath, version)
	}
	return nil
}

func readSchemaVersion(database *db.DB) (string, string, error) {
	if database == nil {
		return "", "unknown", errors.New("sqlite database is unavailable")
	}
	if _, err := database.QueryOne(`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_meta'`); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "missing_schema_meta", err
		}
		return "", "unknown", err
	}
	row, err := database.QueryOne(`SELECT value FROM schema_meta WHERE key='schema_version'`)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "missing_schema_version", err
		}
		return "", "unknown", err
	}
	return row["value"].String(), row["value"].String(), nil
}

func isSupportedSchemaVersion(version string) bool {
	if _, err := strconv.Atoi(version); err != nil {
		return false
	}
	return version == SupportedSchemaVersion
}

func unsupportedDBVersionError(dbPath, detected string) *core.APIError {
	return core.NewError(core.ErrUnsupportedDBVersion, "unsupported database schema version", map[string]any{
		"detected_version":  detected,
		"expected_version":  SupportedSchemaVersion,
		"db_path":           dbPath,
		"operator_guidance": "This Local Symphony build only supports schema version 1. Use a compatible binary for this database, manually restore from an operator-maintained backup outside Symphony, or initialize a new project DB. This build will not modify unsupported databases and does not provide automatic migration, rollback, backup, or restore.",
	})
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func defaultWorkflow() string {
	return `---
tracker:
  kind: local
workspace:
  root: ~/.symphony/workspaces
git:
  repo_root: .
  branch_prefix: symphony
  auto_push: false
agent:
  handoff_required: true
  handoff_state: Human Review
  max_handoff_continuations: 1
tools:
  allow_dynamic_tools: false
  allow_mcp: false
  agent_can_set_terminal_state: false
security:
  allow_remote_api: false
---
Work only inside the current workspace. Do not push branches. Do not create pull requests. Do not mark issues Done. Do not commit unless the operator explicitly requested it outside the current run.

Complete the issue and submit the handoff via stdin:

symphony tool handoff submit --json -

Do not leave a handoff.json temporary file in the workspace root. Handoff submits data only; Human Review transition is performed by the finalizer after successful handoff processing.
`
}

func encodeJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func decodeStringSlice(s string) []string {
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	if out == nil {
		out = []string{}
	}
	return out
}
func trimSlice(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range v {
		t := strings.TrimSpace(x)
		if t == "" {
			continue
		}
		if !seen[t] {
			out = append(out, t)
			seen[t] = true
		}
	}
	return out
}
func normalizeLabels(v []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range v {
		t := strings.ToLower(strings.TrimSpace(x))
		if t == "" {
			continue
		}
		if !seen[t] {
			out = append(out, t)
			seen[t] = true
		}
	}
	sort.Strings(out)
	return out
}
func ptrFromVal(v db.Value) *string {
	if v.Null || v.String() == "" {
		return nil
	}
	s := v.String()
	return &s
}
func int64PtrFromVal(v db.Value) *int64 {
	if v.Null || v.String() == "" {
		return nil
	}
	n := v.Int64()
	return &n
}
func failPtrFromVal(v db.Value) *core.FailureCode {
	if v.Null || v.String() == "" {
		return nil
	}
	f := core.FailureCode(v.String())
	return &f
}

func (s *Store) CreateIssue(in CreateIssueInput) (*core.Issue, error) {
	now := core.Now()
	var ident string
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		var err error
		ident, _, err = s.createIssueInTx(tx, in, now)
		return err
	}); err != nil {
		return nil, err
	}
	return s.GetIssue(ident)
}

func (s *Store) CreateFollowupIssue(parentIssueID, runID string, in CreateIssueInput) (*core.Issue, error) {
	now := core.Now()
	var id string
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		var err error
		_, id, err = s.createIssueInTx(tx, in, now)
		if err != nil {
			return err
		}
		return tx.Exec(`INSERT INTO issue_relations(id,source_issue_id,target_issue_id,relation_type,active,created_by_type,created_by_run_id,created_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("rel_"), id, parentIssueID, "followup_of", 1, "agent", runID, now)
	}); err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}

func (s *Store) createIssueInTx(q sqlRunner, in CreateIssueInput, now string) (string, string, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return "", "", core.NewError(core.ErrInvalidRequest, "title is required", nil)
	}
	if in.Priority == 0 {
		in.Priority = 3
	}
	if in.Priority < 1 || in.Priority > 5 {
		return "", "", core.NewError(core.ErrInvalidRequest, "priority must be between 1 and 5", nil)
	}
	ac := trimSlice(in.AcceptanceCriteria)
	// The published acceptance smoke path creates an issue without AC and then readies it.
	// Provide a safe default checklist so the issue remains dispatch-valid without inventing a new state.
	if len(ac) == 0 && strings.TrimSpace(in.Description) != "" {
		ac = []string{"Task is complete and reviewable."}
	}
	labels := normalizeLabels(in.Labels)
	createdBy := in.CreatedByType
	if createdBy == "" {
		createdBy = "operator"
	}
	if err := q.Exec(`UPDATE counters SET value=value+1 WHERE name='issue_sequence'`); err != nil {
		return "", "", err
	}
	row, err := q.QueryOne(`SELECT value FROM counters WHERE name='issue_sequence'`)
	if err != nil {
		return "", "", err
	}
	seq := row["value"].Int()
	id := core.NewID("iss_")
	ident := fmt.Sprintf("%s-%d", s.IssuePrefix, seq)
	if err := q.Exec(`INSERT INTO issues(id,sequence_no,identifier,title,description,acceptance_criteria_json,state,priority,created_by_type,created_by_run_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, seq, ident, title, strings.TrimSpace(in.Description), encodeJSON(ac), string(core.StateInbox), in.Priority, createdBy, in.CreatedByRunID, now, now); err != nil {
		return "", "", err
	}
	if err := q.Exec(`INSERT INTO issue_state_history(id,issue_id,from_state,to_state,actor_type,reason,created_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("hist_"), id, nil, string(core.StateInbox), createdBy, "created", now); err != nil {
		return "", "", err
	}
	for _, l := range labels {
		if err := q.Exec(`INSERT OR IGNORE INTO issue_labels(issue_id,label,created_at) VALUES(?,?,?)`, id, l, now); err != nil {
			return "", "", err
		}
	}
	if err := s.appendEventInTx(q, "issue.created", createdBy, &id, nil, map[string]any{"identifier": ident}); err != nil {
		return "", "", err
	}
	return ident, id, nil
}

func (s *Store) AppendEvent(eventType, actor string, issueID, runID *string, data map[string]any) error {
	return s.appendEventInTx(s.Project, eventType, actor, issueID, runID, data)
}
func (s *Store) AppendEventTx(eventType, actor string, issueID, runID *string, data map[string]any) error {
	return s.AppendEvent(eventType, actor, issueID, runID, data)
}

func (s *Store) appendEventInTx(q sqlRunner, eventType, actor string, issueID, runID *string, data map[string]any) error {
	return q.Exec(`INSERT INTO run_events(id,project_id,issue_id,run_id,event_type,actor_type,data_json,redacted,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, core.NewID("evt_"), s.ProjectID, issueID, runID, eventType, actor, encodeJSON(data), 1, core.Now())
}

func rowsChanged(q sqlRunner) (int, error) {
	row, err := q.QueryOne(`SELECT changes() AS n`)
	if err != nil {
		return 0, err
	}
	return row["n"].Int(), nil
}

func (s *Store) issueRowByRef(ref string) (map[string]db.Value, error) {
	return s.issueRowByRefTx(s.Project, ref)
}

func (s *Store) issueRowByRefTx(q sqlRunner, ref string) (map[string]db.Value, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "issue ref is required", nil)
	}
	row, err := q.QueryOne(`SELECT * FROM issues WHERE id=? OR identifier=?`, ref, strings.ToUpper(ref))
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.NewError(core.ErrNotFound, "issue not found", map[string]any{"issue_ref": ref})
	}
	return row, err
}

func (s *Store) GetIssue(ref string) (*core.Issue, error) {
	row, err := s.issueRowByRef(ref)
	if err != nil {
		return nil, err
	}
	return s.issueFromRow(row)
}

func (s *Store) ListIssues(opts ListIssueOptions) ([]*core.Issue, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > maxIssueListLimit {
		limit = maxIssueListLimit
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	where := []string{"archived_at IS NULL"}
	args := []any{}
	if len(opts.States) > 0 {
		qs := []string{}
		for _, st := range opts.States {
			if strings.TrimSpace(st) != "" {
				qs = append(qs, "?")
				args = append(args, st)
			}
		}
		if len(qs) > 0 {
			where = append(where, "state IN ("+strings.Join(qs, ",")+")")
		}
	}
	if opts.Query != "" {
		q := "%" + opts.Query + "%"
		where = append(where, "(identifier LIKE ? OR title LIKE ? OR description LIKE ?)")
		args = append(args, q, q, q)
	}
	if opts.DispatchPaused != nil {
		where = append(where, "dispatch_paused=?")
		if *opts.DispatchPaused {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	for _, label := range opts.Labels {
		label = strings.TrimSpace(label)
		if label == "" {
			continue
		}
		where = append(where, "EXISTS (SELECT 1 FROM issue_labels WHERE issue_labels.issue_id=issues.id AND issue_labels.label=?)")
		args = append(args, label)
	}
	order := "priority ASC, updated_at DESC, identifier ASC"
	switch opts.Sort {
	case "updated":
		order = "updated_at DESC, identifier ASC"
	case "identifier":
		order = "sequence_no ASC"
	}
	args = append(args, limit, offset)
	rows, err := s.Project.Query(`SELECT * FROM issues WHERE `+strings.Join(where, " AND ")+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	out := []*core.Issue{}
	for _, r := range rows {
		iss, err := s.issueFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, iss)
	}
	return out, nil
}

func (s *Store) issueFromRow(r map[string]db.Value) (*core.Issue, error) {
	id := r["id"].String()
	labels := []string{}
	lrows, err := s.Project.Query(`SELECT label FROM issue_labels WHERE issue_id=? ORDER BY label`, id)
	if err != nil {
		return nil, fmt.Errorf("load issue labels: %w", err)
	}
	for _, lr := range lrows {
		labels = append(labels, lr["label"].String())
	}
	ws, err := s.workspaceSummary(id)
	if err != nil {
		return nil, fmt.Errorf("load workspace summary: %w", err)
	}
	var git *core.GitSummary
	var branchName, workspacePath, baseRef, baseRefConfig, baseSHA *string
	if ws != nil {
		branchName = &ws.BranchName
		workspacePath = &ws.Path
		baseRef = &ws.BaseRef
		baseRefConfig = &ws.BaseRefConfig
		baseSHA = &ws.BaseSHA
		git = &core.GitSummary{BranchName: branchName, BaseRef: baseRef, BaseRefConfig: baseRefConfig, BaseSHA: baseSHA}
	} else {
		git = &core.GitSummary{}
	}
	latestRun, latestRunID, err := s.latestRunSummary(id)
	if err != nil {
		return nil, fmt.Errorf("load latest run: %w", err)
	}
	activeRunID, err := s.activeRunID(id)
	if err != nil {
		return nil, fmt.Errorf("load active run: %w", err)
	}
	latestRP, latestRPID, err := s.latestReviewSummary(id)
	if err != nil {
		return nil, fmt.Errorf("load latest review packet: %w", err)
	}
	blockedBy, err := s.relationRefs(id, "blocks", "source")
	if err != nil {
		return nil, fmt.Errorf("load blocked_by relations: %w", err)
	}
	blocks, err := s.relationRefs(id, "blocks", "target")
	if err != nil {
		return nil, fmt.Errorf("load blocks relations: %w", err)
	}
	duplicateOf, err := s.singleRelationRef(id, "duplicates")
	if err != nil {
		return nil, fmt.Errorf("load duplicate relation: %w", err)
	}
	duplicates, err := s.relationRefs(id, "duplicates", "target")
	if err != nil {
		return nil, fmt.Errorf("load duplicate references: %w", err)
	}
	followupOf, err := s.singleRelationRef(id, "followup_of")
	if err != nil {
		return nil, fmt.Errorf("load followup relation: %w", err)
	}
	followups, err := s.relationRefs(id, "followup_of", "target")
	if err != nil {
		return nil, fmt.Errorf("load followup references: %w", err)
	}
	return &core.Issue{
		ID: id, Identifier: r["identifier"].String(), SequenceNo: r["sequence_no"].Int(), Title: r["title"].String(), Description: r["description"].String(), AcceptanceCriteria: decodeStringSlice(r["acceptance_criteria_json"].String()), Priority: r["priority"].Int(), State: core.IssueState(r["state"].String()), URL: nil, Labels: labels,
		BlockedBy: blockedBy, Blocks: blocks, DuplicateOf: duplicateOf, Duplicates: duplicates, FollowupOf: followupOf, Followups: followups,
		DispatchPaused: r["dispatch_paused"].Bool(), DispatchPauseReason: ptrFromVal(r["dispatch_pause_reason"]), DispatchPausedAt: ptrFromVal(r["dispatch_paused_at"]), BranchName: branchName, WorkspacePath: workspacePath, BaseRef: baseRef, BaseRefConfig: baseRefConfig, BaseSHA: baseSHA, Workspace: ws, Git: git, LatestRun: latestRun, ActiveRunID: activeRunID, LatestRunID: latestRunID, LatestReviewPacket: latestRP, LatestReviewPacketID: latestRPID, CreatedAt: r["created_at"].String(), UpdatedAt: r["updated_at"].String(), CompletedAt: ptrFromVal(r["completed_at"]), ArchivedAt: ptrFromVal(r["archived_at"]),
	}, nil
}

func (s *Store) issueRefFromID(id string) (core.IssueRef, error) {
	row, err := s.Project.QueryOne(`SELECT id,identifier,title,state FROM issues WHERE id=?`, id)
	if err != nil {
		return core.IssueRef{}, err
	}
	return core.IssueRef{ID: row["id"].String(), Identifier: row["identifier"].String(), Title: row["title"].String(), State: core.IssueState(row["state"].String())}, nil
}
func (s *Store) relationRefs(id, typ, mode string) ([]core.IssueRef, error) {
	var rows []map[string]db.Value
	var err error
	if mode == "source" {
		rows, err = s.Project.Query(`SELECT target_issue_id AS id FROM issue_relations WHERE source_issue_id=? AND relation_type=? AND active=1 ORDER BY created_at`, id, typ)
	} else {
		rows, err = s.Project.Query(`SELECT source_issue_id AS id FROM issue_relations WHERE target_issue_id=? AND relation_type=? AND active=1 ORDER BY created_at`, id, typ)
	}
	if err != nil {
		return nil, err
	}
	out := []core.IssueRef{}
	for _, r := range rows {
		ref, err := s.issueRefFromID(r["id"].String())
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		out = append(out, ref)
	}
	return out, nil
}
func (s *Store) singleRelationRef(id, typ string) (*core.IssueRef, error) {
	rows, err := s.Project.Query(`SELECT target_issue_id AS id FROM issue_relations WHERE source_issue_id=? AND relation_type=? AND active=1 ORDER BY created_at DESC LIMIT 1`, id, typ)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	ref, err := s.issueRefFromID(rows[0]["id"].String())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &ref, nil
}
func (s *Store) workspaceSummary(issueID string) (*core.WorkspaceSummary, error) {
	row, err := s.Project.QueryOne(`SELECT * FROM workspaces WHERE issue_id=?`, issueID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &core.WorkspaceSummary{ID: row["id"].String(), Path: row["path"].String(), BranchName: row["branch_name"].String(), BaseRef: row["base_ref"].String(), BaseRefConfig: row["base_ref_config"].String(), BaseSHA: row["base_sha"].String(), Status: row["status"].String()}, nil
}
func (s *Store) latestRunSummary(issueID string) (*core.RunSummary, *string, error) {
	row, err := s.Project.QueryOne(`SELECT * FROM run_attempts WHERE issue_id=? ORDER BY attempt_no DESC LIMIT 1`, issueID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	fc := failPtrFromVal(row["failure_code"])
	sum := &core.RunSummary{ID: row["id"].String(), Status: core.RunStatus(row["status"].String()), AttemptNo: row["attempt_no"].Int(), FailureCode: fc, CreatedAt: row["created_at"].String()}
	id := sum.ID
	return sum, &id, nil
}
func (s *Store) activeRunID(issueID string) (*string, error) {
	return s.activeRunIDTx(s.Project, issueID)
}

func (s *Store) activeRunIDTx(q sqlRunner, issueID string) (*string, error) {
	rows, err := q.Query(`SELECT id FROM run_attempts WHERE issue_id=? AND status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running') ORDER BY created_at DESC LIMIT 1`, issueID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	id := rows[0]["id"].String()
	return &id, nil
}
func (s *Store) latestReviewSummary(issueID string) (*core.ReviewPacketSummary, *string, error) {
	row, err := s.Project.QueryOne(`SELECT * FROM review_packets WHERE issue_id=? ORDER BY packet_no DESC LIMIT 1`, issueID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	fc := failPtrFromVal(row["failure_code"])
	fm := ptrFromVal(row["failure_message"])
	sum := &core.ReviewPacketSummary{ID: row["id"].String(), RunID: row["run_id"].String(), PacketNo: row["packet_no"].Int(), Status: row["status"].String(), CreatedAt: row["created_at"].String(), FailureCode: fc, FailureMessage: fm}
	id := sum.ID
	return sum, &id, nil
}

func (s *Store) UpdateIssue(ref string, fields map[string]any) (*core.Issue, error) {
	row, err := s.issueRowByRef(ref)
	if err != nil {
		return nil, err
	}
	id := row["id"].String()
	now := core.Now()
	title, hasTitle := fields["title"].(string)
	if hasTitle {
		title = strings.TrimSpace(title)
		if title == "" {
			return nil, core.NewError(core.ErrInvalidRequest, "title is required", nil)
		}
	}
	desc, hasDesc := fields["description"].(string)
	ac, hasAC := fields["acceptance_criteria"].([]string)
	p, hasPriority := fields["priority"].(int)
	if hasPriority && (p < 1 || p > 5) {
		return nil, core.NewError(core.ErrInvalidRequest, "priority must be between 1 and 5", nil)
	}
	labels, hasLabels := fields["labels"].([]string)

	if err := s.Project.WithTx(func(tx *db.Tx) error {
		if hasTitle {
			if err := tx.Exec(`UPDATE issues SET title=?, updated_at=? WHERE id=?`, title, now, id); err != nil {
				return err
			}
		}
		if hasDesc {
			if err := tx.Exec(`UPDATE issues SET description=?, updated_at=? WHERE id=?`, strings.TrimSpace(desc), now, id); err != nil {
				return err
			}
		}
		if hasAC {
			if err := tx.Exec(`UPDATE issues SET acceptance_criteria_json=?, updated_at=? WHERE id=?`, encodeJSON(trimSlice(ac)), now, id); err != nil {
				return err
			}
		}
		if hasPriority {
			if err := tx.Exec(`UPDATE issues SET priority=?, updated_at=? WHERE id=?`, p, now, id); err != nil {
				return err
			}
		}
		if hasLabels {
			if err := tx.Exec(`DELETE FROM issue_labels WHERE issue_id=?`, id); err != nil {
				return err
			}
			for _, l := range normalizeLabels(labels) {
				if err := tx.Exec(`INSERT OR IGNORE INTO issue_labels(issue_id,label,created_at) VALUES(?,?,?)`, id, l, now); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}

func validRequired(row map[string]db.Value) bool {
	if strings.TrimSpace(row["title"].String()) == "" || strings.TrimSpace(row["description"].String()) == "" {
		return false
	}
	if row["priority"].Int() < 1 || row["priority"].Int() > 5 {
		return false
	}
	for _, ac := range decodeStringSlice(row["acceptance_criteria_json"].String()) {
		if strings.TrimSpace(ac) != "" {
			return true
		}
	}
	return false
}

func validIssueState(state core.IssueState) bool {
	switch state {
	case core.StateInbox, core.StateReady, core.StateWorking, core.StateRework, core.StateBlocked, core.StateHumanReview, core.StateDone, core.StateCancelled, core.StateDuplicate:
		return true
	default:
		return false
	}
}

func (s *Store) TransitionIssue(ref string, target core.IssueState, reason, duplicateOf string) (*core.Issue, error) {
	reason = strings.TrimSpace(reason)
	if target == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "state is required", nil)
	}
	if !validIssueState(target) {
		return nil, core.NewError(core.ErrInvalidRequest, "invalid issue state", nil)
	}
	if target == core.StateHumanReview || target == core.StateRework || target == core.StateDone || target == core.StateWorking {
		return nil, core.NewError(core.ErrInvalidStateTransition, "use dispatch or review API for this transition", nil)
	}
	if (target == core.StateBlocked || target == core.StateCancelled || target == core.StateDuplicate) && reason == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "reason is required", nil)
	}
	var id string
	now := core.Now()
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		row, err := s.issueRowByRefTx(tx, ref)
		if err != nil {
			return err
		}
		id = row["id"].String()
		from := core.IssueState(row["state"].String())
		expectedState := from
		if from == target && target != core.StateDuplicate {
			return core.NewError(core.ErrInvalidStateTransition, "same-state transition is not allowed", nil)
		}
		if target == core.StateReady && !validRequired(row) {
			return core.NewError(core.ErrInvalidRequest, "issue required fields are incomplete", nil)
		}
		if from == core.StateBlocked && target != core.StateReady {
			return core.NewError(core.ErrInvalidStateTransition, "blocked issues may resolve to Ready", nil)
		}
		if core.IsTerminalIssueState(from) && !(target == core.StateInbox || target == core.StateReady) {
			return core.NewError(core.ErrInvalidStateTransition, "terminal issues can reopen only to Inbox or Ready", nil)
		}
		active, err := s.activeRunIDTx(tx, id)
		if err != nil {
			return err
		}
		if active != nil {
			if err := s.cancelRunInTx(tx, *active, core.FailureIssueStateChanged, "issue state changed", true); err != nil {
				return err
			}
			current, err := s.issueRowByRefTx(tx, id)
			if err != nil {
				return err
			}
			expectedState = core.IssueState(current["state"].String())
		}
		if target == core.StateDuplicate && duplicateOf != "" {
			can, err := s.issueRowByRefTx(tx, duplicateOf)
			if err != nil {
				return err
			}
			if can["id"].String() == id {
				return core.NewError(core.ErrInvalidRequest, "duplicate_of cannot point to current issue", nil)
			}
			existing, err := tx.Query(`SELECT * FROM issue_relations WHERE source_issue_id=? AND relation_type='duplicates' AND active=1`, id)
			if err != nil {
				return err
			}
			if len(existing) > 0 && existing[0]["target_issue_id"].String() != can["id"].String() {
				return core.NewError(core.ErrInvalidStateTransition, "duplicate relation already points elsewhere", nil)
			}
			if len(existing) == 0 {
				if err := tx.Exec(`INSERT OR IGNORE INTO issue_relations(id,source_issue_id,target_issue_id,relation_type,active,created_by_type,created_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("rel_"), id, can["id"].String(), "duplicates", 1, "operator", now); err != nil {
					return err
				}
			}
		}
		if core.IsTerminalIssueState(from) && (target == core.StateInbox || target == core.StateReady) {
			if err := tx.Exec(`UPDATE issues SET completed_at=NULL, dispatch_paused=0, dispatch_pause_reason=NULL, dispatch_paused_at=NULL WHERE id=?`, id); err != nil {
				return err
			}
		}
		if err := tx.Exec(`UPDATE issues SET state=?, updated_at=? WHERE id=? AND state=?`, string(target), now, id, string(expectedState)); err != nil {
			return err
		}
		changed, err := rowsChanged(tx)
		if err != nil {
			return err
		}
		if changed != 1 {
			return core.NewError(core.ErrInvalidStateTransition, "issue state changed during transition", nil)
		}
		if err := tx.Exec(`INSERT INTO issue_state_history(id,issue_id,from_state,to_state,actor_type,reason,created_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("hist_"), id, string(from), string(target), "operator", reason, now); err != nil {
			return err
		}
		if reason != "" {
			if err := tx.Exec(`INSERT INTO issue_comments(id,issue_id,author_type,body,created_at) VALUES(?,?,?,?,?)`, core.NewID("com_"), id, "operator", reason, now); err != nil {
				return err
			}
		}
		return s.appendEventInTx(tx, "issue.transitioned", "operator", &id, nil, map[string]any{"from_state": from, "to_state": target})
	}); err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}

func (s *Store) AddComment(ref, author, body string, runID *string) error {
	row, err := s.issueRowByRef(ref)
	if err != nil {
		return err
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return core.NewError(core.ErrInvalidRequest, "comment body is required", nil)
	}
	id := row["id"].String()
	now := core.Now()
	if author == "" {
		author = "operator"
	}
	if err := s.Project.Exec(`INSERT INTO issue_comments(id,issue_id,run_id,author_type,body,created_at) VALUES(?,?,?,?,?,?)`, core.NewID("com_"), id, runID, author, body, now); err != nil {
		return err
	}
	return s.AppendEvent("issue.comment", author, &id, runID, map[string]any{"body": body})
}

func (s *Store) AddBlocker(ref, blocker string) (*core.Issue, error) {
	row, err := s.issueRowByRef(ref)
	if err != nil {
		return nil, err
	}
	brow, err := s.issueRowByRef(blocker)
	if err != nil {
		return nil, err
	}
	if row["id"].String() == brow["id"].String() {
		return nil, core.NewError(core.ErrInvalidRequest, "issue cannot block itself", nil)
	}
	now := core.Now()
	if err := s.Project.Exec(`INSERT OR IGNORE INTO issue_relations(id,source_issue_id,target_issue_id,relation_type,active,created_by_type,created_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("rel_"), row["id"].String(), brow["id"].String(), "blocks", 1, "operator", now); err != nil {
		return nil, err
	}
	return s.GetIssue(ref)
}
func (s *Store) RemoveBlocker(ref, blocker string) (*core.Issue, error) {
	row, err := s.issueRowByRef(ref)
	if err != nil {
		return nil, err
	}
	brow, err := s.issueRowByRef(blocker)
	if err != nil {
		return nil, err
	}
	now := core.Now()
	if err := s.Project.Exec(`UPDATE issue_relations SET active=0,resolved_at=? WHERE source_issue_id=? AND target_issue_id=? AND relation_type='blocks' AND active=1`, now, row["id"].String(), brow["id"].String()); err != nil {
		return nil, err
	}
	return s.GetIssue(ref)
}
func (s *Store) RemoveDuplicate(ref, canonical string) (*core.Issue, error) {
	row, err := s.issueRowByRef(ref)
	if err != nil {
		return nil, err
	}
	can, err := s.issueRowByRef(canonical)
	if err != nil {
		return nil, err
	}
	now := core.Now()
	if err := s.Project.Exec(`UPDATE issue_relations SET active=0,resolved_at=? WHERE source_issue_id=? AND target_issue_id=? AND relation_type='duplicates' AND active=1`, now, row["id"].String(), can["id"].String()); err != nil {
		return nil, err
	}
	return s.GetIssue(ref)
}

func (s *Store) DispatchPause(ref, reason string) (*core.Issue, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "reason is required", nil)
	}
	var id string
	now := core.Now()
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		row, err := s.issueRowByRefTx(tx, ref)
		if err != nil {
			return err
		}
		id = row["id"].String()
		if core.IsTerminalIssueState(core.IssueState(row["state"].String())) {
			return core.NewError(core.ErrInvalidStateTransition, "terminal issue dispatch state cannot be changed", nil)
		}
		active, err := s.activeRunIDTx(tx, id)
		if err != nil {
			return err
		}
		if active != nil {
			return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
		}
		if err := tx.Exec(`UPDATE issues SET dispatch_paused=1, dispatch_pause_reason=?, dispatch_paused_at=?, updated_at=? WHERE id=?`, reason, now, now, id); err != nil {
			return err
		}
		active, err = s.activeRunIDTx(tx, id)
		if err != nil {
			return err
		}
		if active != nil {
			return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
		}
		if err := tx.Exec(`INSERT INTO issue_comments(id,issue_id,author_type,body,created_at) VALUES(?,?,?,?,?)`, core.NewID("com_"), id, "operator", reason, now); err != nil {
			return err
		}
		return s.appendEventInTx(tx, "issue.dispatch_paused", "operator", &id, nil, map[string]any{"reason": reason})
	}); err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}
func (s *Store) DispatchResume(ref, reason string) (*core.Issue, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "reason is required", nil)
	}
	var id string
	now := core.Now()
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		row, err := s.issueRowByRefTx(tx, ref)
		if err != nil {
			return err
		}
		id = row["id"].String()
		if core.IsTerminalIssueState(core.IssueState(row["state"].String())) {
			return core.NewError(core.ErrInvalidStateTransition, "terminal issue dispatch state cannot be changed", nil)
		}
		active, err := s.activeRunIDTx(tx, id)
		if err != nil {
			return err
		}
		if active != nil {
			return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
		}
		if err := tx.Exec(`UPDATE issues SET dispatch_paused=0, dispatch_pause_reason=NULL, dispatch_paused_at=NULL, updated_at=? WHERE id=?`, now, id); err != nil {
			return err
		}
		active, err = s.activeRunIDTx(tx, id)
		if err != nil {
			return err
		}
		if active != nil {
			return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
		}
		if err := tx.Exec(`INSERT INTO issue_comments(id,issue_id,author_type,body,created_at) VALUES(?,?,?,?,?)`, core.NewID("com_"), id, "operator", reason, now); err != nil {
			return err
		}
		return s.appendEventInTx(tx, "issue.dispatch_resumed", "operator", &id, nil, map[string]any{"reason": reason})
	}); err != nil {
		return nil, err
	}
	return s.GetIssue(id)
}

func (s *Store) ClaimRun(issueRef, dispatchReason, runnerKind string, maxConcurrent int) (*core.RunAttempt, error) {
	if dispatchReason == "" {
		dispatchReason = "manual"
	}
	if runnerKind == "" {
		runnerKind = "fake"
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	var claimed *core.RunAttempt
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		row, err := s.issueRowByRefTx(tx, issueRef)
		if err != nil {
			return err
		}
		id := row["id"].String()
		st := core.IssueState(row["state"].String())
		if !core.IsDispatchState(st) {
			return core.NewError(core.ErrInvalidStateTransition, "issue is not Ready/Rework", nil)
		}
		if !validRequired(row) {
			return core.NewError(core.ErrInvalidRequest, "issue required fields are incomplete", nil)
		}
		if row["dispatch_paused"].Bool() {
			return core.NewError(core.ErrIssueDispatchPaused, "issue dispatch is paused", nil)
		}
		blockers, err := tx.Query(`SELECT id FROM issue_relations WHERE source_issue_id=? AND relation_type='blocks' AND active=1 LIMIT 1`, id)
		if err != nil {
			return err
		}
		if len(blockers) > 0 {
			return core.NewError(core.ErrIssueBlocked, "issue has active blockers", nil)
		}
		active, err := s.activeRunIDTx(tx, id)
		if err != nil {
			return err
		}
		if active != nil {
			return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
		}
		activeRows, err := tx.Query(`SELECT count(*) AS c FROM run_attempts WHERE status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running')`)
		if err != nil {
			return err
		}
		if len(activeRows) > 0 && activeRows[0]["c"].Int() >= maxConcurrent {
			return core.NewError(core.ErrConcurrencyLimitReached, "concurrency limit reached", nil)
		}
		ar, err := tx.QueryOne(`SELECT COALESCE(MAX(attempt_no),0)+1 AS n FROM run_attempts WHERE issue_id=?`, id)
		if err != nil {
			return err
		}
		attempt := ar["n"].Int()
		now := core.Now()
		runID := core.NewID("run_")
		if err := tx.Exec(`INSERT INTO run_attempts(id,issue_id,attempt_no,status,dispatch_reason,source_issue_state,runner_kind,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, runID, id, attempt, string(core.RunPending), dispatchReason, string(st), runnerKind, now, now); err != nil {
			return err
		}
		claimed = &core.RunAttempt{
			ID:               runID,
			IssueID:          id,
			IssueIdentifier:  row["identifier"].String(),
			AttemptNo:        attempt,
			Status:           core.RunPending,
			DispatchReason:   dispatchReason,
			SourceIssueState: st,
			RunnerKind:       runnerKind,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		if err := tx.Exec(`UPDATE issues SET state=?, latest_run_id=?, updated_at=? WHERE id=?`, string(core.StateWorking), runID, now, id); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO issue_state_history(id,issue_id,from_state,to_state,actor_type,run_id,reason,created_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("hist_"), id, string(st), string(core.StateWorking), "orchestrator", runID, "dispatch", now); err != nil {
			return err
		}
		if err := s.appendEventInTx(tx, "run.claimed", "system", &id, &runID, map[string]any{"source_issue_state": st}); err != nil {
			return err
		}
		if err := s.appendEventInTx(tx, "issue.state_changed", "system", &id, &runID, map[string]any{"from_state": st, "to_state": core.StateWorking}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return claimed, nil
}

func (s *Store) GetRun(runID string) (*core.RunAttempt, error) {
	return s.getRunTx(s.Project, runID)
}

func (s *Store) getRunTx(q sqlRunner, runID string) (*core.RunAttempt, error) {
	row, err := q.QueryOne(`SELECT r.*, i.identifier AS issue_identifier FROM run_attempts r JOIN issues i ON i.id=r.issue_id WHERE r.id=?`, runID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.NewError(core.ErrNotFound, "run not found", nil)
	}
	if err != nil {
		return nil, err
	}
	return runFromRow(row), nil
}
func runFromRow(row map[string]db.Value) *core.RunAttempt {
	return &core.RunAttempt{ID: row["id"].String(), IssueID: row["issue_id"].String(), IssueIdentifier: row["issue_identifier"].String(), AttemptNo: row["attempt_no"].Int(), WorkspaceID: ptrFromVal(row["workspace_id"]), WorkflowSnapshotID: ptrFromVal(row["workflow_snapshot_id"]), Status: core.RunStatus(row["status"].String()), DispatchReason: row["dispatch_reason"].String(), SourceIssueState: core.IssueState(row["source_issue_state"].String()), RunnerKind: row["runner_kind"].String(), BaseRefConfig: ptrFromVal(row["base_ref_config"]), BaseRef: ptrFromVal(row["base_ref"]), BaseSHA: ptrFromVal(row["base_sha"]), BranchName: ptrFromVal(row["branch_name"]), FailureCode: failPtrFromVal(row["failure_code"]), FailureMessage: ptrFromVal(row["failure_message"]), StartedAt: ptrFromVal(row["started_at"]), EndedAt: ptrFromVal(row["ended_at"]), CreatedAt: row["created_at"].String(), UpdatedAt: row["updated_at"].String()}
}
func (s *Store) ListRuns() ([]*core.RunAttempt, error) {
	rows, err := s.Project.Query(`SELECT r.*, i.identifier AS issue_identifier FROM run_attempts r JOIN issues i ON i.id=r.issue_id ORDER BY r.created_at DESC`)
	if err != nil {
		return nil, err
	}
	out := []*core.RunAttempt{}
	for _, r := range rows {
		out = append(out, runFromRow(r))
	}
	return out, nil
}
func (s *Store) UpdateRunStatus(runID string, status core.RunStatus, fields map[string]any) error {
	now := core.Now()
	return s.Project.WithTx(func(tx *db.Tx) error {
		run, err := s.getRunTx(tx, runID)
		if err != nil {
			return err
		}
		if !core.IsActiveRunStatus(run.Status) {
			return core.NewError(core.ErrInvalidStateTransition, "run is not active", map[string]any{"run_id": runID, "status": run.Status})
		}
		if !core.IsActiveRunStatus(status) {
			return core.NewError(core.ErrInvalidStateTransition, "use lifecycle-specific APIs for terminal run status", map[string]any{"run_id": runID, "target_status": status})
		}
		set := []string{"status=?", "updated_at=?"}
		args := []any{string(status), now}
		for k, v := range fields {
			field, err := validateUpdateRunStatusField(k)
			if err != nil {
				return err
			}
			set = append(set, field+"=?")
			args = append(args, v)
		}
		args = append(args, runID)
		return tx.Exec(`UPDATE run_attempts SET `+strings.Join(set, ",")+` WHERE id=?`, args...)
	})
}

var updateRunStatusManagedFields = map[string]struct{}{
	"id":                   {},
	"issue_id":             {},
	"attempt_no":           {},
	"status":               {},
	"workspace_id":         {},
	"workflow_snapshot_id": {},
	"dispatch_reason":      {},
	"source_issue_state":   {},
	"runner_kind":          {},
	"base_ref_config":      {},
	"base_ref":             {},
	"base_sha":             {},
	"branch_name":          {},
	"failure_code":         {},
	"failure_message":      {},
	"ended_at":             {},
	"created_at":           {},
	"updated_at":           {},
}

func validateUpdateRunStatusField(field string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(field))
	if normalized == "" {
		return "", core.NewError(core.ErrInvalidRequest, "run status field is required", nil)
	}
	if _, ok := updateRunStatusManagedFields[normalized]; ok {
		return "", core.NewError(core.ErrInvalidRequest, "run status field is managed by lifecycle APIs", map[string]any{"field": normalized})
	}
	for _, r := range normalized {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return "", core.NewError(core.ErrInvalidRequest, "invalid run status field", map[string]any{"field": normalized})
		}
	}
	return normalized, nil
}

func (s *Store) SetRunWorkspace(runID, workspaceID, branch, baseRefConfig, baseRef, baseSHA string) error {
	return s.Project.Exec(`UPDATE run_attempts SET workspace_id=?, branch_name=?, base_ref_config=?, base_ref=?, base_sha=?, updated_at=? WHERE id=?`, workspaceID, branch, baseRefConfig, baseRef, baseSHA, core.Now(), runID)
}
func (s *Store) CreateOrUpdateWorkspace(issueID, path, branch, baseRefConfig, baseRef, baseSHA string) (string, error) {
	row, err := s.Project.QueryOne(`SELECT id FROM workspaces WHERE issue_id=?`, issueID)
	if err == nil {
		id := row["id"].String()
		return id, s.Project.Exec(`UPDATE workspaces SET path=?, branch_name=?, base_ref_config=?, base_ref=?, base_sha=?, status='prepared', updated_at=? WHERE id=?`, path, branch, baseRefConfig, baseRef, baseSHA, core.Now(), id)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	id := core.NewID("ws_")
	now := core.Now()
	return id, s.Project.Exec(`INSERT INTO workspaces(id,issue_id,path,branch_name,base_ref_config,base_ref,base_sha,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, issueID, path, branch, baseRefConfig, baseRef, baseSHA, "prepared", now, now)
}

func (s *Store) CompleteRunWithReview(runID, reviewPacketID string) error {
	run, err := s.GetRun(runID)
	if err != nil {
		return err
	}
	now := core.Now()
	return s.Project.WithTx(func(tx *db.Tx) error {
		run, err = s.getRunTx(tx, runID)
		if err != nil {
			return err
		}
		if !core.IsActiveRunStatus(run.Status) {
			return core.NewError(core.ErrInvalidStateTransition, "run is not active", map[string]any{"run_id": runID, "status": run.Status})
		}
		packet, err := tx.QueryOne(`SELECT issue_id, run_id, status FROM review_packets WHERE id=?`, reviewPacketID)
		if errors.Is(err, os.ErrNotExist) {
			return core.NewError(core.ErrReviewPacketRequired, "review packet required", map[string]any{"review_packet_id": reviewPacketID})
		}
		if err != nil {
			return err
		}
		if packet["issue_id"].String() != run.IssueID || packet["run_id"].String() != runID {
			return core.NewError(core.ErrInvalidRequest, "review packet does not belong to run", map[string]any{"run_id": runID, "review_packet_id": reviewPacketID})
		}
		if packet["status"].String() != "generated" {
			return core.NewError(core.ErrInvalidStateTransition, "review packet is not generated", map[string]any{"review_packet_id": reviewPacketID, "status": packet["status"].String()})
		}
		if err := tx.Exec(`UPDATE run_attempts SET status=?, failure_code=NULL, failure_message=NULL, ended_at=?, updated_at=? WHERE id=?`, string(core.RunCompleted), now, now, runID); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE issues SET state=?, dispatch_paused=0, dispatch_pause_reason=NULL, dispatch_paused_at=NULL, latest_review_packet_id=?, updated_at=? WHERE id=?`, string(core.StateHumanReview), reviewPacketID, now, run.IssueID); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO issue_state_history(id,issue_id,from_state,to_state,actor_type,run_id,reason,created_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("hist_"), run.IssueID, string(core.StateWorking), string(core.StateHumanReview), "orchestrator", runID, "review packet generated", now); err != nil {
			return err
		}
		if err := s.appendEventInTx(tx, "review.packet_generated", "system", &run.IssueID, &runID, map[string]any{"review_packet_id": reviewPacketID}); err != nil {
			return err
		}
		return nil
	})
}
func (s *Store) FailRun(runID string, code core.FailureCode, message string, status core.RunStatus) error {
	run, err := s.GetRun(runID)
	if err != nil {
		return err
	}
	if status == "" {
		status = core.RunFailed
	}
	now := core.Now()
	return s.Project.WithTx(func(tx *db.Tx) error {
		run, err = s.getRunTx(tx, runID)
		if err != nil {
			return err
		}
		if !core.IsActiveRunStatus(run.Status) {
			return core.NewError(core.ErrInvalidStateTransition, "run is not active", map[string]any{"run_id": runID, "status": run.Status})
		}
		if err := tx.Exec(`UPDATE run_attempts SET status=?, failure_code=?, failure_message=?, ended_at=?, updated_at=? WHERE id=?`, string(status), string(code), message, now, now, runID); err != nil {
			return err
		}
		row, err := tx.QueryOne(`SELECT state FROM issues WHERE id=?`, run.IssueID)
		if err != nil {
			return err
		}
		cur := core.IssueState(row["state"].String())
		target := cur
		if !core.IsTerminalIssueState(cur) && cur != core.StateBlocked {
			target = run.SourceIssueState
		}
		if err := tx.Exec(`UPDATE issues SET state=?, dispatch_paused=1, dispatch_pause_reason=?, dispatch_paused_at=?, updated_at=? WHERE id=?`, string(target), string(code), now, now, run.IssueID); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO issue_comments(id,issue_id,run_id,author_type,body,created_at) VALUES(?,?,?,?,?,?)`, core.NewID("com_"), run.IssueID, runID, "system", fmt.Sprintf("Run ended with %s: %s", code, message), now); err != nil {
			return err
		}
		if err := s.appendEventInTx(tx, "run.failed", "system", &run.IssueID, &runID, map[string]any{"failure_code": code, "message": message}); err != nil {
			return err
		}
		return s.appendEventInTx(tx, "scheduler.paused", "system", &run.IssueID, &runID, map[string]any{"reason": code})
	})
}

func (s *Store) BlockRunByAgent(runID, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return core.NewError(core.ErrInvalidRequest, "reason is required", nil)
	}
	run, err := s.GetRun(runID)
	if err != nil {
		return err
	}
	now := core.Now()
	return s.Project.WithTx(func(tx *db.Tx) error {
		run, err = s.getRunTx(tx, runID)
		if err != nil {
			return err
		}
		if !core.IsActiveRunStatus(run.Status) {
			return core.NewError(core.ErrInvalidStateTransition, "run is not active", map[string]any{"run_id": runID, "status": run.Status})
		}
		row, err := tx.QueryOne(`SELECT state FROM issues WHERE id=?`, run.IssueID)
		if err != nil {
			return err
		}
		from := core.IssueState(row["state"].String())
		if err := tx.Exec(`INSERT INTO issue_comments(id,issue_id,run_id,author_type,body,created_at) VALUES(?,?,?,?,?,?)`, core.NewID("com_"), run.IssueID, runID, "agent", "Blocked by agent: "+reason, now); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE run_attempts SET status=?, failure_code=?, failure_message=?, ended_at=?, updated_at=? WHERE id=?`, string(core.RunCancelled), string(core.FailureAgentBlocked), reason, now, now, runID); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE run_tool_tokens SET revoked_at=? WHERE run_id=? AND revoked_at IS NULL`, now, runID); err != nil {
			return err
		}
		if err := tx.Exec(`UPDATE issues SET state=?, dispatch_paused=1, dispatch_pause_reason=?, dispatch_paused_at=?, updated_at=? WHERE id=?`, string(core.StateBlocked), string(core.FailureAgentBlocked), now, now, run.IssueID); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO issue_state_history(id,issue_id,from_state,to_state,actor_type,run_id,reason,created_at) VALUES(?,?,?,?,?,?,?,?)`, core.NewID("hist_"), run.IssueID, string(from), string(core.StateBlocked), "agent", runID, reason, now); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO issue_comments(id,issue_id,run_id,author_type,body,created_at) VALUES(?,?,?,?,?,?)`, core.NewID("com_"), run.IssueID, runID, "system", fmt.Sprintf("Run ended with %s: %s", core.FailureAgentBlocked, reason), now); err != nil {
			return err
		}
		if err := s.appendEventInTx(tx, "run.failed", "system", &run.IssueID, &runID, map[string]any{"failure_code": core.FailureAgentBlocked, "message": reason}); err != nil {
			return err
		}
		if err := s.appendEventInTx(tx, "scheduler.paused", "system", &run.IssueID, &runID, map[string]any{"reason": core.FailureAgentBlocked}); err != nil {
			return err
		}
		return s.appendEventInTx(tx, "issue.transitioned", "agent", &run.IssueID, &runID, map[string]any{"from_state": from, "to_state": core.StateBlocked})
	})
}

func (s *Store) CancelRun(runID, reason string) error {
	return s.cancelRun(runID, core.FailureOperatorCancelled, reason)
}
func (s *Store) cancelRun(runID string, code core.FailureCode, reason string) error {
	return s.Project.WithTx(func(tx *db.Tx) error {
		return s.cancelRunInTx(tx, runID, code, reason, true)
	})
}
func (s *Store) cancelRunInTx(q sqlRunner, runID string, code core.FailureCode, reason string, restore bool) error {
	run, err := s.getRunTx(q, runID)
	if err != nil {
		return err
	}
	if !core.IsActiveRunStatus(run.Status) {
		return core.NewError(core.ErrInvalidStateTransition, "run is not active", map[string]any{"run_id": runID, "status": run.Status})
	}
	now := core.Now()
	if err := q.Exec(`UPDATE run_attempts SET status=?, failure_code=?, failure_message=?, ended_at=?, updated_at=? WHERE id=?`, string(core.RunCancelled), string(code), reason, now, now, runID); err != nil {
		return err
	}
	target := run.SourceIssueState
	row, err := q.QueryOne(`SELECT state FROM issues WHERE id=?`, run.IssueID)
	if err != nil {
		return err
	}
	cur := core.IssueState(row["state"].String())
	if !restore || cur == core.StateBlocked || core.IsTerminalIssueState(cur) {
		target = cur
	}
	if err := q.Exec(`UPDATE issues SET state=?, dispatch_paused=1, dispatch_pause_reason=?, dispatch_paused_at=?, updated_at=? WHERE id=?`, string(target), string(code), now, now, run.IssueID); err != nil {
		return err
	}
	if err := q.Exec(`UPDATE run_tool_tokens SET revoked_at=? WHERE run_id=? AND revoked_at IS NULL`, now, runID); err != nil {
		return err
	}
	if err := s.appendEventInTx(q, "run.cancelled", "system", &run.IssueID, &runID, map[string]any{"failure_code": code, "reason": reason}); err != nil {
		return err
	}
	return nil
}

func (s *Store) RunEvents(runID string, afterSeq int64, limit int) ([]core.RunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.Project.Query(`SELECT * FROM run_events WHERE (?='' OR run_id=?) AND seq>? ORDER BY seq ASC LIMIT ?`, runID, runID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := []core.RunEvent{}
	for _, r := range rows {
		var data map[string]any
		_ = json.Unmarshal([]byte(r["data_json"].String()), &data)
		out = append(out, core.RunEvent{Seq: r["seq"].Int64(), ID: r["id"].String(), ProjectID: r["project_id"].String(), IssueID: ptrFromVal(r["issue_id"]), RunID: ptrFromVal(r["run_id"]), EventType: r["event_type"].String(), ActorType: r["actor_type"].String(), Data: data, Redacted: r["redacted"].Bool(), CreatedAt: r["created_at"].String()})
	}
	return out, nil
}

func (s *Store) IssueEvents(issueID string, afterSeq int64, limit int) ([]core.RunEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.Project.Query(`SELECT * FROM run_events WHERE issue_id=? AND seq>? ORDER BY seq ASC LIMIT ?`, issueID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	out := []core.RunEvent{}
	for _, r := range rows {
		var data map[string]any
		_ = json.Unmarshal([]byte(r["data_json"].String()), &data)
		out = append(out, core.RunEvent{Seq: r["seq"].Int64(), ID: r["id"].String(), ProjectID: r["project_id"].String(), IssueID: ptrFromVal(r["issue_id"]), RunID: ptrFromVal(r["run_id"]), EventType: r["event_type"].String(), ActorType: r["actor_type"].String(), Data: data, Redacted: r["redacted"].Bool(), CreatedAt: r["created_at"].String()})
	}
	return out, nil
}

func (s *Store) CreateWorkflowSnapshot(status, sourcePath, configJSON, promptHash string, errorsJSON string) (string, error) {
	id := core.NewID("wf_")
	if errorsJSON == "" {
		errorsJSON = "[]"
	}
	return id, s.Project.Exec(`INSERT INTO workflow_snapshots(id,status,source_path,config_json,prompt_body_sha256,validation_errors_json,created_at) VALUES(?,?,?,?,?,?,?)`, id, status, sourcePath, configJSON, promptHash, errorsJSON, core.Now())
}
func (s *Store) AttachWorkflowSnapshot(runID, wfID string) error {
	return s.Project.Exec(`UPDATE run_attempts SET workflow_snapshot_id=?, updated_at=? WHERE id=?`, wfID, core.Now(), runID)
}
func (s *Store) CreatePromptSnapshot(runID, wfID, ctxHash, promptHash, rootPath string) (string, error) {
	return s.CreatePromptSnapshotTx(s.Project, runID, wfID, ctxHash, promptHash, rootPath)
}

func (s *Store) CreatePromptSnapshotTx(q TxRunner, runID, wfID, ctxHash, promptHash, rootPath string) (string, error) {
	id := core.NewID("ps_")
	now := core.Now()
	return id, q.Exec(`INSERT OR REPLACE INTO prompt_snapshots(id,run_id,workflow_snapshot_id,runtime_envelope_version,tool_manifest_version,context_hash,rendered_prompt_hash,context_json_path,redacted_prompt_path,prompt_meta_json_path,tool_manifest_path,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, runID, wfID, "v1", "v1", ctxHash, promptHash, filepath.Join(rootPath, "prompt/context.json"), filepath.Join(rootPath, "prompt/rendered_prompt.redacted.md"), filepath.Join(rootPath, "prompt/prompt_meta.json"), filepath.Join(rootPath, "prompt/tool_manifest.md"), now)
}

func (s *Store) CreateToolToken(runID string, tokenHash string, scope map[string]any, expiresAt string) (string, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return "", err
	}
	id := core.NewID("tok_")
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		run, err = s.getRunTx(tx, runID)
		if err != nil {
			return err
		}
		if run.Status != core.RunStartingAgent && run.Status != core.RunRunning {
			return core.NewError(core.ErrInvalidStateTransition, "run is not starting agent or running", map[string]any{"run_id": runID, "status": run.Status})
		}
		return tx.Exec(`INSERT INTO run_tool_tokens(id,run_id,issue_id,token_hash,scope_json,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`, id, runID, run.IssueID, tokenHash, encodeJSON(scope), core.Now(), expiresAt)
	}); err != nil {
		return "", err
	}
	return id, nil
}
func (s *Store) ValidateToolToken(tokenHash string) (runID, issueID string, err error) {
	runID, issueID, _, err = s.ValidateToolTokenWithScope(tokenHash)
	return runID, issueID, err
}

func (s *Store) ValidateToolTokenWithScope(tokenHash string) (runID, issueID string, scope map[string]any, err error) {
	row, err := s.Project.QueryOne(`SELECT t.*, r.status AS run_status FROM run_tool_tokens t JOIN run_attempts r ON r.id=t.run_id WHERE t.token_hash=?`, tokenHash)
	if errors.Is(err, os.ErrNotExist) {
		return "", "", nil, core.NewError(core.ErrToolTokenInvalid, "tool token invalid", nil)
	}
	if err != nil {
		return "", "", nil, err
	}
	if !row["revoked_at"].Null && row["revoked_at"].String() != "" {
		return "", "", nil, core.NewError(core.ErrToolTokenInvalid, "tool token revoked", nil)
	}
	if toolTokenExpiredAtOrBefore(row["expires_at"].String(), time.Now().UTC()) {
		return "", "", nil, core.NewError(core.ErrToolTokenInvalid, "tool token expired", nil)
	}
	if core.RunStatus(row["run_status"].String()) != core.RunRunning {
		return "", "", nil, core.NewError(core.ErrToolTokenInvalid, "run is not running", nil)
	}
	if err := json.Unmarshal([]byte(row["scope_json"].String()), &scope); err != nil {
		return "", "", nil, core.NewError(core.ErrToolTokenInvalid, "tool token scope invalid", nil)
	}
	if scope == nil {
		scope = map[string]any{}
	}
	_ = s.Project.Exec(`UPDATE run_tool_tokens SET last_used_at=? WHERE id=?`, core.Now(), row["id"].String())
	return row["run_id"].String(), row["issue_id"].String(), scope, nil
}

func toolTokenExpiredAtOrBefore(expiresAt string, now time.Time) bool {
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return true
	}
	return !expires.After(now)
}

func (s *Store) RecordToolCall(issueID, runID, tool, status string, input, output any, errCode, errMsg string) error {
	now := core.Now()
	ended := now
	if status == "started" {
		ended = ""
	}
	ih := hashJSON(input)
	oh := hashJSON(output)
	return s.Project.Exec(`INSERT INTO tool_calls(id,issue_id,run_id,tool_name,status,input_hash,input_json_redacted,output_hash,output_json_redacted,error_code,error_message,started_at,ended_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, core.NewID("tc_"), issueID, runID, tool, status, ih, redactedJSONSummary(input), oh, redactedJSONSummary(output), errCode, errMsg, now, core.NullableString(ended))
}
func hashJSON(v any) string {
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func redactedJSONSummary(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return encodeJSON(map[string]any{"type": "unmarshalable"})
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return encodeJSON(map[string]any{"type": "unparseable", "sha256": hashJSON(v)})
	}
	return encodeJSON(jsonShape(decoded))
}

func jsonShape(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return map[string]any{"type": "null"}
	case map[string]any:
		keys := make([]map[string]any, 0, len(x))
		for k := range x {
			sum := sha256.Sum256([]byte(k))
			keys = append(keys, map[string]any{
				"len":    len(k),
				"sha256": hex.EncodeToString(sum[:]),
			})
		}
		sort.Slice(keys, func(i, j int) bool {
			return keys[i]["sha256"].(string) < keys[j]["sha256"].(string)
		})
		if len(keys) > 20 {
			keys = keys[:20]
		}
		return map[string]any{"type": "object", "key_count": len(x), "keys": keys}
	case []any:
		items := make([]any, 0, min(len(x), 3))
		for i := 0; i < len(x) && i < 3; i++ {
			items = append(items, jsonShape(x[i]))
		}
		return map[string]any{"type": "array", "len": len(x), "items": items}
	case string:
		sum := sha256.Sum256([]byte(x))
		return map[string]any{"type": "string", "len": len(x), "sha256": hex.EncodeToString(sum[:])}
	case bool:
		return map[string]any{"type": "bool"}
	case float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{"type": "unknown"}
	}
}

func (s *Store) InsertHandoff(issueID, runID, payloadHash string, payload map[string]any) (*core.Handoff, error) {
	existing, err := s.GetHandoffByRun(runID)
	if err == nil {
		if existing.PayloadHash == payloadHash {
			return existing, nil
		}
		return nil, core.NewError(core.APIErrorCode("handoff_conflict"), "handoff already exists for this run with a different payload hash", map[string]any{"run_id": runID, "handoff_id": existing.ID, "existing_payload_hash": existing.PayloadHash, "incoming_payload_hash": payloadHash})
	}
	if core.AsAPIError(err).Code != core.ErrNotFound {
		return nil, err
	}
	id := core.NewID("hand_")
	now := core.Now()
	summary, _ := payload["summary"].(string)
	target := "Human Review"
	if x, ok := payload["target_state"].(string); ok && x != "" {
		target = x
	}
	cf := toStringSlice(payload["changed_files"])
	tests := toStringSlice(payload["tests"])
	risks := toStringSlice(payload["risks"])
	ver := toStringSlice(payload["verification"])
	follow := toStringSlice(payload["followups"])
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		if err := tx.Exec(`INSERT INTO handoffs(id,issue_id,run_id,payload_hash,payload_json_redacted,summary,changed_files_json,tests_json,risks_json,verification_json,followups_json,target_state,submitted_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, issueID, runID, payloadHash, encodeJSON(payload), summary, encodeJSON(cf), encodeJSON(tests), encodeJSON(risks), encodeJSON(ver), encodeJSON(follow), target, now); err != nil {
			return err
		}
		return s.appendEventInTx(tx, "handoff.submitted", "agent", &issueID, &runID, map[string]any{"handoff_id": id, "payload_hash": payloadHash})
	}); err != nil {
		return nil, err
	}
	return s.GetHandoffByRun(runID)
}
func toStringSlice(v any) []string {
	out := []string{}
	if a, ok := v.([]string); ok {
		return a
	}
	if a, ok := v.([]any); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func (s *Store) GetHandoffByRun(runID string) (*core.Handoff, error) {
	row, err := s.Project.QueryOne(`SELECT * FROM handoffs WHERE run_id=?`, runID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.NewError(core.ErrNotFound, "handoff not found", nil)
	}
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	_ = json.Unmarshal([]byte(row["payload_json_redacted"].String()), &payload)
	return &core.Handoff{ID: row["id"].String(), IssueID: row["issue_id"].String(), RunID: row["run_id"].String(), PayloadHash: row["payload_hash"].String(), Payload: payload, Summary: row["summary"].String(), ChangedFiles: decodeStringSlice(row["changed_files_json"].String()), Tests: decodeStringSlice(row["tests_json"].String()), Risks: decodeStringSlice(row["risks_json"].String()), Verification: decodeStringSlice(row["verification_json"].String()), Followups: decodeStringSlice(row["followups_json"].String()), TargetState: row["target_state"].String(), SubmittedAt: row["submitted_at"].String()}, nil
}

func (s *Store) InsertArtifact(a ArtifactRecord) error {
	return s.InsertArtifactTx(s.Project, a)
}

func (s *Store) InsertArtifactTx(q TxRunner, a ArtifactRecord) error {
	if a.ID == "" {
		a.ID = core.NewID("art_")
	}
	if a.CreatedAt == "" {
		a.CreatedAt = core.Now()
	}
	red := 0
	if a.Redacted {
		red = 1
	}
	return q.Exec(`INSERT INTO artifacts(id,issue_id,run_id,review_packet_id,kind,path,mime_type,size_bytes,sha256,redacted,description,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, a.ID, a.IssueID, a.RunID, a.ReviewPacketID, a.Kind, a.Path, a.MimeType, a.SizeBytes, a.SHA256, red, a.Description, a.CreatedAt)
}
func (s *Store) GetArtifact(id string) (*ArtifactRecord, error) {
	row, err := s.Project.QueryOne(`SELECT * FROM artifacts WHERE id=?`, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.NewError(core.ErrNotFound, "artifact not found", nil)
	}
	if err != nil {
		return nil, err
	}
	return &ArtifactRecord{ID: row["id"].String(), IssueID: ptrFromVal(row["issue_id"]), RunID: ptrFromVal(row["run_id"]), ReviewPacketID: ptrFromVal(row["review_packet_id"]), Kind: row["kind"].String(), Path: row["path"].String(), MimeType: ptrFromVal(row["mime_type"]), SizeBytes: row["size_bytes"].Int64(), SHA256: ptrFromVal(row["sha256"]), Redacted: row["redacted"].Bool(), Description: ptrFromVal(row["description"]), CreatedAt: row["created_at"].String()}, nil
}
func (s *Store) ArtifactsForReview(rpID string) ([]*ArtifactRecord, error) {
	rows, err := s.Project.Query(`SELECT * FROM artifacts WHERE review_packet_id=? ORDER BY kind,path`, rpID)
	if err != nil {
		return nil, err
	}
	out := []*ArtifactRecord{}
	for _, row := range rows {
		out = append(out, &ArtifactRecord{ID: row["id"].String(), IssueID: ptrFromVal(row["issue_id"]), RunID: ptrFromVal(row["run_id"]), ReviewPacketID: ptrFromVal(row["review_packet_id"]), Kind: row["kind"].String(), Path: row["path"].String(), MimeType: ptrFromVal(row["mime_type"]), SizeBytes: row["size_bytes"].Int64(), SHA256: ptrFromVal(row["sha256"]), Redacted: row["redacted"].Bool(), Description: ptrFromVal(row["description"]), CreatedAt: row["created_at"].String()})
	}
	return out, nil
}

func (s *Store) InsertReviewPacket(issueID, runID, handoffID, root, reviewMD, reviewJSON, patch, changed, untracked, diffstat, promptSnapshotID string) (string, error) {
	var id string
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		var err error
		id, err = s.insertReviewPacketInTx(tx, issueID, runID, handoffID, root, reviewMD, reviewJSON, patch, changed, untracked, diffstat, promptSnapshotID)
		return err
	}); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) InsertReviewPacketTx(q TxRunner, issueID, runID, handoffID, root, reviewMD, reviewJSON, patch, changed, untracked, diffstat, promptSnapshotID string) (string, error) {
	return s.insertReviewPacketInTx(q, issueID, runID, handoffID, root, reviewMD, reviewJSON, patch, changed, untracked, diffstat, promptSnapshotID)
}

func (s *Store) insertReviewPacketInTx(q sqlRunner, issueID, runID, handoffID, root, reviewMD, reviewJSON, patch, changed, untracked, diffstat, promptSnapshotID string) (string, error) {
	row, err := q.QueryOne(`SELECT COALESCE(MAX(packet_no),0)+1 AS n FROM review_packets WHERE issue_id=?`, issueID)
	if err != nil {
		return "", err
	}
	packetNo := row["n"].Int()
	id := core.NewID("rp_")
	now := core.Now()
	if err := q.Exec(`INSERT INTO review_packets(id,issue_id,run_id,handoff_id,packet_no,status,root_path,review_md_path,review_json_path,patch_path,changed_files_path,untracked_files_path,diffstat_path,prompt_snapshot_id,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, issueID, runID, handoffID, packetNo, "generated", root, reviewMD, reviewJSON, patch, changed, untracked, diffstat, core.NullableString(promptSnapshotID), now); err != nil {
		return "", err
	}
	if err := q.Exec(`UPDATE issues SET latest_review_packet_id=?, updated_at=? WHERE id=?`, id, now, issueID); err != nil {
		return "", err
	}
	return id, nil
}
func (s *Store) ReviewPacketRow(issueRef string) (map[string]db.Value, error) {
	issue, err := s.GetIssue(issueRef)
	if err != nil {
		return nil, err
	}
	row, err := s.Project.QueryOne(`SELECT * FROM review_packets WHERE issue_id=? ORDER BY packet_no DESC LIMIT 1`, issue.ID)
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.NewError(core.ErrReviewPacketRequired, "review packet required", nil)
	}
	return row, err
}

func (s *Store) MarkDone(issueRef, reason string) (*core.Issue, error) {
	return s.reviewAction(issueRef, reason, core.StateDone)
}
func (s *Store) SendToRework(issueRef, reason string) (*core.Issue, error) {
	return s.reviewAction(issueRef, reason, core.StateRework)
}
func (s *Store) reviewAction(issueRef, reason string, target core.IssueState) (*core.Issue, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, core.NewError(core.ErrInvalidRequest, "reason is required", nil)
	}
	now := core.Now()
	var issueID string
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		issue, err := s.issueRowByRefTx(tx, issueRef)
		if err != nil {
			return err
		}
		issueID = issue["id"].String()
		if core.IssueState(issue["state"].String()) != core.StateHumanReview {
			return core.NewError(core.ErrInvalidStateTransition, "issue is not in Human Review", nil)
		}
		active, err := s.activeRunIDTx(tx, issueID)
		if err != nil {
			return err
		}
		if active != nil {
			return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
		}
		rp, err := tx.QueryOne(`SELECT * FROM review_packets WHERE issue_id=? ORDER BY packet_no DESC LIMIT 1`, issueID)
		if errors.Is(err, os.ErrNotExist) {
			return core.NewError(core.ErrReviewPacketRequired, "review packet required", nil)
		}
		if err != nil {
			return err
		}
		reviewPacketID := rp["id"].String()
		if issue["latest_review_packet_id"].String() != reviewPacketID {
			return core.NewError(core.ErrReviewPacketRequired, "latest review packet does not match issue", nil)
		}
		if rp["status"].String() != "generated" {
			return core.NewError(core.ErrReviewPacketRequired, "latest review packet is not generated", nil)
		}
		runID := rp["run_id"].String()
		run, err := s.getRunTx(tx, runID)
		if err != nil {
			return err
		}
		if run.Status != core.RunCompleted {
			return core.NewError(core.ErrReviewPacketRequired, "latest review packet does not belong to latest completed handoff run", nil)
		}
		var completedAt any = nil
		if target == core.StateDone {
			completedAt = now
		}
		if err := tx.Exec(`UPDATE issues SET state=?, completed_at=?, dispatch_paused=0, dispatch_pause_reason=NULL, dispatch_paused_at=NULL, updated_at=?
WHERE id=? AND state=? AND latest_review_packet_id=? AND NOT EXISTS (
	SELECT 1 FROM run_attempts
	WHERE issue_id=? AND status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running')
)`, string(target), completedAt, now, issueID, string(core.StateHumanReview), reviewPacketID, issueID); err != nil {
			return err
		}
		changed, err := rowsChanged(tx)
		if err != nil {
			return err
		}
		if changed != 1 {
			active, err := s.activeRunIDTx(tx, issueID)
			if err != nil {
				return err
			}
			if active != nil {
				return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
			}
			cur, err := tx.QueryOne(`SELECT state, latest_review_packet_id FROM issues WHERE id=?`, issueID)
			if err != nil {
				return err
			}
			if core.IssueState(cur["state"].String()) != core.StateHumanReview {
				return core.NewError(core.ErrInvalidStateTransition, "issue is not in Human Review", nil)
			}
			return core.NewError(core.ErrReviewPacketRequired, "latest review packet does not match issue", nil)
		}
		active, err = s.activeRunIDTx(tx, issueID)
		if err != nil {
			return err
		}
		if active != nil {
			return core.NewError(core.ErrIssueAlreadyRunning, "issue has an active run", nil)
		}
		latest, err := tx.QueryOne(`SELECT * FROM review_packets WHERE issue_id=? ORDER BY packet_no DESC LIMIT 1`, issueID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return core.NewError(core.ErrReviewPacketRequired, "review packet required", nil)
			}
			return err
		}
		if latest["id"].String() != reviewPacketID || latest["status"].String() != "generated" {
			return core.NewError(core.ErrReviewPacketRequired, "latest review packet is not generated", nil)
		}
		run, err = s.getRunTx(tx, runID)
		if err != nil {
			return err
		}
		if run.Status != core.RunCompleted {
			return core.NewError(core.ErrReviewPacketRequired, "latest review packet does not belong to latest completed handoff run", nil)
		}
		if err := tx.Exec(`INSERT INTO issue_state_history(id,issue_id,from_state,to_state,actor_type,reason,created_at) VALUES(?,?,?,?,?,?,?)`, core.NewID("hist_"), issueID, string(core.StateHumanReview), string(target), "operator", reason, now); err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO issue_comments(id,issue_id,author_type,body,created_at) VALUES(?,?,?,?,?)`, core.NewID("com_"), issueID, "operator", reason, now); err != nil {
			return err
		}
		event := "review.sent_to_rework"
		if target == core.StateDone {
			event = "review.marked_done"
		}
		if err := s.appendEventInTx(tx, event, "operator", &issueID, nil, map[string]any{"reason": reason}); err != nil {
			return err
		}
		if target == core.StateDone {
			if err := s.appendEventInTx(tx, "issue.completed", "operator", &issueID, nil, map[string]any{"reason": reason}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return s.GetIssue(issueID)
}

func (s *Store) PendingApprovals() ([]Approval, error) {
	rows, err := s.Project.Query(`SELECT * FROM approval_requests ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	out := []Approval{}
	for _, r := range rows {
		out = append(out, approvalFromRow(r))
	}
	return out, nil
}

func (s *Store) ApprovalByID(id string) (*Approval, error) {
	row, err := s.Project.QueryOne(`SELECT * FROM approval_requests WHERE id=?`, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil, core.NewError(core.ErrNotFound, "approval not found", nil)
	}
	if err != nil {
		return nil, err
	}
	approval := approvalFromRow(row)
	return &approval, nil
}

func (s *Store) CreatePendingApprovalRequest(in CreateApprovalRequestInput) (*Approval, error) {
	id := core.NewID("apr_")
	createdAt := core.Now()
	switch in.Kind {
	case "command", "file_change", "network":
	default:
		return nil, core.NewError(core.ErrInvalidRequest, "unsupported approval kind", map[string]any{"kind": in.Kind})
	}
	request := map[string]string{}
	if value := strings.TrimSpace(in.ActionSummary); value != "" {
		request["action_summary"] = value
	}
	if value := strings.TrimSpace(in.RiskLevel); value != "" {
		request["risk_level"] = value
	}
	if value := strings.TrimSpace(in.PolicyMatch); value != "" {
		request["policy_match"] = value
	}
	if value := strings.TrimSpace(in.RequestID); value != "" {
		request["request_id"] = value
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	var timeoutMS any
	var expiresAt any
	if in.TimeoutMS > 0 {
		timeoutMS = in.TimeoutMS
		created, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		expiresAt = created.Add(time.Duration(in.TimeoutMS) * time.Millisecond).UTC().Format(time.RFC3339Nano)
	}
	if err := s.Project.WithTx(func(tx *db.Tx) error {
		run, err := s.getRunTx(tx, in.RunID)
		if err != nil {
			return err
		}
		if run.IssueID != in.IssueID {
			return core.NewError(core.ErrInvalidRequest, "approval issue does not match run", map[string]any{"run_id": in.RunID, "issue_id": in.IssueID})
		}
		if !core.IsActiveRunStatus(run.Status) {
			return core.NewError(core.ErrInvalidStateTransition, "run is not active", map[string]any{"run_id": in.RunID, "status": run.Status})
		}
		return tx.Exec(`INSERT INTO approval_requests(id,run_id,issue_id,kind,status,request_json,timeout_ms,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, in.RunID, in.IssueID, in.Kind, "pending", string(requestJSON), timeoutMS, expiresAt, createdAt)
	}); err != nil {
		return nil, err
	}
	return s.ApprovalByID(id)
}

func approvalFromRow(r map[string]db.Value) Approval {
	id := r["id"].String()
	kind := r["kind"].String()
	actionSummary, riskLevel, policyMatch := approvalRequestFields(r["request_json"].String(), kind, id)
	createdAt := r["created_at"].String()
	return Approval{
		ID:            id,
		RunID:         r["run_id"].String(),
		IssueID:       r["issue_id"].String(),
		Kind:          kind,
		Status:        r["status"].String(),
		ActionSummary: actionSummary,
		RiskLevel:     riskLevel,
		PolicyMatch:   policyMatch,
		RequestedAt:   createdAt,
		CreatedAt:     createdAt,
		TimeoutMS:     int64PtrFromVal(r["timeout_ms"]),
		ExpiresAt:     ptrFromVal(r["expires_at"]),
		ResolvedAt:    ptrFromVal(r["resolved_at"]),
		Reason:        ptrFromVal(r["reason"]),
	}
}

func approvalRequestFields(raw, kind, id string) (string, string, string) {
	actionSummary := kind + " approval " + id
	riskLevel := "unknown"
	policyMatch := "unclassified"
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return actionSummary, riskLevel, policyMatch
	}
	if value, ok := nonBlankJSONString(fields["action_summary"]); ok {
		actionSummary = value
	}
	if value, ok := nonBlankJSONString(fields["risk_level"]); ok {
		riskLevel = value
	}
	if value, ok := nonBlankJSONString(fields["policy_match"]); ok {
		policyMatch = value
	}
	return actionSummary, riskLevel, policyMatch
}

func nonBlankJSONString(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

func approvalDecisionJSON(status, reason, resolvedAt string) (string, error) {
	b, err := json.Marshal(map[string]string{
		"status":      status,
		"reason":      reason,
		"resolved_at": resolvedAt,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Store) DecideApproval(id, status, reason string) error {
	if status == "cancel_run" {
		status = "cancelled"
	}
	now := core.Now()
	decisionJSON, err := approvalDecisionJSON(status, reason, now)
	if err != nil {
		return err
	}
	return s.Project.WithTx(func(tx *db.Tx) error {
		row, err := tx.QueryOne(`SELECT * FROM approval_requests WHERE id=?`, id)
		if errors.Is(err, os.ErrNotExist) {
			return core.NewError(core.ErrNotFound, "approval not found", nil)
		}
		if err != nil {
			return err
		}
		if row["status"].String() != "pending" {
			return core.NewError(core.ErrApprovalNotPending, "approval is not pending", nil)
		}
		if status == "cancelled" {
			if err := s.cancelRunInTx(tx, row["run_id"].String(), core.FailureOperatorCancelled, reason, true); err != nil {
				return err
			}
		}
		if err := tx.Exec(`UPDATE approval_requests SET status=?, reason=?, decision_json=?, resolved_at=? WHERE id=? AND status='pending'`, status, reason, decisionJSON, now, id); err != nil {
			return err
		}
		changed, err := rowsChanged(tx)
		if err != nil {
			return err
		}
		if changed != 1 {
			return core.NewError(core.ErrApprovalNotPending, "approval is not pending", nil)
		}
		return nil
	})
}

func (s *Store) MarkApprovalTimeout(id, reason string) error {
	now := core.Now()
	decisionJSON, err := approvalDecisionJSON("timeout", reason, now)
	if err != nil {
		return err
	}
	return s.Project.WithTx(func(tx *db.Tx) error {
		if err := tx.Exec(`UPDATE approval_requests SET status='timeout', reason=?, decision_json=?, resolved_at=? WHERE id=? AND status='pending'`, reason, decisionJSON, now, id); err != nil {
			return err
		}
		changed, err := rowsChanged(tx)
		if err != nil {
			return err
		}
		if changed != 1 {
			return core.NewError(core.ErrApprovalNotPending, "approval is not pending", nil)
		}
		return nil
	})
}

func (s *Store) MarkApprovalCancelled(id, reason string) error {
	now := core.Now()
	decisionJSON, err := approvalDecisionJSON("cancelled", reason, now)
	if err != nil {
		return err
	}
	return s.Project.WithTx(func(tx *db.Tx) error {
		if err := tx.Exec(`UPDATE approval_requests SET status='cancelled', reason=?, decision_json=?, resolved_at=? WHERE id=? AND status='pending'`, reason, decisionJSON, now, id); err != nil {
			return err
		}
		changed, err := rowsChanged(tx)
		if err != nil {
			return err
		}
		if changed != 1 {
			return core.NewError(core.ErrApprovalNotPending, "approval is not pending", nil)
		}
		return nil
	})
}

func (s *Store) ReconcileStaleActiveRuns() error {
	rows, err := s.Project.Query(`SELECT id FROM run_attempts WHERE status IN ('pending','preparing_workspace','rendering_prompt','starting_agent','running')`)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		runID := r["id"].String()
		if err := s.FailRun(runID, core.FailureDaemonRestartedInterrupted, "daemon restarted while run was active", core.RunFailed); err != nil {
			errs = append(errs, fmt.Errorf("reconcile stale run %s: %w", runID, err))
		}
	}
	return errors.Join(errs...)
}
func (s *Store) CreateRuntimeDescriptor(apiURL, toolURL string, pid int) error {
	if err := os.MkdirAll(db.RuntimeDir(), 0o700); err != nil {
		return err
	}
	payload := map[string]any{"project_id": s.ProjectID, "repo_root": s.RepoRoot, "api_url": apiURL, "tool_gateway_endpoint": toolURL, "daemon_pid": pid, "started_at": core.Now()}
	b, _ := json.MarshalIndent(payload, "", "  ")
	_ = s.App.Exec(`INSERT OR REPLACE INTO runtime_descriptors(project_id,api_url,tool_gateway_endpoint,daemon_pid,started_at,updated_at) VALUES(?,?,?,?,?,?)`, s.ProjectID, apiURL, toolURL, pid, core.Now(), core.Now())
	return os.WriteFile(db.RuntimeDescriptorPath(s.ProjectID), b, 0o600)
}
func (s *Store) RemoveRuntimeDescriptor() {
	_ = os.Remove(db.RuntimeDescriptorPath(s.ProjectID))
	_ = s.App.Exec(`DELETE FROM runtime_descriptors WHERE project_id=?`, s.ProjectID)
}

func ParseBool(s string) *bool {
	if s == "" {
		return nil
	}
	v := strings.ToLower(s)
	b := v == "1" || v == "true" || v == "yes"
	return &b
}
func ParseInt(s string, def int) int {
	if s == "" {
		return def
	}
	i, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return i
}
