package cron

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"renstra/internal/store"
)

type Job struct {
	ID       int64
	ChatID   int64
	Schedule string
	Prompt   string
}

type scheduleSpec struct {
	minutes map[int]bool
	hours   map[int]bool
	doms    map[int]bool
	months  map[int]bool
	dows    map[int]bool
	domStar bool
	dowStar bool
}

// NextAfter menghitung waktu eksekusi berikutnya setelah `from`.
// Mendukung format cron 5 field: min (0-59), hour (0-23), dom (1-31), month (1-12), dow (0-6).
// Mendukung *, daftar 1,2, rentang 1-5, step */5 dan 0-30/5, serta @hourly, @daily, @weekly.
func NextAfter(schedule string, from time.Time, loc *time.Location) (time.Time, error) {
	if loc == nil {
		loc = from.Location()
		if loc == nil {
			loc = time.Local
		}
	}
	spec, err := parseSchedule(schedule)
	if err != nil {
		return time.Time{}, err
	}

	t := from.In(loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)

	for t.Before(limit) {
		if !spec.months[int(t.Month())] {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}

		var dayMatch bool
		if spec.domStar && spec.dowStar {
			dayMatch = true
		} else if !spec.domStar && !spec.dowStar {
			dayMatch = spec.doms[t.Day()] || spec.dows[int(t.Weekday())]
		} else if !spec.domStar {
			dayMatch = spec.doms[t.Day()]
		} else {
			dayMatch = spec.dows[int(t.Weekday())]
		}

		if !dayMatch {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}

		if !spec.hours[t.Hour()] {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			continue
		}

		if !spec.minutes[t.Minute()] {
			t = t.Add(time.Minute)
			continue
		}

		return t, nil
	}
	return time.Time{}, fmt.Errorf("tidak ada waktu yang cocok dalam rentang 5 tahun untuk %q", schedule)
}

func parseSchedule(schedule string) (*scheduleSpec, error) {
	s := strings.TrimSpace(schedule)
	switch s {
	case "@hourly":
		s = "0 * * * *"
	case "@daily":
		s = "0 0 * * *"
	case "@weekly":
		s = "0 0 * * 0"
	default:
		if strings.HasPrefix(s, "@") {
			return nil, fmt.Errorf("shorthand cron tidak didukung: %s", s)
		}
	}

	fields := strings.Fields(s)
	if len(fields) != 5 {
		return nil, fmt.Errorf("format cron harus memiliki 5 field (dapat %d): %q", len(fields), s)
	}

	mins, _, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("field minute tidak valid: %w", err)
	}
	hours, _, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("field hour tidak valid: %w", err)
	}
	doms, domStar, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("field day-of-month tidak valid: %w", err)
	}
	months, _, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("field month tidak valid: %w", err)
	}
	dows, dowStar, err := parseField(fields[4], 0, 7)
	if err != nil {
		return nil, fmt.Errorf("field day-of-week tidak valid: %w", err)
	}
	if dows[7] {
		dows[0] = true
		delete(dows, 7)
	}

	return &scheduleSpec{
		minutes: mins,
		hours:   hours,
		doms:    doms,
		months:  months,
		dows:    dows,
		domStar: domStar,
		dowStar: dowStar,
	}, nil
}

