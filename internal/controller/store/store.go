package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	_ "modernc.org/sqlite"

	"nyarelay/internal/shared/model"
)

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
		_ = db.Close()
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
		`CREATE TABLE IF NOT EXISTS links (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			from_node TEXT NOT NULL,
			to_node TEXT NOT NULL,
			bind_addr TEXT NOT NULL,
			public_addr TEXT NOT NULL,
			server_name TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL,
			settings_json TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS routes (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			protocol TEXT NOT NULL,
			entry_node TEXT NOT NULL,
			listen TEXT NOT NULL,
			target TEXT NOT NULL,
			enabled INTEGER NOT NULL,
			hops_json TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
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
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return s.ensureNodeColumns(ctx)
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
	return nil
}

func (s *Store) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	defer rows.Close()
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
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO nodes
		 (id, name, token, status, version, labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   status = excluded.status,
		   version = excluded.version,
		   labels_json = excluded.labels_json,
		   public_host = excluded.public_host,
		   port_min = excluded.port_min,
		   port_max = excluded.port_max,
		   approved = excluded.approved,
		   revoked = excluded.revoked,
		   last_seen = excluded.last_seen,
		   updated_at = excluded.updated_at,
		   system_json = excluded.system_json`,
		node.ID, node.Name, token, string(node.Status), node.Version, string(labels), node.PublicHost, node.PortMin, node.PortMax,
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
		`SELECT id, name, token, status, version, labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json
		 FROM nodes WHERE id = ?`, id,
	)
	return scanNode(row)
}

func (s *Store) ListNodes(ctx context.Context) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, token, status, version, labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json
		 FROM nodes WHERE revoked = 0 ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
		`SELECT id, name, token, status, version, labels_json, public_host, port_min, port_max, approved, revoked, last_seen, created_at, updated_at, system_json
		 FROM nodes WHERE id = ?`, id,
	)
	node, _, err := scanNode(row)
	return node, err
}

