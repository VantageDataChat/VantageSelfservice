package auth

import (
	"database/sql"
	"fmt"
	"sync"
	"time"
)

// LoginLimiter tracks failed admin login attempts and enforces lockout policies:
//   - 10 consecutive failures → lock for 1 hour
//   - 50 failures in a day → lock for the rest of the day
//   - 100 consecutive failures → lock IP for 10 days
type LoginLimiter struct {
	readDB  *sql.DB
	writeDB *sql.DB
	mu      sync.Mutex
}

// NewLoginLimiter creates a LoginLimiter backed by the given database.
// For backward compatibility, if only one DB is provided, it is used for both reads and writes.
func NewLoginLimiter(db *sql.DB) *LoginLimiter {
	return &LoginLimiter{readDB: db, writeDB: db}
}

// NewLoginLimiterRW creates a LoginLimiter with separate read and write database pools.
func NewLoginLimiterRW(readDB, writeDB *sql.DB) *LoginLimiter {
	return &LoginLimiter{readDB: readDB, writeDB: writeDB}
}

// CheckAllowed returns nil if the login attempt is allowed, or an error describing the lockout.
func (ll *LoginLimiter) CheckAllowed(username, ip string) error {
	// No mutex needed: all operations are read-only DB queries,
	// and SQLite WAL mode + database/sql handle concurrency safely.

	now := time.Now().UTC()

	// Check manual bans first
	var manualBanReason string
	err := ll.readDB.QueryRow(
		`SELECT reason FROM login_bans WHERE (username = ? OR ip = ?) AND unlocks_at > ? LIMIT 1`,
		username, ip, now.Format(time.RFC3339),
	).Scan(&manualBanReason)
	if err == nil && manualBanReason != "" {
		return fmt.Errorf("%s", manualBanReason)
	}

	// Rule 3: IP locked for 10 days after 100 consecutive failures
	var ipConsec int
	err = ll.readDB.QueryRow(
		`SELECT COUNT(*) FROM login_attempts WHERE ip = ? AND success = 0 AND created_at > (
			SELECT COALESCE(MAX(created_at), '1970-01-01') FROM login_attempts WHERE ip = ? AND success = 1
		)`, ip, ip,
	).Scan(&ipConsec)
	if err != nil {
		return fmt.Errorf("查询登录记录失败: %w", err)
	}
	if ipConsec >= 100 {
		// Check if the 100th failure was within the last 10 days
		var lastFailStr sql.NullString
		ll.readDB.QueryRow(
			`SELECT created_at FROM login_attempts WHERE ip = ? AND success = 0 ORDER BY created_at DESC LIMIT 1 OFFSET 99`, ip,
		).Scan(&lastFailStr)
		if lastFailStr.Valid {
			if t, e := time.Parse(time.RFC3339, lastFailStr.String); e == nil {
				if now.Before(t.Add(10 * 24 * time.Hour)) {
					remaining := time.Until(t.Add(10 * 24 * time.Hour))
					days := int(remaining.Hours() / 24)
					if days < 1 {
						return fmt.Errorf("该IP已被锁定，剩余不到1天")
					}
					return fmt.Errorf("该IP已被锁定，剩余%d天", days)
				}
			}
		}
	}

	// Rule 2: 50 failures today → locked for the rest of the day
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	tomorrowStart := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	var dailyFails int
	err = ll.readDB.QueryRow(
		`SELECT COUNT(*) FROM login_attempts WHERE username = ? AND success = 0 AND created_at >= ? AND created_at < ?`,
		username, todayStart, tomorrowStart,
	).Scan(&dailyFails)
	if err != nil {
		return fmt.Errorf("查询登录记录失败: %w", err)
	}
	if dailyFails >= 50 {
		return fmt.Errorf("今日密码错误次数过多，当天禁止登录")
	}

	// Rule 1: 10 consecutive failures → lock for 1 hour
	var consecFails int
	err = ll.readDB.QueryRow(
		`SELECT COUNT(*) FROM login_attempts WHERE username = ? AND success = 0 AND created_at > (
			SELECT COALESCE(MAX(created_at), '1970-01-01') FROM login_attempts WHERE username = ? AND success = 1
		)`, username, username,
	).Scan(&consecFails)
	if err != nil {
		return fmt.Errorf("查询登录记录失败: %w", err)
	}
	if consecFails >= 10 {
		// Check if the 10th consecutive failure was within the last hour
		var tenthFailStr sql.NullString
		ll.readDB.QueryRow(
			`SELECT created_at FROM login_attempts WHERE username = ? AND success = 0 AND created_at > (
				SELECT COALESCE(MAX(created_at), '1970-01-01') FROM login_attempts WHERE username = ? AND success = 1
			) ORDER BY created_at ASC LIMIT 1`,
			username, username,
		).Scan(&tenthFailStr)
		if tenthFailStr.Valid {
			if t, e := time.Parse(time.RFC3339, tenthFailStr.String); e == nil {
				if now.Before(t.Add(1 * time.Hour)) {
					remaining := time.Until(t.Add(1 * time.Hour))
					mins := int(remaining.Minutes())
					if mins < 1 {
						return fmt.Errorf("连续密码错误过多，请稍后再试")
					}
					return fmt.Errorf("连续密码错误过多，请%d分钟后再试", mins)
				}
			}
		}
	}

	return nil
}

