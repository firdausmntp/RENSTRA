package main

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"renstra/internal/config"
	"renstra/internal/migrate"
	"renstra/internal/update"
)

func migrateCmd(args []string) error {
	if len(args) == 0 || args[0] != "hermes" {
		return fmt.Errorf("pemakaian: renstra migrate hermes [--from <dir>] [--dry-run] [--force]")
	}
	var fromDir string
	var dryRun, force bool
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dry-run":
			dryRun = true
		case "--force":
			force = true
		case "--from":
			if i+1 >= len(rest) {
				return fmt.Errorf("--from butuh path direktori")
			}
			i++
			fromDir = rest[i]
		default:
			if strings.HasPrefix(rest[i], "--from=") {
				fromDir = strings.TrimPrefix(rest[i], "--from=")
			} else {
				return fmt.Errorf("flag tidak dikenal: %s", rest[i])
			}
		}
	}

	rep, err := migrate.FromHermes(context.Background(), migrate.Options{
		FromDir: fromDir,
		ToDir:   config.Home(),
		DryRun:  dryRun,
		Force:   force,
	})
	if err != nil {
		return err
	}

	mode := "SELESAI"
	if rep.DryRun {
		mode = "DRY-RUN (tidak ada perubahan ditulis)"
	}
	fmt.Printf("Migrasi Hermes -> RENSTRA [%s]\n", mode)
	fmt.Printf("  Sumber : %s\n", rep.SourceDir)
	fmt.Printf("  Tujuan : %s\n", rep.DestDir)
	fmt.Printf("  Config : written=%v\n", rep.ConfigWritten)
	fmt.Printf("  Env    : copied=%v (keys=%d)\n", rep.EnvCopied, len(rep.EnvVarsFound))
	fmt.Printf("  MCP    : written=%v (servers=%d)\n", rep.MCPWritten, rep.MCPServers)
	fmt.Printf("  Memory : MEMORY.md=%v, USER.md=%v, SOUL.md=%v\n", rep.MemoryCopied, rep.UserCopied, rep.SoulCopied)
	fmt.Printf("  Skills : disalin=%d, dilewati=%d\n", rep.SkillsCopied, rep.SkillsSkipped)
	fmt.Printf("  History: sesi=%d (dilewati=%d), pesan=%d\n", rep.SessionsCopied, rep.SessionsSkipped, rep.MessagesCopied)
	if len(rep.EnvVarsFound) > 0 {
		fmt.Printf("  Env API key ditemukan di .env Hermes (pastikan di-export): %s\n", strings.Join(rep.EnvVarsFound, ", "))
	}
	for _, s := range rep.SkippedItems {
		fmt.Printf("  [lewati] %s\n", s)
	}
	for _, w := range rep.Warnings {
		fmt.Printf("  [peringatan] %s\n", w)
	}
	return nil
}

func updateCmd(args []string) error {
	checkOnly := len(args) > 0 && (args[0] == "check" || args[0] == "--check")

	fmt.Printf("Memeriksa rilis terbaru dari %s (versi kamu: %s)...\n", update.DefaultRepo, version)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rel, err := update.FetchLatestRelease(ctx, update.DefaultRepo, version)
	if err != nil {
		return fmt.Errorf("gagal memeriksa pembaruan: %w", err)
	}

	if !update.IsNewer(rel.TagName, version) {
		fmt.Printf("✅ RENSTRA sudah versi terbaru (%s).\n", version)
		return nil
	}

	fmt.Printf("🚀 Ditemukan versi baru: %s (rilis pada %s)\n", rel.TagName, rel.PublishedAt.Local().Format("02 Jan 2006"))
	if rel.HTMLURL != "" {
		fmt.Printf("Catatan rilis: %s\n\n", rel.HTMLURL)
	}
	if checkOnly {
		fmt.Println("Jalankan `renstra update` untuk mengunduh dan memasang versi ini secara otomatis.")
		return nil
	}

	targetAsset := update.TargetAssetName(runtime.GOOS, runtime.GOARCH)
	fmt.Printf("Mengunduh asset: %s...\n", targetAsset)
	err = update.DownloadAsset(ctx, rel, func(downloaded, total int64) {
		if total > 0 {
			pct := float64(downloaded) / float64(total) * 100
			fmt.Printf("\rMengunduh... %.1f%% (%d / %d byte)", pct, downloaded, total)
		} else {
			fmt.Printf("\rMengunduh... %d byte", downloaded)
		}
	})
	fmt.Println()
	if err != nil {
		return fmt.Errorf("gagal memperbarui: %w", err)
	}

	fmt.Printf("✨ Sukses! RENSTRA berhasil diperbarui ke %s.\n", rel.TagName)
	fmt.Println("Jalankan `renstra version` untuk memverifikasi.")
	return nil
}