func parseField(f string, minVal, maxVal int) (map[int]bool, bool, error) {
	res := make(map[int]bool)
	if f == "*" {
		for i := minVal; i <= maxVal; i++ {
			res[i] = true
		}
		return res, true, nil
	}

	parts := strings.Split(f, ",")
	for _, part := range parts {
		if part == "" {
			return nil, false, errors.New("entri kosong pada daftar")
		}
		if part == "*" {
			for i := minVal; i <= maxVal; i++ {
				res[i] = true
			}
			continue
		}
		if strings.HasPrefix(part, "*/") {
			stepStr := strings.TrimPrefix(part, "*/")
			step, err := strconv.Atoi(stepStr)
			if err != nil || step <= 0 {
				return nil, false, fmt.Errorf("step tidak valid: %s", part)
			}
			for i := minVal; i <= maxVal; i += step {
				res[i] = true
			}
			continue
		}
		if strings.Contains(part, "/") {
			sub := strings.Split(part, "/")
			if len(sub) != 2 {
				return nil, false, fmt.Errorf("format step tidak valid: %s", part)
			}
			step, err := strconv.Atoi(sub[1])
			if err != nil || step <= 0 {
				return nil, false, fmt.Errorf("step tidak valid: %s", part)
			}
			if strings.Contains(sub[0], "-") {
				rg := strings.Split(sub[0], "-")
				if len(rg) != 2 {
					return nil, false, fmt.Errorf("format rentang tidak valid: %s", sub[0])
				}
				start, err1 := strconv.Atoi(rg[0])
				end, err2 := strconv.Atoi(rg[1])
				if err1 != nil || err2 != nil || start < minVal || end > maxVal || start > end {
					return nil, false, fmt.Errorf("rentang tidak valid: %s", sub[0])
				}
				for i := start; i <= end; i += step {
					res[i] = true
				}
			} else {
				start, err := strconv.Atoi(sub[0])
				if err != nil || start < minVal || start > maxVal {
					return nil, false, fmt.Errorf("nilai awal tidak valid: %s", sub[0])
				}
				for i := start; i <= maxVal; i += step {
					res[i] = true
				}
			}
			continue
		}
		if strings.Contains(part, "-") {
			rg := strings.Split(part, "-")
			if len(rg) != 2 {
				return nil, false, fmt.Errorf("format rentang tidak valid: %s", part)
			}
			start, err1 := strconv.Atoi(rg[0])
			end, err2 := strconv.Atoi(rg[1])
			if err1 != nil || err2 != nil || start < minVal || end > maxVal || start > end {
				return nil, false, fmt.Errorf("rentang tidak valid: %s", part)
			}
			for i := start; i <= end; i++ {
				res[i] = true
			}
			continue
		}
		val, err := strconv.Atoi(part)
		if err != nil || val < minVal || val > maxVal {
			return nil, false, fmt.Errorf("nilai diluar rentang [%d..%d]: %s", minVal, maxVal, part)
		}
		res[val] = true
	}

	return res, false, nil
}

func parseTime(s string, loc *time.Location) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		time.RFC3339Nano,
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("format waktu tidak dikenal: %s", s)
}

// Run menjalankan loop cron. Setiap interval, membaca pekerjaan aktif di tabel `jobs`
// yang `next_run_at <= now`, memanggil `fire` tepat sekali (tanpa backfill slot yang terlewat),
// lalu memperbarui `next_run_at` dan `last_run_at`. Berhenti saat ctx.Done().
func Run(ctx context.Context, st *store.Store, loc *time.Location, fire func(context.Context, Job), interval time.Duration) error {
	if loc == nil {
		loc = time.Local
	}
	if interval <= 0 {
		interval = time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	tick := func() {
		now := time.Now().In(loc)
		rows, err := st.DB().QueryContext(ctx, `SELECT id, chat_id, schedule, prompt, next_run_at FROM jobs WHERE enabled = 1`)
		if err != nil {
			return
		}

		type candidate struct {
			job       Job
			needsInit bool
		}
		var ready []candidate

		for rows.Next() {
			var j Job
			var nextStr sql.NullString
			if err := rows.Scan(&j.ID, &j.ChatID, &j.Schedule, &j.Prompt, &nextStr); err != nil {
				continue
			}

			if !nextStr.Valid || strings.TrimSpace(nextStr.String) == "" {
				ready = append(ready, candidate{job: j, needsInit: true})
				continue
			}

			tRun, err := parseTime(nextStr.String, loc)
			if err != nil {
				ready = append(ready, candidate{job: j, needsInit: true})
				continue
			}

			if !tRun.After(now) {
				ready = append(ready, candidate{job: j})
			}
		}
		rows.Close()

		for _, c := range ready {
			if ctx.Err() != nil {
				return
			}
			if c.needsInit {
				next, err := NextAfter(c.job.Schedule, now, loc)
				if err == nil {
					st.DB().ExecContext(ctx, `UPDATE jobs SET next_run_at = ? WHERE id = ?`, next.Format(time.RFC3339), c.job.ID)
				}
				continue
			}

			fire(ctx, c.job)

			next, err := NextAfter(c.job.Schedule, now, loc)
			nowStr := now.Format(time.RFC3339)
			if err != nil {
				st.DB().ExecContext(ctx, `UPDATE jobs SET last_run_at = ? WHERE id = ?`, nowStr, c.job.ID)
			} else {
				st.DB().ExecContext(ctx, `UPDATE jobs SET next_run_at = ?, last_run_at = ? WHERE id = ?`, next.Format(time.RFC3339), nowStr, c.job.ID)
			}
		}
	}

	tick()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			tick()
		}
	}
}
