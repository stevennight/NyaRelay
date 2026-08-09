package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"nyarelay/internal/shared/model"
	sharedversion "nyarelay/internal/shared/version"
)

const singleCandidateStrategyMigrationSetting = "migration_single_candidate_strategies_v1"

type Store struct {
	db *sql.DB
}

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	TOTPEnabled  bool      `json:"totp_enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		closeDB(db)
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA journal_mode=WAL;`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			totp_secret TEXT NOT NULL DEFAULT '',
			totp_enabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			token TEXT NOT NULL,
			status TEXT NOT NULL,
			version TEXT NOT NULL DEFAULT '',
			desired_version TEXT NOT NULL DEFAULT '',
			update_status TEXT NOT NULL DEFAULT '',
			update_error TEXT NOT NULL DEFAULT '',
			update_requested_at TEXT NOT NULL DEFAULT '',
			update_finished_at TEXT NOT NULL DEFAULT '',
			labels_json TEXT NOT NULL DEFAULT '{}',
			public_host TEXT NOT NULL DEFAULT '',
			port_min INTEGER NOT NULL DEFAULT 10000,
			port_max INTEGER NOT NULL DEFAULT 65535,
			approved INTEGER NOT NULL DEFAULT 1,
			revoked INTEGER NOT NULL DEFAULT 0,
			last_seen TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			system_json TEXT NOT NULL DEFAULT '{}'
		);`,
		`CREATE TABLE IF NOT EXISTS tunnels (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			transport TEXT NOT NULL,
			entry_address TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL,
			settings_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS tunnel_stages (
			id TEXT PRIMARY KEY,
			tunnel_id TEXT NOT NULL,
			stage_index INTEGER NOT NULL,
			role TEXT NOT NULL,
			strategy TEXT NOT NULL DEFAULT 'single',
			tcp_strategy TEXT NOT NULL DEFAULT '',
			udp_strategy TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS tunnel_stage_nodes (
			id TEXT PRIMARY KEY,
			tunnel_id TEXT NOT NULL,
			stage_id TEXT NOT NULL,
			node_id TEXT NOT NULL,
			listen_addr TEXT NOT NULL DEFAULT '',
			public_addr TEXT NOT NULL DEFAULT '',
			connect_addr TEXT NOT NULL DEFAULT '',
			weight INTEGER NOT NULL DEFAULT 1,
			protocols_json TEXT NOT NULL DEFAULT '[]',
			settings_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS forwards (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			tunnel_id TEXT NOT NULL,
			protocols_json TEXT NOT NULL,
			listen TEXT NOT NULL,
			target TEXT NOT NULL,
			strategy TEXT NOT NULL DEFAULT '',
			tcp_strategy TEXT NOT NULL DEFAULT '',
			udp_strategy TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS forward_targets (
			id TEXT PRIMARY KEY,
			forward_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			address TEXT NOT NULL,
			protocols_json TEXT NOT NULL DEFAULT '[]',
			weight INTEGER NOT NULL DEFAULT 1,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS port_allocations (
			id TEXT PRIMARY KEY,
			node_id TEXT NOT NULL,
			owner_kind TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			protocol TEXT NOT NULL,
			port INTEGER NOT NULL,
			bind_addr TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(node_id, protocol, port)
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tunnel_stage_order ON tunnel_stages(tunnel_id, stage_index);`,
		`CREATE INDEX IF NOT EXISTS idx_tunnel_stage_nodes_tunnel ON tunnel_stage_nodes(tunnel_id);`,
		`CREATE INDEX IF NOT EXISTS idx_tunnel_stage_nodes_stage ON tunnel_stage_nodes(stage_id);`,
		`CREATE INDEX IF NOT EXISTS idx_forwards_tunnel ON forwards(tunnel_id);`,
		`CREATE INDEX IF NOT EXISTS idx_forward_targets_forward ON forward_targets(forward_id, position);`,
		`CREATE INDEX IF NOT EXISTS idx_port_allocations_owner ON port_allocations(owner_kind, owner_id);`,
		`CREATE TABLE IF NOT EXISTS metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			node_id TEXT NOT NULL,
			stat_id TEXT NOT NULL,
			stat_kind TEXT NOT NULL,
			bytes_in INTEGER NOT NULL,
			bytes_out INTEGER NOT NULL,
			connections INTEGER NOT NULL,
			observed_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor TEXT NOT NULL,
			action TEXT NOT NULL,
			target TEXT NOT NULL,
			detail TEXT NOT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_metrics_observed_at ON metrics(observed_at);`,
		`CREATE INDEX IF NOT EXISTS idx_audit_created_at ON audit(created_at);`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if err := s.ensureNodeColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureTunnelColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureForwardColumns(ctx); err != nil {
		return err
	}
	return s.migrateSingleCandidateStrategies(ctx)
}

func (s *Store) ensureNodeColumns(ctx context.Context) error {
	columns, err := s.tableColumns(ctx, "nodes")
	if err != nil {
		return err
	}
	if !columns["public_host"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE nodes ADD COLUMN public_host TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !columns["port_min"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE nodes ADD COLUMN port_min INTEGER NOT NULL DEFAULT 10000`); err != nil {
			return err
		}
	}
	if !columns["port_max"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE nodes ADD COLUMN port_max INTEGER NOT NULL DEFAULT 65535`); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		name string
		sql  string
	}{
		{name: "desired_version", sql: `ALTER TABLE nodes ADD COLUMN desired_version TEXT NOT NULL DEFAULT ''`},
		{name: "update_status", sql: `ALTER TABLE nodes ADD COLUMN update_status TEXT NOT NULL DEFAULT ''`},
		{name: "update_error", sql: `ALTER TABLE nodes ADD COLUMN update_error TEXT NOT NULL DEFAULT ''`},
		{name: "update_requested_at", sql: `ALTER TABLE nodes ADD COLUMN update_requested_at TEXT NOT NULL DEFAULT ''`},
		{name: "update_finished_at", sql: `ALTER TABLE nodes ADD COLUMN update_finished_at TEXT NOT NULL DEFAULT ''`},
	} {
		if !columns[column.name] {
			if _, err := s.db.ExecContext(ctx, column.sql); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) ensureTunnelColumns(ctx context.Context) error {
	columns, err := s.tableColumns(ctx, "tunnels")
	if err != nil {
		return err
	}
	if !columns["entry_address"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE tunnels ADD COLUMN entry_address TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	stageColumns, err := s.tableColumns(ctx, "tunnel_stages")
	if err != nil {
		return err
	}
	if !stageColumns["tcp_strategy"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE tunnel_stages ADD COLUMN tcp_strategy TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if !stageColumns["udp_strategy"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE tunnel_stages ADD COLUMN udp_strategy TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	stageNodeColumns, err := s.tableColumns(ctx, "tunnel_stage_nodes")
	if err != nil {
		return err
	}
	if !stageNodeColumns["protocols_json"] {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE tunnel_stage_nodes ADD COLUMN protocols_json TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureForwardColumns(ctx context.Context) error {
	columns, err := s.tableColumns(ctx, "forwards")
	if err != nil {
		return err
	}
	for _, column := range []struct {
		name string
		sql  string
	}{
		{name: "strategy", sql: `ALTER TABLE forwards ADD COLUMN strategy TEXT NOT NULL DEFAULT ''`},
		{name: "tcp_strategy", sql: `ALTER TABLE forwards ADD COLUMN tcp_strategy TEXT NOT NULL DEFAULT ''`},
		{name: "udp_strategy", sql: `ALTER TABLE forwards ADD COLUMN udp_strategy TEXT NOT NULL DEFAULT ''`},
	} {
		if !columns[column.name] {
			if _, err := s.db.ExecContext(ctx, column.sql); err != nil {
				return err
			}
		}
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS forward_targets (
		id TEXT PRIMARY KEY,
		forward_id TEXT NOT NULL,
		position INTEGER NOT NULL,
		address TEXT NOT NULL,
		protocols_json TEXT NOT NULL DEFAULT '[]',
		weight INTEGER NOT NULL DEFAULT 1,
		enabled INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_forward_targets_forward ON forward_targets(forward_id, position)`); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO forward_targets
			(id, forward_id, position, address, protocols_json, weight, enabled, created_at, updated_at)
		SELECT 'legacy:' || f.id, f.id, 0, f.target, '[]', 1, f.enabled, f.created_at, f.updated_at
		FROM forwards f
		WHERE TRIM(f.target) <> ''
		  AND NOT EXISTS (SELECT 1 FROM forward_targets t WHERE t.forward_id = f.id)`)
	return err
}

func (s *Store) migrateSingleCandidateStrategies(ctx context.Context) error {
	if _, ok, err := s.GetSetting(ctx, singleCandidateStrategyMigrationSetting); err != nil {
		return err
	} else if ok {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if _, err := tx.ExecContext(ctx, `
		UPDATE forwards
		SET strategy = 'single', tcp_strategy = 'single', udp_strategy = 'single'
		WHERE id IN (
			SELECT forward_id
			FROM forward_targets
			WHERE enabled = 1 AND TRIM(address) <> ''
			GROUP BY forward_id
			HAVING COUNT(*) = 1
		)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE tunnel_stages
		SET strategy = 'single', tcp_strategy = 'single', udp_strategy = 'single'
		WHERE id IN (
			SELECT stage_id
			FROM tunnel_stage_nodes
			WHERE TRIM(node_id) <> ''
			GROUP BY stage_id
			HAVING COUNT(*) = 1
		)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		singleCandidateStrategyMigrationSetting, "1",
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`,
		username, passwordHash, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: now}, nil
}

func (s *Store) FindUserByUsername(ctx context.Context, username string) (User, error) {
	var u User
	var created string
	var totpEnabled int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, password_hash, totp_enabled, created_at FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &totpEnabled, &created)
	if err != nil {
		return User{}, err
	}
	u.TOTPEnabled = totpEnabled == 1
	u.CreatedAt = parseTime(created)
	return u, nil
}

func (s *Store) TOTPSecret(ctx context.Context, userID int64) (string, bool, error) {
	var secret string
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT totp_secret, totp_enabled FROM users WHERE id = ?`, userID).Scan(&secret, &enabled)
	if err != nil {
		return "", false, err
	}
	return secret, enabled == 1, nil
}

func (s *Store) SetTOTPSecret(ctx context.Context, userID int64, secret string, enabled bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET totp_secret = ?, totp_enabled = ? WHERE id = ?`, secret, boolInt(enabled), userID)
	return err
}

func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SettingsPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings WHERE key LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	for key, value := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			key, value,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) UpsertNode(ctx context.Context, node model.Node, token string) error {
	now := time.Now().UTC()
	if node.CreatedAt.IsZero() {
		node.CreatedAt = now
	}
	normalizeNodePorts(&node)
	node.UpdatedAt = now
	labels, _ := json.Marshal(emptyMap(node.Labels))
	system, _ := json.Marshal(node.System)
	lastSeen := ""
	if !node.LastSeen.IsZero() {
		lastSeen = node.LastSeen.UTC().Format(time.RFC3339Nano)
	}
	updateRequestedAt := ""
	if !node.UpdateRequestedAt.IsZero() {
		updateRequestedAt = node.UpdateRequestedAt.UTC().Format(time.RFC3339Nano)
	}
	updateFinishedAt := ""
	if !node.UpdateFinishedAt.IsZero() {
		updateFinishedAt = node.UpdateFinishedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO nodes
		 (id, name, token, status, version, desired_version, update_status, update_error, update_requested_at, update_finished_at,
		  labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   status = excluded.status,
		   version = excluded.version,
		   desired_version = excluded.desired_version,
		   update_status = excluded.update_status,
		   update_error = excluded.update_error,
		   update_requested_at = excluded.update_requested_at,
		   update_finished_at = excluded.update_finished_at,
		   labels_json = excluded.labels_json,
		   public_host = excluded.public_host,
		   port_min = excluded.port_min,
		   port_max = excluded.port_max,
		   approved = excluded.approved,
		   revoked = excluded.revoked,
		   last_seen = excluded.last_seen,
		   updated_at = excluded.updated_at,
		   system_json = excluded.system_json`,
		node.ID, node.Name, token, string(node.Status), node.Version, node.DesiredVersion, string(node.UpdateStatus), node.UpdateError,
		updateRequestedAt, updateFinishedAt, string(labels), node.PublicHost, node.PortMin, node.PortMax,
		boolInt(node.Approved), boolInt(node.Revoked), lastSeen,
		node.CreatedAt.UTC().Format(time.RFC3339Nano), node.UpdatedAt.UTC().Format(time.RFC3339Nano), string(system),
	)
	return err
}

func (s *Store) UpdateNode(ctx context.Context, node model.Node) error {
	now := time.Now().UTC()
	normalizeNodePorts(&node)
	node.UpdatedAt = now
	labels, _ := json.Marshal(emptyMap(node.Labels))
	res, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET name = ?, labels_json = ?, public_host = ?, port_min = ?, port_max = ?, updated_at = ? WHERE id = ? AND revoked = 0`,
		node.Name, string(labels), node.PublicHost, node.PortMin, node.PortMax, node.UpdatedAt.UTC().Format(time.RFC3339Nano), node.ID,
	)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return errors.New("node not found")
	}
	return nil
}

func (s *Store) AuthenticateNode(ctx context.Context, id, token string) (model.Node, error) {
	node, storedToken, err := s.GetNodeWithToken(ctx, id)
	if err != nil {
		return model.Node{}, err
	}
	if storedToken != token || node.Revoked || !node.Approved {
		return model.Node{}, errors.New("node is not authorized")
	}
	return node, nil
}

func (s *Store) GetNodeWithToken(ctx context.Context, id string) (model.Node, string, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, token, status, version, desired_version, update_status, update_error, update_requested_at, update_finished_at,
		        labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json
		 FROM nodes WHERE id = ?`, id,
	)
	return scanNode(row)
}