// RecordAttempt records a login attempt (success or failure).
func (ll *LoginLimiter) RecordAttempt(username, ip string, success bool) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	successInt := 0
	if success {
		successInt = 1
	}
	ll.writeDB.Exec(
		`INSERT INTO login_attempts (username, ip, success, created_at) VALUES (?, ?, ?, ?)`,
		username, ip, successInt, time.Now().UTC().Format(time.RFC3339),
	)
}

// CleanOld removes login attempt records older than 30 days.
func (ll *LoginLimiter) CleanOld() {
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	ll.writeDB.Exec(`DELETE FROM login_attempts WHERE created_at < ?`, cutoff)
}

// BanEntry represents a banned username or IP for display in the admin UI.
type BanEntry struct {
	Type       string `json:"type"`        // "user_consecutive", "user_daily", "ip"
	Username   string `json:"username"`
	IP         string `json:"ip"`
	FailCount  int    `json:"fail_count"`
	Reason     string `json:"reason"`
	UnlocksAt  string `json:"unlocks_at"`
	IsManual   bool   `json:"is_manual"`
}

// ListBans returns all currently active login bans (user-level and IP-level).
func (ll *LoginLimiter) ListBans() []BanEntry {
	// No mutex needed: all operations are read-only DB queries.

	now := time.Now().UTC()
	var bans []BanEntry

	// Manual bans
	rows, err := ll.readDB.Query(`SELECT username, ip, reason, unlocks_at FROM login_bans WHERE unlocks_at > ?`, now.Format(time.RFC3339))
	if err == nil {
		for rows.Next() {
			var b BanEntry
			var unlocks string
			if err := rows.Scan(&b.Username, &b.IP, &b.Reason, &unlocks); err != nil {
				continue
			}
			b.Type = "manual"
			b.UnlocksAt = unlocks
			b.IsManual = true
			if b.Username != "" {
				b.Type = "manual_user"
			} else {
				b.Type = "manual_ip"
			}
			bans = append(bans, b)
		}
		rows.Close()
	}

	// Rule 1: users with >=10 consecutive failures (locked 1 hour)
	userRows, err := ll.readDB.Query(`
		SELECT username, COUNT(*) as cnt FROM login_attempts
		WHERE success = 0 AND created_at > (
			SELECT COALESCE(MAX(la2.created_at), '1970-01-01') FROM login_attempts la2 WHERE la2.username = login_attempts.username AND la2.success = 1
		)
		GROUP BY username HAVING cnt >= 10
	`)
	if err == nil {
		type userFail struct {
			username string
			cnt      int
		}
		var userFails []userFail
		for userRows.Next() {
			var uf userFail
			if err := userRows.Scan(&uf.username, &uf.cnt); err != nil {
				continue
			}
			userFails = append(userFails, uf)
		}
		userRows.Close()

		for _, uf := range userFails {
			// Find the first failure in the consecutive streak
			var firstFailStr sql.NullString
			ll.readDB.QueryRow(`
				SELECT created_at FROM login_attempts WHERE username = ? AND success = 0 AND created_at > (
					SELECT COALESCE(MAX(created_at), '1970-01-01') FROM login_attempts WHERE username = ? AND success = 1
				) ORDER BY created_at ASC LIMIT 1
			`, uf.username, uf.username).Scan(&firstFailStr)
			if firstFailStr.Valid {
				if t, e := time.Parse(time.RFC3339, firstFailStr.String); e == nil {
					unlocks := t.Add(1 * time.Hour)
					if now.Before(unlocks) {
						bans = append(bans, BanEntry{
							Type:      "user_consecutive",
							Username:  uf.username,
							FailCount: uf.cnt,
							Reason:    fmt.Sprintf("连续%d次密码错误，锁定1小时", uf.cnt),
							UnlocksAt: unlocks.Format(time.RFC3339),
						})
					}
				}
			}
		}
	}

	// Rule 2: users with >=50 daily failures
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	tomorrowStart := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	dailyRows, err := ll.readDB.Query(`
		SELECT username, COUNT(*) as cnt FROM login_attempts
		WHERE success = 0 AND created_at >= ? AND created_at < ?
		GROUP BY username HAVING cnt >= 50
	`, todayStart, tomorrowStart)
	if err == nil {
		for dailyRows.Next() {
			var username string
			var cnt int
			if err := dailyRows.Scan(&username, &cnt); err != nil {
				continue
			}
			bans = append(bans, BanEntry{
				Type:      "user_daily",
				Username:  username,
				FailCount: cnt,
				Reason:    fmt.Sprintf("今日%d次密码错误，当天禁止登录", cnt),
				UnlocksAt: tomorrowStart,
			})
		}
		dailyRows.Close()
	}

	// Rule 3: IPs with >=100 consecutive failures (locked 10 days)
	ipRows, err := ll.readDB.Query(`
		SELECT ip, COUNT(*) as cnt FROM login_attempts
		WHERE success = 0 AND created_at > (
			SELECT COALESCE(MAX(la2.created_at), '1970-01-01') FROM login_attempts la2 WHERE la2.ip = login_attempts.ip AND la2.success = 1
		)
		GROUP BY ip HAVING cnt >= 100
	`)
	if err == nil {
		type ipFail struct {
			ip  string
			cnt int
		}
		var ipFails []ipFail
		for ipRows.Next() {
			var f ipFail
			if err := ipRows.Scan(&f.ip, &f.cnt); err != nil {
				continue
			}
			ipFails = append(ipFails, f)
		}
		ipRows.Close()

		for _, f := range ipFails {
			var hundredthFailStr sql.NullString
			ll.readDB.QueryRow(
				`SELECT created_at FROM login_attempts WHERE ip = ? AND success = 0 ORDER BY created_at DESC LIMIT 1 OFFSET 99`, f.ip,
			).Scan(&hundredthFailStr)
			if hundredthFailStr.Valid {
				if t, e := time.Parse(time.RFC3339, hundredthFailStr.String); e == nil {
					unlocks := t.Add(10 * 24 * time.Hour)
					if now.Before(unlocks) {
						bans = append(bans, BanEntry{
							Type:      "ip",
							IP:        f.ip,
							FailCount: f.cnt,
							Reason:    fmt.Sprintf("IP连续%d次密码错误，锁定10天", f.cnt),
							UnlocksAt: unlocks.Format(time.RFC3339),
						})
					}
				}
			}
		}
	}

	return bans
}

