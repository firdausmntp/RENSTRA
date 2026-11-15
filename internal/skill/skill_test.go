package skill_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/skill"
)

// writeSkill membuat <dir>/<name>/SKILL.md (atau <dir>/<name>.md bila dirFile).
func writeSkill(t *testing.T, dir, name, description, body string, dirFile bool) {
	t.Helper()
	var path string
	if dirFile {
		path = filepath.Join(dir, name+".md")
	} else {
		path = filepath.Join(dir, name, "SKILL.md")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + body
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func TestLoadPrecedence(t *testing.T) {
	top := t.TempDir()
	bottom := t.TempDir()

	writeSkill(t, top, "deploy", "deploy dari lapis atas", "body atas", false)
	writeSkill(t, bottom, "deploy", "deploy dari lapis bawah", "body bawah", false)
	writeSkill(t, bottom, "review", "review kode", "body review", false)

	reg, err := skill.Load(skill.Layer{Dir: top}, skill.Layer{Dir: bottom})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	sk, ok := reg.Get("deploy")
	if !ok {
		t.Fatalf("deploy tidak ditemukan")
	}
	if sk.Description != "deploy dari lapis atas" || sk.Body != "body atas" {
		t.Fatalf("lapis atas tidak menang: %+v", sk)
	}

	// Skill hanya di lapis bawah tetap terlihat
	if _, ok := reg.Get("review"); !ok {
		t.Fatalf("review dari lapis bawah tidak ditemukan")
	}
}

func TestLoadSingleFileForm(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "quick", "skill satu berkas", "isi", true)

	reg, err := skill.Load(skill.Layer{Dir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("quick")
	if !ok {
		t.Fatalf("quick tidak ditemukan")
	}
	if sk.Body != "isi\n" && sk.Body != "isi" {
		t.Fatalf("body tidak terpotong benar: %q", sk.Body)
	}
}

func TestLoadMissingDirSkipped(t *testing.T) {
	reg, err := skill.Load(skill.Layer{Dir: filepath.Join(t.TempDir(), "tidak-ada")})
	if err != nil {
		t.Fatalf("Load direktori hilang harus sukses: %v", err)
	}
	if reg.Index() != "" {
		t.Fatalf("index harus kosong, dapat %q", reg.Index())
	}
}

func TestConflicts(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()

	writeSkill(t, a, "sama", "versi a", "body a", false)
	writeSkill(t, b, "sama", "versi b", "body b", false)
	writeSkill(t, c, "sama", "versi c", "body c", false)
	writeSkill(t, a, "bersih", "tanpa bentrok", "body", false)

	reg, err := skill.Load(skill.Layer{Dir: a}, skill.Layer{Dir: b}, skill.Layer{Dir: c})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	conflicts := reg.Conflicts()
	if len(conflicts) != 2 {
		t.Fatalf("harus ada 2 benturan, dapat %d: %+v", len(conflicts), conflicts)
	}
	for _, cf := range conflicts {
		if cf.Name != "sama" {
			t.Fatalf("nama benturan salah: %+v", cf)
		}
		if cf.WinnerDir != a {
			t.Fatalf("pemenang harus lapis pertama: %+v", cf)
		}
	}
	if conflicts[0].ShadowedDir != b || conflicts[1].ShadowedDir != c {
		t.Fatalf("shadowed dir salah: %+v", conflicts)
	}

	// Pemenang tetap versi lapis atas
	if sk, _ := reg.Get("sama"); sk.Description != "versi a" {
		t.Fatalf("pemenang salah: %+v", sk)
	}
}

func TestIndexFormatting(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "zeta", "skill terakhir", "b", false)
	writeSkill(t, dir, "alpha", "skill\n  multi   baris", "b", false)

	reg, err := skill.Load(skill.Layer{Dir: dir})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got := reg.Index()
	want := "alpha: skill multi baris\nzeta: skill terakhir\n"
	if got != want {
		t.Fatalf("index salah:\ngot  %q\nwant %q", got, want)
	}
}

func TestCreateWritableLayer(t *testing.T) {
	top := t.TempDir()
	bottom := t.TempDir()

	reg, err := skill.Load(
		skill.Layer{Dir: top, Writable: false},
		skill.Layer{Dir: bottom, Writable: true},
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := reg.Create("baru", "deskripsi baru", "isi skill\n"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Harus ditulis ke lapis writable, bukan lapis pertama
	if _, err := os.Stat(filepath.Join(bottom, "baru", "SKILL.md")); err != nil {
		t.Fatalf("berkas tidak ditulis ke lapis writable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(top, "baru")); !os.IsNotExist(err) {
		t.Fatalf("berkas tidak boleh ditulis ke lapis read-only")
	}

	// Langsung terlihat di registry
	sk, ok := reg.Get("baru")
	if !ok {
		t.Fatalf("skill baru tidak terdaftar")
	}
	if sk.Description != "deskripsi baru" || sk.Body != "isi skill\n" {
		t.Fatalf("skill baru salah: %+v", sk)
	}

	// Muat ulang dari disk harus sama (frontmatter valid)
	again, err := skill.Load(skill.Layer{Dir: top}, skill.Layer{Dir: bottom, Writable: true})
	if err != nil {
		t.Fatalf("Load ulang: %v", err)
	}
	reloaded, ok := again.Get("baru")
	if !ok {
		t.Fatalf("skill baru tidak terbaca ulang")
	}
	if reloaded.Description != "deskripsi baru" {
		t.Fatalf("deskripsi tidak bulat balik: %q", reloaded.Description)
	}
	if strings.TrimSpace(reloaded.Body) != "isi skill" {
		t.Fatalf("body tidak bulat balik: %q", reloaded.Body)
	}
}

func TestCreateRejectsDuplicate(t *testing.T) {
	top := t.TempDir()
	bottom := t.TempDir()
	writeSkill(t, top, "lama", "sudah ada", "body", false)

	reg, err := skill.Load(skill.Layer{Dir: top}, skill.Layer{Dir: bottom, Writable: true})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Create("lama", "duplikat", "body"); err == nil {
		t.Fatalf("Create harus gagal untuk nama duplikat")
	}
}

func TestCreateReadOnly(t *testing.T) {
	dir := t.TempDir()
	reg, err := skill.Load(skill.Layer{Dir: dir, Writable: false})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Create("baru", "deskripsi", "body"); err == nil {
		t.Fatalf("Create harus gagal tanpa lapis writable")
	}
	if _, err := os.Stat(filepath.Join(dir, "baru")); !os.IsNotExist(err) {
		t.Fatalf("tidak boleh ada berkas tertulis")
	}
}