func (s *Store) ListNodes(ctx context.Context) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, token, status, version, desired_version, update_status, update_error, update_requested_at, update_finished_at,
		        labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json
		 FROM nodes WHERE revoked = 0 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []model.Node
	for rows.Next() {
		node, _, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *Store) GetNode(ctx context.Context, id string) (model.Node, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, token, status, version, desired_version, update_status, update_error, update_requested_at, update_finished_at,
		        labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json
		 FROM nodes WHERE id = ?`, id,
	)
	node, _, err := scanNode(row)
	return node, err
}

func (s *Store) MarkNodeSeen(ctx context.Context, id string, system model.NodeSystem, version string) error {
	now := time.Now().UTC()
	systemJSON, _ := json.Marshal(system)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)

	var desiredVersion string
	err = tx.QueryRowContext(ctx, `SELECT desired_version FROM nodes WHERE id = ? AND revoked = 0`, id).Scan(&desiredVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	updateReached := desiredVersion != "" && !sharedversion.NeedsUpdate(version, desiredVersion)
	reached := boolInt(updateReached)
	_, err = tx.ExecContext(ctx,
		`UPDATE nodes
		    SET status = ?,
		        version = ?,
		        last_seen = ?,
		        updated_at = ?,
		        system_json = ?,
		        desired_version = CASE WHEN ? != 0 THEN ? ELSE desired_version END,
		        update_status = CASE WHEN ? != 0 THEN ? ELSE update_status END,
		        update_error = CASE WHEN ? != 0 THEN '' ELSE update_error END,
		        update_finished_at = CASE WHEN ? != 0 THEN ? ELSE update_finished_at END
		  WHERE id = ? AND revoked = 0`,
		string(model.NodeOnline), version, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), string(systemJSON),
		reached, version,
		reached, string(model.NodeUpdateSucceeded),
		reached,
		reached, now.Format(time.RFC3339Nano),
		id,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RequestNodeUpdate(ctx context.Context, id, version string) (model.Node, error) {
	now := time.Now().UTC()
	res, err := s.db.ExecContext(ctx,
		`UPDATE nodes
		    SET desired_version = ?,
		        update_status = ?,
		        update_error = '',
		        update_requested_at = ?,
		        update_finished_at = '',
		        updated_at = ?
		  WHERE id = ? AND revoked = 0`,
		version, string(model.NodeUpdateRequested), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return model.Node{}, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return model.Node{}, errors.New("node not found")
	}
	return s.GetNode(ctx, id)
}

func (s *Store) UpdateNodeReport(ctx context.Context, id string, report model.NodeUpdateReport) error {
	now := time.Now().UTC()
	finished := ""
	if !report.CompletedAt.IsZero() {
		finished = report.CompletedAt.UTC().Format(time.RFC3339Nano)
	} else if report.Status == model.NodeUpdateSucceeded || report.Status == model.NodeUpdateFailed {
		finished = now.Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes
		    SET update_status = ?,
		        update_error = ?,
		        update_finished_at = CASE WHEN ? != '' THEN ? ELSE update_finished_at END,
		        updated_at = ?
		  WHERE id = ? AND revoked = 0`,
		string(report.Status), report.Error, finished, finished, now.Format(time.RFC3339Nano), id,
	)
	return err
}

