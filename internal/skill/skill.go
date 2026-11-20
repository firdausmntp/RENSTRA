// Package skill memuat skill dari beberapa lapis direktori dengan aturan
// lapis teratas menang, dan mencatat nama yang bentrok antar lapis.
package skill

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Layer adalah satu direktori sumber skill. Writable menentukan apakah
// Registry.Create boleh menulis ke lapis ini.
type Layer struct {
	Dir      string
	Writable bool
}

// Skill adalah satu skill yang sudah dimuat.
type Skill struct {
	Name        string
	Description string
	Body        string
	Path        string
	Dir         string
}

// Conflict mencatat nama yang didefinisikan lebih dari satu lapis.
type Conflict struct {
	Name        string
	WinnerDir   string
	ShadowedDir string
}

type Registry struct {
	mu        sync.RWMutex
	layers    []Layer
	skills    map[string]Skill
	conflicts []Conflict
}

// Load memindai setiap lapis sesuai urutan prioritas (lapis 0 menang).
// Direktori yang tidak ada dilewati.
func Load(layers ...Layer) (*Registry, error) {
	r := &Registry{layers: layers, skills: map[string]Skill{}}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// Reload memindai ulang semua lapis skill di disk dan memperbarui registry.
func (r *Registry) Reload() error {
	skills := map[string]Skill{}
	var conflicts []Conflict
	r.mu.RLock()
	layers := append([]Layer(nil), r.layers...)
	r.mu.RUnlock()

	for _, layer := range layers {
		found, err := scan(layer.Dir)
		if err != nil {
			return err
		}
		for _, sk := range found {
			if prev, ok := skills[sk.Name]; ok {
				conflicts = append(conflicts, Conflict{
					Name:        sk.Name,
					WinnerDir:   prev.Dir,
					ShadowedDir: sk.Dir,
				})
				continue
			}
			skills[sk.Name] = sk
		}
	}

	r.mu.Lock()
	r.skills = skills
	r.conflicts = conflicts
	r.mu.Unlock()
	return nil
}

// Get mengembalikan skill berdasarkan nama.
func (r *Registry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sk, ok := r.skills[name]
	return sk, ok
}

// Index mengembalikan daftar ringkas "nama: deskripsi", satu per baris,
// diurutkan berdasarkan nama untuk injeksi system prompt yang stabil.
// Names mengembalikan daftar nama skill terurut.
func (r *Registry) Names() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)
	return names
}

func (r *Registry) Index() string {
	r.mu.RLock()
	names := make([]string, 0, len(r.skills))
	for name := range r.skills {
		names = append(names, name)
	}
	r.mu.RUnlock()
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		sk, _ := r.Get(name)
		b.WriteString(name)
		b.WriteString(": ")
		b.WriteString(oneLine(sk.Description))
		b.WriteString("\n")
	}
	return b.String()
}

// Conflicts mengembalikan salinan daftar benturan nama antar lapis.
func (r *Registry) Conflicts() []Conflict {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Conflict(nil), r.conflicts...)
}