// Unban removes the ban for a given username or IP by inserting a synthetic success record.
// For manual bans, it deletes the ban record.
func (ll *LoginLimiter) Unban(username, ip string) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	// Remove manual bans
	if username != "" {
		ll.writeDB.Exec(`DELETE FROM login_bans WHERE username = ?`, username)
	}
	if ip != "" {
		ll.writeDB.Exec(`DELETE FROM login_bans WHERE ip = ?`, ip)
	}

	// Insert a synthetic success to reset consecutive counters
	now := time.Now().UTC().Format(time.RFC3339)
	if username != "" {
		ll.writeDB.Exec(`INSERT INTO login_attempts (username, ip, success, created_at) VALUES (?, '', 1, ?)`, username, now)
	}
	if ip != "" {
		ll.writeDB.Exec(`INSERT INTO login_attempts (username, ip, success, created_at) VALUES ('', ?, 1, ?)`, ip, now)
	}
}

// AddManualBan adds a manual ban for a username or IP until the specified time.
func (ll *LoginLimiter) AddManualBan(username, ip, reason string, duration time.Duration) {
	ll.mu.Lock()
	defer ll.mu.Unlock()

	unlocks := time.Now().UTC().Add(duration).Format(time.RFC3339)
	ll.writeDB.Exec(
		`INSERT INTO login_bans (username, ip, reason, unlocks_at, created_at) VALUES (?, ?, ?, ?, ?)`,
		username, ip, reason, unlocks, time.Now().UTC().Format(time.RFC3339),
	)
}