func (s *Store) MarkNodeOffline(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET status = ?, updated_at = ? WHERE id = ? AND revoked = 0`,
		string(model.NodeOffline), now.Format(time.RFC3339Nano), id,
	)
	return err
}

func (s *Store) RevokeNode(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET revoked = 1, status = ?, updated_at = ? WHERE id = ?`,
		string(model.NodeRevoked), now.Format(time.RFC3339Nano), id,
	)
	return err
}

func (s *Store) SaveTunnel(ctx context.Context, tunnel model.Tunnel, allocations []model.PortAllocation) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTx(tx)
	now := time.Now().UTC()
	if tunnel.CreatedAt.IsZero() {
		tunnel.CreatedAt = now
	}
	tunnel.UpdatedAt = now
	settings, _ := json.Marshal(emptyMap(tunnel.Settings))
	_, err = tx.ExecContext(ctx,
		`INSERT INTO tunnels (id, name, type, transport, entry_address, enabled, settings_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   type = excluded.type,
		   transport = excluded.transport,
		   entry_address = excluded.entry_address,
		   enabled = excluded.enabled,
		   settings_json = excluded.settings_json,
		   updated_at = excluded.updated_at`,
		tunnel.ID, tunnel.Name, string(tunnel.Type), string(tunnel.Transport), tunnel.EntryAddress, boolInt(tunnel.Enabled), string(settings),
		tunnel.CreatedAt.UTC().Format(time.RFC3339Nano), tunnel.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM port_allocations
		 WHERE owner_kind = 'tunnel_stage_node'
		   AND owner_id IN (SELECT id FROM tunnel_stage_nodes WHERE tunnel_id = ?)`,
		tunnel.ID,
	); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tunnel_stage_nodes WHERE tunnel_id = ?`, tunnel.ID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tunnel_stages WHERE tunnel_id = ?`, tunnel.ID); err != nil {
		return 0, err
	}
	for _, stage := range tunnel.Stages {
		if stage.CreatedAt.IsZero() {
			stage.CreatedAt = now
		}
		stage.UpdatedAt = now
		if stage.Strategy == "" {
			stage.Strategy = "single"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tunnel_stages (id, tunnel_id, stage_index, role, strategy, tcp_strategy, udp_strategy, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			stage.ID, tunnel.ID, stage.Index, string(stage.Role), stage.Strategy, stage.TCPStrategy, stage.UDPStrategy,
			stage.CreatedAt.UTC().Format(time.RFC3339Nano), stage.UpdatedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return 0, err
		}
		for _, node := range stage.Nodes {
			if node.CreatedAt.IsZero() {
				node.CreatedAt = now
			}
			node.UpdatedAt = now
			if node.Weight <= 0 {
				node.Weight = 1
			}
			protocols, _ := json.Marshal(node.Protocols)
			nodeSettings, _ := json.Marshal(emptyMap(node.Settings))
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO tunnel_stage_nodes
				 (id, tunnel_id, stage_id, node_id, listen_addr, public_addr, connect_addr, weight, protocols_json, settings_json, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				node.ID, tunnel.ID, stage.ID, node.NodeID, node.ListenAddr, node.PublicAddr, node.ConnectAddr,
				node.Weight, string(protocols), string(nodeSettings), node.CreatedAt.UTC().Format(time.RFC3339Nano), node.UpdatedAt.UTC().Format(time.RFC3339Nano),
			); err != nil {
				return 0, err
			}
		}
	}
	if err := insertAllocations(ctx, tx, allocations); err != nil {
		return 0, err
	}
	rev, err := bumpRevisionTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *Store) ListTunnels(ctx context.Context) ([]model.Tunnel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, type, transport, entry_address, enabled, settings_json, created_at, updated_at
		 FROM tunnels ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []model.Tunnel
	for rows.Next() {
		tunnel, err := scanTunnel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, tunnel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadTunnelStages(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) GetTunnel(ctx context.Context, id string) (model.Tunnel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, transport, entry_address, enabled, settings_json, created_at, updated_at
		 FROM tunnels WHERE id = ?`, id,
	)
	tunnel, err := scanTunnel(row)
	if err != nil {
		return model.Tunnel{}, err
	}
	if err := s.loadTunnelStages(ctx, &tunnel); err != nil {
		return model.Tunnel{}, err
	}
	return tunnel, nil
}