// Create menulis skill baru ke lapis writable pertama. Gagal bila nama sudah
// ada di lapis mana pun atau tidak ada lapis yang writable.
func (r *Registry) Create(name, description, body string) error {
	if err := validName(name); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.skills[name]; exists {
		return fmt.Errorf("skill %q sudah ada", name)
	}
	target := -1
	for i, layer := range r.layers {
		if layer.Writable {
			target = i
			break
		}
	}
	if target < 0 {
		return errors.New("tidak ada lapis skill yang writable")
	}

	dir := filepath.Join(r.layers[target].Dir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("membuat direktori skill: %w", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	content := frontmatter(name, description) + body
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("menulis skill: %w", err)
	}

	r.skills[name] = Skill{
		Name:        name,
		Description: description,
		Body:        body,
		Path:        path,
		Dir:         r.layers[target].Dir,
	}
	return nil
}

// Update menimpa skill yang sudah ada, di lapis tempat skill itu berada, asal
// lapis tersebut writable. Description kosong mempertahankan deskripsi lama.
func (r *Registry) Update(name, description, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.skills[name]
	if !ok {
		return fmt.Errorf("skill %q tidak ditemukan", name)
	}
	if !r.layerWritable(existing.Dir) {
		return fmt.Errorf("skill %q berada di lapis read-only (%s)", name, existing.Dir)
	}
	if strings.TrimSpace(description) == "" {
		description = existing.Description
	}

	content := frontmatter(name, description) + body
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := os.WriteFile(existing.Path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("menulis skill: %w", err)
	}

	existing.Description = description
	existing.Body = body
	r.skills[name] = existing
	return nil
}

// Delete menghapus skill dari lapisnya, asal lapis tersebut writable.
func (r *Registry) Delete(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.skills[name]
	if !ok {
		return fmt.Errorf("skill %q tidak ditemukan", name)
	}
	if !r.layerWritable(existing.Dir) {
		return fmt.Errorf("skill %q berada di lapis read-only (%s)", name, existing.Dir)
	}

	if err := os.Remove(existing.Path); err != nil {
		return fmt.Errorf("menghapus skill: %w", err)
	}
	// Bentuk <dir>/<name>/SKILL.md: buang direktori kosongnya.
	if filepath.Base(existing.Path) == "SKILL.md" {
		_ = os.Remove(filepath.Dir(existing.Path))
	}
	delete(r.skills, name)
	return nil
}

// layerWritable reports whether dir is a writable layer.
func (r *Registry) layerWritable(dir string) bool {
	for _, layer := range r.layers {
		if layer.Dir == dir {
			return layer.Writable
		}
	}
	return false
}

// scan mengumpulkan skill dalam satu lapis. Berkas rusak dilewati agar satu
// skill cacat tidak menggagalkan seluruh registry.
func scan(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("membaca direktori skill %s: %w", dir, err)
	}

	seen := map[string]bool{}
	var out []Skill
	add := func(path, fallback string) {
		sk, err := parse(path, dir, fallback)
		if err != nil || seen[sk.Name] {
			return
		}
		seen[sk.Name] = true
		out = append(out, sk)
	}
	for _, entry := range entries {
		switch {
		case entry.IsDir():
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			add(filepath.Join(dir, entry.Name(), "SKILL.md"), entry.Name())
		case strings.HasSuffix(entry.Name(), ".md"):
			base := strings.TrimSuffix(entry.Name(), ".md")
			add(filepath.Join(dir, entry.Name()), base)
		}
	}
	return out, nil
}

func parse(path, layerDir, fallback string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	meta, body, ok := splitFrontmatter(string(data))
	if !ok {
		return Skill{}, fmt.Errorf("%s: frontmatter tidak ditemukan", path)
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal([]byte(meta), &fm); err != nil {
		return Skill{}, fmt.Errorf("%s: frontmatter tidak valid: %w", path, err)
	}
	name := strings.TrimSpace(fm.Name)
	if name == "" {
		name = fallback
	}
	return Skill{
		Name:        name,
		Description: strings.TrimSpace(fm.Description),
		Body:        body,
		Path:        path,
		Dir:         layerDir,
	}, nil
}

// splitFrontmatter memisahkan blok YAML di antara dua delimiter "---".
func splitFrontmatter(content string) (meta, body string, ok bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	rest, found := strings.CutPrefix(content, "---\n")
	if !found {
		return "", "", false
	}
	meta, body, found = strings.Cut(rest, "\n---")
	if !found {
		return "", "", false
	}
	body = strings.TrimPrefix(body, "\n")
	// Buang sisa baris setelah delimiter penutup.
	body = strings.TrimPrefix(body, "\n")
	return meta, body, true
}

func frontmatter(name, description string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	if description == "" {
		b.WriteString("description: \"\"\n")
	} else {
		encoded, err := yaml.Marshal(map[string]string{"description": description})
		if err != nil {
			fmt.Fprintf(&b, "description: %s\n", description)
		} else {
			line := strings.TrimPrefix(strings.TrimSpace(string(encoded)), "description: ")
			fmt.Fprintf(&b, "description: %s\n", line)
		}
	}
	b.WriteString("---\n\n")
	return b.String()
}

func validName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("nama skill tidak boleh kosong")
	case name != filepath.Base(name) || strings.ContainsAny(name, `/\`):
		return fmt.Errorf("nama skill %q tidak valid", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("nama skill %q tidak valid", name)
	}
	return nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
