package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"renstra/internal/provider"
	"renstra/internal/store"
)

func migrateStateDB(ctx context.Context, src string, opts Options, rep *Report) error {
	dbSrc := filepath.Join(src, "state.db")
	if _, err := os.Stat(dbSrc); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	dsn := "file:" + filepath.ToSlash(dbSrc) + "?mode=ro&_pragma=busy_timeout(3000)"
	srcDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("membuka state.db Hermes: %w", err)
	}
	defer srcDB.Close()

	if !hasTable(ctx, srcDB, "sessions") || !hasTable(ctx, srcDB, "messages") {
		rep.Warnings = append(rep.Warnings, "state.db Hermes tidak memiliki tabel sessions/messages; dilewati")
		return nil
	}

	sessCols := tableColumns(ctx, srcDB, "sessions")
	msgCols := tableColumns(ctx, srcDB, "messages")

	type hermesSession struct {
		ID       string
		Platform string
		UserID   string
	}
	sessQuery := buildSessionQuery(sessCols)
	sRows, err := srcDB.QueryContext(ctx, sessQuery)
	if err != nil {
		return fmt.Errorf("membaca tabel sessions Hermes: %w", err)
	}
	var sessions []hermesSession
	for sRows.Next() {
		var hs hermesSession
		if err := sRows.Scan(&hs.ID, &hs.Platform, &hs.UserID); err != nil {
			continue
		}
		if hs.ID != "" {
			sessions = append(sessions, hs)
		}
	}
	sRows.Close()

	var dstStore *store.Store
	if !opts.DryRun {
		dstStore, err = store.Open(filepath.Join(opts.ToDir, "state.db"))
		if err != nil {
			return fmt.Errorf("membuka state.db tujuan: %w", err)
		}
		defer dstStore.Close()
	}

	msgQuery := buildMessageQuery(msgCols)
	for _, sess := range sessions {
		kvKey := "migrated_hermes_session:" + sess.ID
		if !opts.DryRun && !opts.Force {
			var existing string
			if err := dstStore.DB().QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, kvKey).Scan(&existing); err == nil {
				rep.SessionsSkipped++
				continue
			}
		}

		mRows, err := srcDB.QueryContext(ctx, msgQuery, sess.ID)
		if err != nil {
			continue
		}

		var msgs []provider.Message
		for mRows.Next() {
			var role, content, toolCallsRaw, toolCallID string
			if err := mRows.Scan(&role, &content, &toolCallsRaw, &toolCallID); err != nil {
				continue
			}
			r := normalizeRole(role)
			if r == "" {
				continue
			}
			m := provider.Message{
				Role:       r,
				Content:    content,
				ToolCallID: toolCallID,
			}
			if toolCallsRaw != "" && toolCallsRaw != "null" && toolCallsRaw != "[]" {
				_ = json.Unmarshal([]byte(toolCallsRaw), &m.ToolCalls)
			}
			msgs = append(msgs, m)
		}
		mRows.Close()

		if len(msgs) == 0 {
			continue
		}

		chatID := mapChatID(sess.Platform, sess.UserID)
		if !opts.DryRun {
			existingMsgs, _ := dstStore.Messages(ctx, chatID)
			if len(existingMsgs) > 0 {
				_ = dstStore.SessionBreak(ctx, chatID)
			}
			for _, m := range msgs {
				if _, err := dstStore.AppendMessage(ctx, chatID, m); err != nil {
					return fmt.Errorf("menyimpan pesan sesi %s: %w", sess.ID, err)
				}
			}
			_, _ = dstStore.DB().ExecContext(ctx, `INSERT OR REPLACE INTO kv(key, value) VALUES(?, '1')`, kvKey)
		}
		rep.SessionsCopied++
		rep.MessagesCopied += len(msgs)
	}

	return nil
}

func hasTable(ctx context.Context, db *sql.DB, name string) bool {
	var c int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&c); err != nil {
		return false
	}
	return c > 0
}

func tableColumns(ctx context.Context, db *sql.DB, table string) map[string]bool {
	cols := map[string]bool{}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return cols
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err == nil {
			cols[strings.ToLower(name)] = true
		}
	}
	return cols
}

func buildSessionQuery(cols map[string]bool) string {
	idCol := "''"
	if cols["session_id"] {
		idCol = "COALESCE(session_id,'')"
	} else if cols["id"] {
		idCol = "CAST(id AS TEXT)"
	}
	platCol := "''"
	if cols["source_platform"] {
		platCol = "COALESCE(source_platform,'')"
	} else if cols["platform"] {
		platCol = "COALESCE(platform,'')"
	}
	userCol := "''"
	if cols["user_id"] {
		userCol = "COALESCE(CAST(user_id AS TEXT),'')"
	}
	orderCol := "rowid"
	if cols["started_at"] {
		orderCol = "started_at, rowid"
	}
	return fmt.Sprintf("SELECT %s, %s, %s FROM sessions ORDER BY %s", idCol, platCol, userCol, orderCol)
}

func buildMessageQuery(cols map[string]bool) string {
	roleCol := "COALESCE(role,'')"
	contentCol := "COALESCE(content,'')"
	callsCol := "''"
	if cols["tool_calls_json"] {
		callsCol = "COALESCE(tool_calls_json,'')"
	} else if cols["tool_calls"] {
		callsCol = "COALESCE(tool_calls,'')"
	}
	callIDCol := "''"
	if cols["tool_call_id"] {
		callIDCol = "COALESCE(tool_call_id,'')"
	}
	orderCol := "rowid"
	if cols["timestamp"] {
		orderCol = "timestamp, rowid"
	} else if cols["created_at"] {
		orderCol = "created_at, rowid"
	}
	return fmt.Sprintf("SELECT %s, %s, %s, %s FROM messages WHERE session_id = ? ORDER BY %s",
		roleCol, contentCol, callsCol, callIDCol, orderCol)
}

func normalizeRole(r string) provider.Role {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "user":
		return provider.RoleUser
	case "assistant", "model":
		return provider.RoleAssistant
	case "tool":
		return provider.RoleTool
	case "system":
		return provider.RoleSystem
	default:
		return ""
	}
}

func mapChatID(platform, userID string) int64 {
	if strings.EqualFold(strings.TrimSpace(platform), "telegram") {
		if id, err := strconv.ParseInt(strings.TrimSpace(userID), 10, 64); err == nil && id != 0 {
			return id
		}
	}
	return 1
}