func (s *Store) DeleteTunnel(ctx context.Context, id string, force bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTx(tx)
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM forwards WHERE tunnel_id = ?`, id).Scan(&count); err != nil {
		return 0, err
	}
	if count > 0 && !force {
		return 0, errors.New("tunnel still has forwards")
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM port_allocations
		 WHERE owner_kind = 'forward'
		   AND owner_id IN (SELECT id FROM forwards WHERE tunnel_id = ?)`,
		id,
	); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM forward_targets WHERE forward_id IN (SELECT id FROM forwards WHERE tunnel_id = ?)`, id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM port_allocations
		 WHERE owner_kind = 'tunnel_stage_node'
		   AND owner_id IN (SELECT id FROM tunnel_stage_nodes WHERE tunnel_id = ?)`,
		id,
	); err != nil {
		return 0, err
	}
	if force {
		if _, err := tx.ExecContext(ctx, `DELETE FROM forwards WHERE tunnel_id = ?`, id); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tunnel_stage_nodes WHERE tunnel_id = ?`, id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tunnel_stages WHERE tunnel_id = ?`, id); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM tunnels WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return 0, errors.New("tunnel not found")
	}
	rev, err := bumpRevisionTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *Store) SetTunnelEnabled(ctx context.Context, id string, enabled bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTx(tx)
	res, err := tx.ExecContext(ctx, `UPDATE tunnels SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return 0, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return 0, errors.New("tunnel not found")
	}
	rev, err := bumpRevisionTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *Store) loadTunnelStages(ctx context.Context, tunnel *model.Tunnel) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tunnel_id, stage_index, role, strategy, tcp_strategy, udp_strategy, created_at, updated_at
		 FROM tunnel_stages WHERE tunnel_id = ? ORDER BY stage_index ASC`,
		tunnel.ID,
	)
	if err != nil {
		return err
	}
	defer closeRows(rows)
	var stages []model.TunnelStage
	for rows.Next() {
		stage, err := scanTunnelStage(rows)
		if err != nil {
			return err
		}
		stages = append(stages, stage)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for i := range stages {
		nodes, err := s.listStageNodes(ctx, stages[i].ID)
		if err != nil {
			return err
		}
		stages[i].Nodes = nodes
	}
	tunnel.Stages = stages
	return nil
}

func (s *Store) listStageNodes(ctx context.Context, stageID string) ([]model.TunnelStageNode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, tunnel_id, stage_id, node_id, listen_addr, public_addr, connect_addr, weight, protocols_json, settings_json, created_at, updated_at
		 FROM tunnel_stage_nodes WHERE stage_id = ? ORDER BY rowid ASC`,
		stageID,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []model.TunnelStageNode
	for rows.Next() {
		node, err := scanTunnelStageNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *Store) SaveForward(ctx context.Context, forward model.Forward, allocations []model.PortAllocation) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTx(tx)
	now := time.Now().UTC()
	if forward.CreatedAt.IsZero() {
		forward.CreatedAt = now
	}
	forward.UpdatedAt = now
	protocols, _ := json.Marshal(forward.Protocols)
	legacyTarget := strings.TrimSpace(forward.Target)
	if len(forward.Targets) > 0 {
		legacyTarget = strings.TrimSpace(forward.Targets[0].Address)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO forwards (id, name, tunnel_id, protocols_json, listen, target, strategy, tcp_strategy, udp_strategy, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   tunnel_id = excluded.tunnel_id,
		   protocols_json = excluded.protocols_json,
		   listen = excluded.listen,
		   target = excluded.target,
		   strategy = excluded.strategy,
		   tcp_strategy = excluded.tcp_strategy,
		   udp_strategy = excluded.udp_strategy,
		   enabled = excluded.enabled,
		   updated_at = excluded.updated_at`,
		forward.ID, forward.Name, forward.TunnelID, string(protocols), forward.Listen, legacyTarget,
		forward.Strategy, forward.TCPStrategy, forward.UDPStrategy, boolInt(forward.Enabled),
		forward.CreatedAt.UTC().Format(time.RFC3339Nano), forward.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM port_allocations WHERE owner_kind = 'forward' AND owner_id = ?`, forward.ID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM forward_targets WHERE forward_id = ?`, forward.ID); err != nil {
		return 0, err
	}
	for index, target := range forward.Targets {
		if target.Weight <= 0 {
			target.Weight = 1
		}
		if target.ID == "" {
			target.ID = fmt.Sprintf("%s:%d", forward.ID, index)
		}
		targetProtocols, _ := json.Marshal(target.Protocols)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO forward_targets
			 (id, forward_id, position, address, protocols_json, weight, enabled, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			target.ID, forward.ID, index, target.Address, string(targetProtocols), target.Weight, boolInt(target.Enabled),
			now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		); err != nil {
			return 0, err
		}
	}
	if err := insertAllocations(ctx, tx, allocations); err != nil {
		return 0, err
	}
	rev, err := bumpRevisionTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *Store) ListForwards(ctx context.Context) ([]model.Forward, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, tunnel_id, protocols_json, listen, target, strategy, tcp_strategy, udp_strategy, enabled, created_at, updated_at
		 FROM forwards ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []model.Forward
	for rows.Next() {
		forward, err := scanForward(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, forward)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadForwardTargets(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) ListForwardsByTunnel(ctx context.Context, tunnelID string) ([]model.Forward, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, tunnel_id, protocols_json, listen, target, strategy, tcp_strategy, udp_strategy, enabled, created_at, updated_at
		 FROM forwards WHERE tunnel_id = ? ORDER BY created_at DESC`,
		tunnelID,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []model.Forward
	for rows.Next() {
		forward, err := scanForward(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, forward)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.loadForwardTargets(ctx, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) GetForward(ctx context.Context, id string) (model.Forward, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, tunnel_id, protocols_json, listen, target, strategy, tcp_strategy, udp_strategy, enabled, created_at, updated_at
		 FROM forwards WHERE id = ?`, id,
	)
	forward, err := scanForward(row)
	if err != nil {
		return model.Forward{}, err
	}
	if err := s.loadForwardTargets(ctx, &forward); err != nil {
		return model.Forward{}, err
	}
	return forward, nil
}

func (s *Store) DeleteForward(ctx context.Context, id string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTx(tx)
	if _, err := tx.ExecContext(ctx, `DELETE FROM port_allocations WHERE owner_kind = 'forward' AND owner_id = ?`, id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM forward_targets WHERE forward_id = ?`, id); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM forwards WHERE id = ?`, id)
	if err != nil {
		return 0, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return 0, errors.New("forward not found")
	}
	rev, err := bumpRevisionTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *Store) SetForwardEnabled(ctx context.Context, id string, enabled bool) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTx(tx)
	res, err := tx.ExecContext(ctx, `UPDATE forwards SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return 0, err
	}
	if rows, err := res.RowsAffected(); err == nil && rows == 0 {
		return 0, errors.New("forward not found")
	}
	rev, err := bumpRevisionTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *Store) ListPortAllocations(ctx context.Context) ([]model.PortAllocation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, owner_kind, owner_id, protocol, port, bind_addr, created_at, updated_at
		 FROM port_allocations ORDER BY node_id, protocol, port`,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []model.PortAllocation
	for rows.Next() {
		allocation, err := scanPortAllocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, allocation)
	}
	return out, rows.Err()
}