func (s *Store) MarkNodeSeen(ctx context.Context, id string, system model.NodeSystem, version string) error {
	now := time.Now().UTC()
	systemJSON, _ := json.Marshal(system)
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET status = ?, version = ?, last_seen = ?, updated_at = ?, system_json = ? WHERE id = ? AND revoked = 0`,
		string(model.NodeOnline), version, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), string(systemJSON), id,
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

func (s *Store) UpsertLink(ctx context.Context, link model.Link) error {
	now := time.Now().UTC()
	if link.CreatedAt.IsZero() {
		link.CreatedAt = now
	}
	link.UpdatedAt = now
	settings, _ := json.Marshal(emptyMap(link.Settings))
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO links
		 (id, name, type, from_node, to_node, bind_addr, public_addr, server_name, enabled, settings_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   type = excluded.type,
		   from_node = excluded.from_node,
		   to_node = excluded.to_node,
		   bind_addr = excluded.bind_addr,
		   public_addr = excluded.public_addr,
		   server_name = excluded.server_name,
		   enabled = excluded.enabled,
		   settings_json = excluded.settings_json,
		   updated_at = excluded.updated_at`,
		link.ID, link.Name, string(link.Type), link.FromNode, link.ToNode, link.BindAddr, link.PublicAddr,
		link.ServerName, boolInt(link.Enabled), string(settings),
		link.CreatedAt.UTC().Format(time.RFC3339Nano), link.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListLinks(ctx context.Context) ([]model.Link, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, type, from_node, to_node, bind_addr, public_addr, server_name, enabled, settings_json, created_at, updated_at
		 FROM links ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Link
	for rows.Next() {
		link, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (s *Store) GetLink(ctx context.Context, id string) (model.Link, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, type, from_node, to_node, bind_addr, public_addr, server_name, enabled, settings_json, created_at, updated_at
		 FROM links WHERE id = ?`, id,
	)
	return scanLink(row)
}

func (s *Store) UpsertRoute(ctx context.Context, route model.Route) error {
	now := time.Now().UTC()
	if route.CreatedAt.IsZero() {
		route.CreatedAt = now
	}
	route.UpdatedAt = now
	hops, _ := json.Marshal(route.Hops)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO routes
		 (id, name, protocol, entry_node, listen, target, enabled, hops_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name,
		   protocol = excluded.protocol,
		   entry_node = excluded.entry_node,
		   listen = excluded.listen,
		   target = excluded.target,
		   enabled = excluded.enabled,
		   hops_json = excluded.hops_json,
		   updated_at = excluded.updated_at`,
		route.ID, route.Name, string(route.Protocol), route.EntryNode, route.Listen, route.Target,
		boolInt(route.Enabled), string(hops), route.CreatedAt.UTC().Format(time.RFC3339Nano),
		route.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (s *Store) ListRoutes(ctx context.Context) ([]model.Route, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, protocol, entry_node, listen, target, enabled, hops_json, created_at, updated_at
		 FROM routes ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Route
	for rows.Next() {
		route, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, route)
	}
	return out, rows.Err()
}

func (s *Store) NodePortUsage(ctx context.Context, nodeID, excludeRouteID string) (map[string]bool, error) {
	used := make(map[string]bool)
	rows, err := s.db.QueryContext(ctx, `SELECT id, listen FROM routes WHERE enabled = 1 AND entry_node = ?`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, listen string
		if err := rows.Scan(&id, &listen); err != nil {
			return nil, err
		}
		if excludeRouteID != "" && id == excludeRouteID {
			continue
		}
		if _, port, err := net.SplitHostPort(listen); err == nil {
			used[port] = true
		}
	}
	linkRows, err := s.db.QueryContext(ctx, `SELECT bind_addr FROM links WHERE enabled = 1 AND to_node = ?`, nodeID)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()
	for linkRows.Next() {
		var bindAddr string
		if err := linkRows.Scan(&bindAddr); err != nil {
			return nil, err
		}
		if _, port, err := net.SplitHostPort(bindAddr); err == nil {
			used[port] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return used, linkRows.Err()
}

func (s *Store) GetRoute(ctx context.Context, id string) (model.Route, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, protocol, entry_node, listen, target, enabled, hops_json, created_at, updated_at
		 FROM routes WHERE id = ?`, id,
	)
	return scanRoute(row)
}

func (s *Store) InsertMetrics(ctx context.Context, report model.MetricsReport) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	for _, stat := range report.RouteStats {
		if err := insert("route", stat); err != nil {
			return err
		}
	}
	for _, stat := range report.LinkStats {
		if err := insert("link", stat); err != nil {
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
	defer rows.Close()
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
	defer rows.Close()
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
	value, ok, err := s.GetSetting(ctx, "config_revision")
	if err != nil {
		return 0, err
	}
	var rev int64
	if ok {
		_, _ = fmt.Sscan(value, &rev)
	}
	rev++
	if err := s.SetSetting(ctx, "config_revision", fmt.Sprintf("%d", rev)); err != nil {
		return 0, err
	}
	return rev, nil
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

type scanner interface {
	Scan(dest ...any) error
}

func scanNode(row scanner) (model.Node, string, error) {
	var node model.Node
	var token, status, labelsJSON, publicHost, lastSeen, created, updated, systemJSON string
	var portMin, portMax int
	var approved, revoked int
	err := row.Scan(&node.ID, &node.Name, &token, &status, &node.Version, &labelsJSON, &publicHost, &portMin, &portMax, &approved, &revoked, &lastSeen, &created, &updated, &systemJSON)
	if err != nil {
		return model.Node{}, "", err
	}
	node.Status = model.NodeStatus(status)
	node.PublicHost = publicHost
	node.PortMin = portMin
	node.PortMax = portMax
	node.Approved = approved == 1
	node.Revoked = revoked == 1
	node.LastSeen = parseTime(lastSeen)
	node.CreatedAt = parseTime(created)
	node.UpdatedAt = parseTime(updated)
	_ = json.Unmarshal([]byte(labelsJSON), &node.Labels)
	_ = json.Unmarshal([]byte(systemJSON), &node.System)
	return node, token, nil
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

func scanLink(row scanner) (model.Link, error) {
	var link model.Link
	var linkType, settingsJSON, created, updated string
	var enabled int
	err := row.Scan(&link.ID, &link.Name, &linkType, &link.FromNode, &link.ToNode, &link.BindAddr,
		&link.PublicAddr, &link.ServerName, &enabled, &settingsJSON, &created, &updated)
	if err != nil {
		return model.Link{}, err
	}
	link.Type = model.LinkType(linkType)
	link.Enabled = enabled == 1
	link.CreatedAt = parseTime(created)
	link.UpdatedAt = parseTime(updated)
	_ = json.Unmarshal([]byte(settingsJSON), &link.Settings)
	return link, nil
}

func scanRoute(row scanner) (model.Route, error) {
	var route model.Route
	var protocol, hopsJSON, created, updated string
	var enabled int
	err := row.Scan(&route.ID, &route.Name, &protocol, &route.EntryNode, &route.Listen, &route.Target, &enabled, &hopsJSON, &created, &updated)
	if err != nil {
		return model.Route{}, err
	}
	route.Protocol = model.RouteProtocol(protocol)
	route.Enabled = enabled == 1
	route.CreatedAt = parseTime(created)
	route.UpdatedAt = parseTime(updated)
	_ = json.Unmarshal([]byte(hopsJSON), &route.Hops)
	return route, nil
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
