package store

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	reNamedTopic = regexp.MustCompile(`Topik:\s*"([^"]+)"\s*\(#(\d+)\)`)
	reBareTopic  = regexp.MustCompile(`Topik\s*#(\d+)`)
)

// BackupArchive writes a consistent tar.gz snapshot of state.db (via VACUUM INTO),
// config.yaml, memory/, and skills/ from homeDir into w.
func (s *Store) BackupArchive(ctx context.Context, homeDir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	if s != nil && s.db != nil {
		tmpDB := filepath.Join(os.TempDir(), fmt.Sprintf("renstra-backup-%d.db", time.Now().UnixNano()))
		defer os.Remove(tmpDB)
		escaped := strings.ReplaceAll(filepath.ToSlash(tmpDB), "'", "''")
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", escaped)); err == nil {
			_ = addFileToTar(tw, tmpDB, "state.db")
		} else if homeDir != "" {
			_ = addFileToTar(tw, filepath.Join(homeDir, "state.db"), "state.db")
		}
	}

	if strings.TrimSpace(homeDir) == "" {
		return nil
	}
	_ = addFileToTar(tw, filepath.Join(homeDir, "config.yaml"), "config.yaml")
	_ = addFileToTar(tw, filepath.Join(homeDir, "mcp.yaml"), "mcp.yaml")
	for _, sub := range []string{"memory", "skills"} {
		root := filepath.Join(homeDir, sub)
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(homeDir, path)
			if rerr != nil {
				return nil
			}
			return addFileToTar(tw, path, filepath.ToSlash(rel))
		})
	}
	return nil
}

func addFileToTar(tw *tar.Writer, srcPath, arcName string) error {
	st, err := os.Stat(srcPath)
	if err != nil || st.IsDir() {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	hdr := &tar.Header{
		Name:    arcName,
		Mode:    0o600,
		Size:    st.Size(),
		ModTime: st.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// HarvestTopicsFromHistory scans stored user messages for "[... | Topik: "Name" (#ID) | ...]"
// headers so forum topics active prior to v1.3.2 are automatically discovered.
func (s *Store) HarvestTopicsFromHistory(ctx context.Context) map[int64]string {
	out := make(map[int64]string)
	if s == nil || s.db == nil {
		return out
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT content FROM messages WHERE role = 'user' AND content LIKE '%Topik%' ORDER BY id DESC LIMIT 2000`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			continue
		}
		if m := reNamedTopic.FindStringSubmatch(content); len(m) == 3 {
			if tid, err := strconv.ParseInt(m[2], 10, 64); err == nil && tid > 0 {
				if out[tid] == "" {
					out[tid] = strings.TrimSpace(m[1])
				}
				continue
			}
		}
		if m := reBareTopic.FindStringSubmatch(content); len(m) == 2 {
			if tid, err := strconv.ParseInt(m[1], 10, 64); err == nil && tid > 0 {
				if _, exists := out[tid]; !exists {
					out[tid] = ""
				}
			}
		}
	}
	return out
}

// RestoreArchive safely extracts state.db, config.yaml, mcp.yaml, memory/, and skills/
// from a backup tar.gz reader into homeDir, rejecting path traversal entries.
func RestoreArchive(homeDir string, r io.Reader) ([]string, error) {
	if strings.TrimSpace(homeDir) == "" {
		return nil, fmt.Errorf("homeDir wajib diisi")
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("arsip bukan gzip valid: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var restored []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return restored, err
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(strings.TrimLeft(hdr.Name, "/")))
		if !isAllowedRestorePath(clean) {
			continue
		}
		dst := filepath.Join(homeDir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return restored, err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return restored, err
		}
		_, cerr := io.Copy(f, io.LimitReader(tr, 200*1024*1024))
		_ = f.Close()
		if cerr != nil {
			return restored, cerr
		}
		restored = append(restored, clean)
	}
	return restored, nil
}

func isAllowedRestorePath(clean string) bool {
	if clean == "." || strings.HasPrefix(clean, "..") || strings.Contains(clean, "../") {
		return false
	}
	switch clean {
	case "state.db", "config.yaml", "mcp.yaml":
		return true
	}
	return strings.HasPrefix(clean, "memory/") || strings.HasPrefix(clean, "skills/")
}