func (s *Store) InsertMetrics(ctx context.Context, report model.MetricsReport) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	observed := report.ObservedAt
	if observed.IsZero() {
		observed = time.Now().UTC()
	}
	insert := func(kind string, stat model.TrafficStat) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO metrics (node_id, stat_id, stat_kind, bytes_in, bytes_out, connections, observed_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			report.NodeID, stat.ID, kind, stat.BytesIn, stat.BytesOut, stat.Connections, observed.UTC().Format(time.RFC3339Nano),
		)
		return err
	}
	for _, stat := range report.ForwardStats {
		if err := insert("forward", stat); err != nil {
			return err
		}
	}
	for _, stat := range report.TunnelStats {
		if err := insert("tunnel", stat); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddAudit(ctx context.Context, actor, action, target string, detail any) error {
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO audit (actor, action, target, detail, created_at) VALUES (?, ?, ?, ?, ?)`,
		actor, action, target, string(detailJSON), time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]model.AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, actor, action, target, detail, created_at FROM audit ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []model.AuditEvent
	for rows.Next() {
		var event model.AuditEvent
		var created string
		if err := rows.Scan(&event.ID, &event.Actor, &event.Action, &event.Target, &event.Detail, &created); err != nil {
			return nil, err
		}
		event.CreatedAt = parseTime(created)
		out = append(out, event)
	}
	return out, rows.Err()
}

type MetricSummary struct {
	StatID      string `json:"stat_id"`
	Kind        string `json:"kind"`
	BytesIn     int64  `json:"bytes_in"`
	BytesOut    int64  `json:"bytes_out"`
	Connections int64  `json:"connections"`
	LastSeen    string `json:"last_seen"`
}

func (s *Store) MetricSummary(ctx context.Context, limit int) ([]MetricSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT stat_id, stat_kind, SUM(bytes_in), SUM(bytes_out), SUM(connections), MAX(observed_at)
		 FROM metrics
		 GROUP BY stat_id, stat_kind
		 ORDER BY MAX(observed_at) DESC
		 LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var out []MetricSummary
	for rows.Next() {
		var item MetricSummary
		if err := rows.Scan(&item.StatID, &item.Kind, &item.BytesIn, &item.BytesOut, &item.Connections, &item.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) BumpRevision(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer rollbackTx(tx)
	rev, err := bumpRevisionTx(ctx, tx)
	if err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

func (s *Store) CurrentRevision(ctx context.Context) (int64, error) {
	value, ok, err := s.GetSetting(ctx, "config_revision")
	if err != nil || !ok {
		return 0, err
	}
	var rev int64
	_, _ = fmt.Sscan(value, &rev)
	return rev, nil
}

func insertAllocations(ctx context.Context, tx *sql.Tx, allocations []model.PortAllocation) error {
	now := time.Now().UTC()
	for _, allocation := range allocations {
		if allocation.CreatedAt.IsZero() {
			allocation.CreatedAt = now
		}
		allocation.UpdatedAt = now
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO port_allocations
			 (id, node_id, owner_kind, owner_id, protocol, port, bind_addr, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			allocation.ID, allocation.NodeID, allocation.OwnerKind, allocation.OwnerID, allocation.Protocol,
			allocation.Port, allocation.BindAddr, allocation.CreatedAt.UTC().Format(time.RFC3339Nano), allocation.UpdatedAt.UTC().Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
	}
	return nil
}

func bumpRevisionTx(ctx context.Context, tx *sql.Tx) (int64, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, "config_revision").Scan(&value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var rev int64
	if err == nil {
		_, _ = fmt.Sscan(value, &rev)
	}
	rev++
	_, err = tx.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		"config_revision", fmt.Sprintf("%d", rev),
	)
	return rev, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(row scanner) (model.Node, string, error) {
	var node model.Node
	var token, status, updateStatus, labelsJSON, publicHost, lastSeen, created, updated, systemJSON string
	var updateRequestedAt, updateFinishedAt string
	var portMin, portMax int
	var approved, revoked int
	err := row.Scan(
		&node.ID, &node.Name, &token, &status, &node.Version, &node.DesiredVersion, &updateStatus, &node.UpdateError,
		&updateRequestedAt, &updateFinishedAt, &labelsJSON, &publicHost, &portMin, &portMax, &approved, &revoked,
		&lastSeen, &created, &updated, &systemJSON,
	)
	if err != nil {
		return model.Node{}, "", err
	}
	node.Status = model.NodeStatus(status)
	node.UpdateStatus = model.NodeUpdateStatus(updateStatus)
	node.PublicHost = publicHost
	node.PortMin = portMin
	node.PortMax = portMax
	node.Approved = approved == 1
	node.Revoked = revoked == 1
	node.LastSeen = parseTime(lastSeen)
	node.UpdateRequestedAt = parseTime(updateRequestedAt)
	node.UpdateFinishedAt = parseTime(updateFinishedAt)
	node.CreatedAt = parseTime(created)
	node.UpdatedAt = parseTime(updated)
	_ = json.Unmarshal([]byte(labelsJSON), &node.Labels)
	_ = json.Unmarshal([]byte(systemJSON), &node.System)
	return node, token, nil
}

func scanTunnel(row scanner) (model.Tunnel, error) {
	var tunnel model.Tunnel
	var tunnelType, transport, settingsJSON, created, updated string
	var enabled int
	err := row.Scan(&tunnel.ID, &tunnel.Name, &tunnelType, &transport, &tunnel.EntryAddress, &enabled, &settingsJSON, &created, &updated)
	if err != nil {
		return model.Tunnel{}, err
	}
	tunnel.Type = model.TunnelType(tunnelType)
	tunnel.Transport = model.TunnelTransport(transport)
	tunnel.Enabled = enabled == 1
	tunnel.CreatedAt = parseTime(created)
	tunnel.UpdatedAt = parseTime(updated)
	_ = json.Unmarshal([]byte(settingsJSON), &tunnel.Settings)
	return tunnel, nil
}

func scanTunnelStage(row scanner) (model.TunnelStage, error) {
	var stage model.TunnelStage
	var role, created, updated string
	err := row.Scan(&stage.ID, &stage.TunnelID, &stage.Index, &role, &stage.Strategy, &stage.TCPStrategy, &stage.UDPStrategy, &created, &updated)
	if err != nil {
		return model.TunnelStage{}, err
	}
	stage.Role = model.TunnelStageRole(role)
	stage.CreatedAt = parseTime(created)
	stage.UpdatedAt = parseTime(updated)
	return stage, nil
}

func scanTunnelStageNode(row scanner) (model.TunnelStageNode, error) {
	var node model.TunnelStageNode
	var protocolsJSON, settingsJSON, created, updated string
	err := row.Scan(&node.ID, &node.TunnelID, &node.StageID, &node.NodeID, &node.ListenAddr, &node.PublicAddr,
		&node.ConnectAddr, &node.Weight, &protocolsJSON, &settingsJSON, &created, &updated)
	if err != nil {
		return model.TunnelStageNode{}, err
	}
	node.CreatedAt = parseTime(created)
	node.UpdatedAt = parseTime(updated)
	_ = json.Unmarshal([]byte(protocolsJSON), &node.Protocols)
	_ = json.Unmarshal([]byte(settingsJSON), &node.Settings)
	return node, nil
}

func scanForward(row scanner) (model.Forward, error) {
	var forward model.Forward
	var protocolsJSON, created, updated string
	var enabled int
	err := row.Scan(&forward.ID, &forward.Name, &forward.TunnelID, &protocolsJSON, &forward.Listen, &forward.Target,
		&forward.Strategy, &forward.TCPStrategy, &forward.UDPStrategy, &enabled, &created, &updated)
	if err != nil {
		return model.Forward{}, err
	}
	forward.Enabled = enabled == 1
	forward.CreatedAt = parseTime(created)
	forward.UpdatedAt = parseTime(updated)
	_ = json.Unmarshal([]byte(protocolsJSON), &forward.Protocols)
	return forward, nil
}

func (s *Store) loadForwardTargets(ctx context.Context, forward *model.Forward) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, forward_id, position, address, protocols_json, weight, enabled
		 FROM forward_targets WHERE forward_id = ? ORDER BY position ASC, rowid ASC`,
		forward.ID,
	)
	if err != nil {
		return err
	}
	defer closeRows(rows)
	var targets []model.ForwardTarget
	for rows.Next() {
		var target model.ForwardTarget
		var protocolsJSON string
		var enabled int
		if err := rows.Scan(&target.ID, &target.ForwardID, &target.Position, &target.Address, &protocolsJSON, &target.Weight, &enabled); err != nil {
			return err
		}
		target.Enabled = enabled == 1
		_ = json.Unmarshal([]byte(protocolsJSON), &target.Protocols)
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(targets) == 0 && strings.TrimSpace(forward.Target) != "" {
		targets = []model.ForwardTarget{{
			ID:        "legacy:" + forward.ID,
			ForwardID: forward.ID,
			Address:   forward.Target,
			Enabled:   true,
			Position:  0,
		}}
	}
	for i := range targets {
		targets[i].Position = i
	}
	forward.Targets = targets
	return nil
}

func scanPortAllocation(row scanner) (model.PortAllocation, error) {
	var allocation model.PortAllocation
	var created, updated string
	err := row.Scan(&allocation.ID, &allocation.NodeID, &allocation.OwnerKind, &allocation.OwnerID, &allocation.Protocol,
		&allocation.Port, &allocation.BindAddr, &created, &updated)
	if err != nil {
		return model.PortAllocation{}, err
	}
	allocation.CreatedAt = parseTime(created)
	allocation.UpdatedAt = parseTime(updated)
	return allocation, nil
}

func normalizeNodePorts(node *model.Node) {
	if node.PortMin <= 0 && node.PortMax <= 0 {
		node.PortMin = 10000
		node.PortMax = 65535
		return
	}
	if node.PortMin <= 0 {
		node.PortMin = 10000
	}
	if node.PortMax <= 0 {
		node.PortMax = 65535
	}
}

func parseTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func emptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func closeDB(db *sql.DB) {
	_ = db.Close()
}

func closeRows(rows *sql.Rows) {
	_ = rows.Close()
}

func rollbackTx(tx *sql.Tx) {
	_ = tx.Rollback()
}

func (s *Store) PruneHistory(ctx context.Context, metricsBefore, auditBefore time.Time) (metricsDeleted, auditDeleted int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer rollbackTx(tx)

	if !metricsBefore.IsZero() {
		result, err := tx.ExecContext(ctx,
			`DELETE FROM metrics WHERE observed_at < ?`,
			metricsBefore.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return 0, 0, err
		}
		metricsDeleted, _ = result.RowsAffected()
	}
	if !auditBefore.IsZero() {
		result, err := tx.ExecContext(ctx,
			`DELETE FROM audit WHERE created_at < ?`,
			auditBefore.UTC().Format(time.RFC3339Nano),
		)
		if err != nil {
			return 0, 0, err
		}
		auditDeleted, _ = result.RowsAffected()
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	if metricsDeleted > 0 || auditDeleted > 0 {
		if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE);`); err != nil {
			return metricsDeleted, auditDeleted, fmt.Errorf("checkpoint sqlite WAL: %w", err)
		}
	}
	return metricsDeleted, auditDeleted, nil
}
