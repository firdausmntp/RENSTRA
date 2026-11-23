package main

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"renstra/internal/config"
	"renstra/internal/telegram"
)

func (h *telegramHandler) buildGroupMenu(ctx context.Context, chatID int64) (string, [][]telegram.InlineButton) {
	mode := "always"
	var topics []int64
	if h.bot != nil {
		mode = h.bot.GetGroupMode()
		topics = h.bot.AllowedTopicIDs()
	} else if h.cfg != nil && strings.EqualFold(h.cfg.Telegram.GroupMode, "mention") {
		mode = "mention"
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i] < topics[j] })

	curTopic := telegram.TopicIDFrom(ctx)
	topicList := "Semua topik diizinkan"
	if len(topics) > 0 {
		parts := make([]string, len(topics))
		for i, id := range topics {
			parts[i] = fmt.Sprintf("`#%d`", id)
		}
		topicList = strings.Join(parts, ", ")
	}

	var b strings.Builder
	b.WriteString("👥 **Kontrol Grup & Forum Topics**\n")
	fmt.Fprintf(&b, "• **Chat ID:** `%d`\n", chatID)
	if curTopic > 0 {
		fmt.Fprintf(&b, "• **Topik Aktif:** `#%d`\n", curTopic)
	}
	fmt.Fprintf(&b, "• **Group Mode:** `%s`\n", mode)
	fmt.Fprintf(&b, "• **Filter Topik Relevan:** %s\n\n", topicList)
	b.WriteString("• `always` — Bot merespons semua pesan di grup/topik yang diizinkan\n")
	b.WriteString("• `mention` — Bot hanya merespons `/command`, `@bot`, atau reply ke pesan bot\n")

	alwaysBtn := telegram.InlineButton{Text: "🗣️ Always", CallbackData: "grp:mode:always", Style: "primary"}
	mentionBtn := telegram.InlineButton{Text: "🔔 Mention Only", CallbackData: "grp:mode:mention", Style: "primary"}
	if mode == "always" {
		alwaysBtn.Text = "✅ Always"
		alwaysBtn.Style = "success"
	} else {
		mentionBtn.Text = "✅ Mention Only"
		mentionBtn.Style = "success"
	}

	rows := [][]telegram.InlineButton{
		{alwaysBtn, mentionBtn},
	}
	if curTopic > 0 {
		rows = append(rows, []telegram.InlineButton{
			{Text: fmt.Sprintf("📌 Toggle Topik Ini (#%d)", curTopic), CallbackData: fmt.Sprintf("grp:topic:%d", curTopic), Style: "primary"},
		})
	}
	if len(topics) > 0 {
		rows = append(rows, []telegram.InlineButton{
			{Text: "🔓 Izinkan Semua Topik", CallbackData: "grp:all", Style: "danger"},
		})
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "🔄 Refresh", CallbackData: "cmd:group", Style: "primary"},
		{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
		{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
	})
	return b.String(), rows
}

func (h *telegramHandler) handleGroupCallback(ctx context.Context, chatID, msgID int64, parts []string) {
	if h.bot == nil || len(parts) < 2 {
		return
	}
	switch parts[1] {
	case "mode":
		if len(parts) >= 3 {
			h.bot.SetGroupMode(parts[2])
			if h.cfg != nil {
				h.cfg.Telegram.GroupMode = h.bot.GetGroupMode()
			}
		}
	case "topic":
		if len(parts) >= 3 {
			if tid, err := strconv.ParseInt(parts[2], 10, 64); err == nil && tid > 0 {
				h.bot.ToggleAllowedTopic(tid)
			}
		}
	case "all":
		h.bot.ClearAllowedTopics()
	}
	text, rows := h.buildGroupMenu(ctx, chatID)
	_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
}

func (h *telegramHandler) handleGroupCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) == 0 {
		text, _ := h.buildGroupMenu(ctx, chatID)
		return text, nil
	}
	sub := strings.ToLower(fields[0])
	switch sub {
	case "always", "mention":
		if h.bot != nil {
			h.bot.SetGroupMode(sub)
		}
		if h.cfg != nil {
			h.cfg.Telegram.GroupMode = sub
		}
		return fmt.Sprintf("👥 Mode grup diubah ke: `%s`", sub), nil
	case "topic":
		tid := telegram.TopicIDFrom(ctx)
		if len(fields) >= 2 {
			if parsed, err := strconv.ParseInt(strings.TrimPrefix(fields[1], "#"), 10, 64); err == nil {
				tid = parsed
			}
		}
		if tid <= 0 {
			return "Pemakaian: `/group topic <thread_id>` (atau jalankan `/group topic` di dalam topik forum).", nil
		}
		if h.bot != nil {
			added := h.bot.ToggleAllowedTopic(tid)
			if added {
				return fmt.Sprintf("📌 Topik `#%d` ditambahkan ke daftar topik relevan.", tid), nil
			}
			return fmt.Sprintf("📌 Topik `#%d` dihapus dari filter topik.", tid), nil
		}
		return fmt.Sprintf("📌 Topik `#%d` dicatat.", tid), nil
	case "all", "clear":
		if h.bot != nil {
			h.bot.ClearAllowedTopics()
		}
		return "🔓 Filter topik dihapus; semua topik diizinkan.", nil
	case "new", "create":
		if h.bot == nil || len(fields) < 2 {
			return "Pemakaian: `/group new <nama_topik>` (bot harus menjadi admin grup forum).", nil
		}
		name := strings.TrimSpace(strings.Join(fields[1:], " "))
		tid, err := h.bot.CreateForumTopic(ctx, chatID, name)
		if err != nil {
			return "", fmt.Errorf("gagal membuat topik forum: %w", err)
		}
		return fmt.Sprintf("✨ Topik forum baru dibuat: **%s** (`#%d`)", name, tid), nil
	case "rename":
		tid := telegram.TopicIDFrom(ctx)
		if h.bot == nil || tid <= 0 || len(fields) < 2 {
			return "Pemakaian: jalankan `/group rename <nama_baru>` di dalam topik forum yang ingin diganti namanya.", nil
		}
		name := strings.TrimSpace(strings.Join(fields[1:], " "))
		if err := h.bot.EditForumTopic(ctx, chatID, tid, name); err != nil {
			return "", fmt.Errorf("gagal mengubah nama topik: %w", err)
		}
		return fmt.Sprintf("✏️ Nama topik `#%d` diubah menjadi: **%s**", tid, name), nil
	default:
		return "Pemakaian: `/group [always|mention|topic <id>|all|new <nama>|rename <nama>]`", nil
	}
}

func (h *telegramHandler) handleModelAddCmd(ctx context.Context, chatID int64, rawArgs string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(rawArgs))
	spec, err := config.ParseAddModelArgs(fields, h.cfg)
	if err != nil {
		return "", err
	}
	updatedCfg, err := config.AddModel(spec, h.cfg)
	if err != nil {
		return "", fmt.Errorf("gagal menyimpan model baru: %w", err)
	}
	h.cfg = updatedCfg
	ref := spec.Provider + ":" + spec.Model
	_, switchErr := h.switchChatModel(ctx, chatID, ref)

	var b strings.Builder
	fmt.Fprintf(&b, "✅ **Model ditambahkan & diaktifkan:** `%s`\n", ref)
	fmt.Fprintf(&b, "• **Provider:** `%s`\n", spec.Provider)
	fmt.Fprintf(&b, "• **Context Window:** `%d` tok\n", spec.Context)
	if spec.MaxOutput > 0 {
		fmt.Fprintf(&b, "• **Max Output:** `%d` tok\n", spec.MaxOutput)
	}
	fmt.Fprintf(&b, "• **Vision:** `%v`\n", spec.Vision)
	if spec.Alias != "" {
		fmt.Fprintf(&b, "• **Alias:** `%s` -> `%s`\n", spec.Alias, ref)
	}
	if switchErr != nil {
		fmt.Fprintf(&b, "\n⚠️ _Catatan aktivasi:_ `%v`", switchErr)
	}
	return b.String(), nil
}

func (h *telegramHandler) switchChatModel(ctx context.Context, chatID int64, arg string) (string, error) {
	sessID := telegram.SessionChatID(ctx, chatID)
	cr := h.runnerForChat(ctx, chatID)
	if h.cfg != nil && len(h.cfg.Providers) > 0 {
		pcfg, model, err := config.Resolve(arg, h.cfg)
		if err != nil {
			return "", fmt.Errorf("gagal memilih model %q: %w", arg, err)
		}
		prov, err := buildProvider(pcfg)
		if err != nil {
			return "", fmt.Errorf("gagal inisialisasi provider untuk %q: %w", arg, err)
		}
		mc := pcfg.ModelConfig(model)
		cr.SetModel(prov, model, mc.MaxOutput, mc.Context, mc.PriceIn, mc.PriceOut)
		cr.SetCapabilities(mc.Vision, mc.Tools != nil && !*mc.Tools)
		_ = h.store.SetChatModel(ctx, sessID, arg)
		if arg != model {
			return fmt.Sprintf("Model aktif diganti ke: %s (%s)", model, arg), nil
		}
		return fmt.Sprintf("Model aktif diganti ke: %s", model), nil
	}
	cr.SetModel(nil, arg, 0, 0, 0, 0)
	_ = h.store.SetChatModel(ctx, sessID, arg)
	return fmt.Sprintf("Model aktif diganti ke: %s", arg), nil
}

func (h *telegramHandler) handleModelCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	cr := h.runnerForChat(ctx, chatID)
	if arg == "" {
		var b strings.Builder
		fmt.Fprintf(&b, "Model aktif saat ini: %s\n", cr.GetModel())
		if h.cfg != nil && len(h.cfg.Providers) > 0 {
			b.WriteString("\nProvider & model terkonfigurasi:\n")
			pnames := make([]string, 0, len(h.cfg.Providers))
			for n := range h.cfg.Providers {
				pnames = append(pnames, n)
			}
			sort.Strings(pnames)
			for _, pn := range pnames {
				pc := h.cfg.Providers[pn]
				fmt.Fprintf(&b, "- %s (%s): default=%s", pn, pc.Kind, pc.DefaultModel)
				if len(pc.Models) > 0 {
					mnames := make([]string, 0, len(pc.Models))
					for mn := range pc.Models {
						mnames = append(mnames, mn)
					}
					sort.Strings(mnames)
					fmt.Fprintf(&b, " [%s]", strings.Join(mnames, ", "))
				}
				b.WriteString("\n")
			}
		}
		if h.cfg != nil && len(h.cfg.Aliases) > 0 {
			b.WriteString("\nAlias:\n")
			anames := make([]string, 0, len(h.cfg.Aliases))
			for a := range h.cfg.Aliases {
				anames = append(anames, a)
			}
			sort.Strings(anames)
			for _, a := range anames {
				fmt.Fprintf(&b, "- %s -> %s\n", a, h.cfg.Aliases[a])
			}
		}
		b.WriteString("\nGunakan `/model <nama>`, `/model <provider>:<model>`, atau `/model add <nama> <provider> <context>` untuk menambah model baru.")
		return strings.TrimRight(b.String(), "\n"), nil
	}
	trimmedArg := strings.TrimSpace(arg)
	lowerArg := strings.ToLower(trimmedArg)
	if lowerArg == "add" || strings.HasPrefix(lowerArg, "add ") {
		rest := strings.TrimSpace(trimmedArg[len("add"):])
		return h.handleModelAddCmd(ctx, chatID, rest)
	}
	for _, prefix := range []string{"rm", "remove", "delete", "del"} {
		if lowerArg == prefix || strings.HasPrefix(lowerArg, prefix+" ") {
			rest := strings.TrimSpace(trimmedArg[len(prefix):])
			updatedCfg, removed, err := config.RemoveModel(rest, h.cfg)
			if err != nil {
				return "", err
			}
			h.cfg = updatedCfg
			return fmt.Sprintf("🗑️ Model/alias dihapus dari `config.yaml`: `%s`", removed), nil
		}
	}
	ref, isGlobal := parseGlobalModelFlag(trimmedArg)
	if isGlobal {
		if ref == "" {
			return "", fmt.Errorf("pemakaian: /model <model_id> --global")
		}
		return h.switchGlobalModel(ctx, ref)
	}
	return h.switchChatModel(ctx, chatID, arg)
}

func parseGlobalModelFlag(arg string) (string, bool) {
	fields := strings.Fields(arg)
	var rest []string
	global := false
	for _, f := range fields {
		if f == "--global" || f == "-g" {
			global = true
			continue
		}
		rest = append(rest, f)
	}
	return strings.Join(rest, " "), global
}

func (h *telegramHandler) switchGlobalModel(ctx context.Context, ref string) (string, error) {
	if h.cfg != nil && len(h.cfg.Providers) > 0 {
		pcfg, model, err := config.Resolve(ref, h.cfg)
		if err != nil {
			return "", err
		}
		prov, err := buildProvider(pcfg)
		if err != nil {
			return "", err
		}
		mc := pcfg.ModelConfig(model)
		h.runner.SetModel(prov, model, mc.MaxOutput, mc.Context, mc.PriceIn, mc.PriceOut)
		h.runner.SetCapabilities(mc.Vision, mc.Tools != nil && !*mc.Tools)
		h.mu.Lock()
		for _, cr := range h.chats {
			cr.SetModel(prov, model, mc.MaxOutput, mc.Context, mc.PriceIn, mc.PriceOut)
			cr.SetCapabilities(mc.Vision, mc.Tools != nil && !*mc.Tools)
		}
		h.mu.Unlock()
		_, storedRef, _ := config.SetDefaultModel(ref)
		if storedRef == "" {
			storedRef = ref
		}
		h.cfg.DefaultModel = storedRef
		if h.store != nil {
			_ = h.store.SetGlobalModel(ctx, storedRef)
		}
		return fmt.Sprintf("🌐 **Model global diubah ke:** `%s`\nBerlaku untuk seluruh sesi, grup, dan topik.", model), nil
	}

	h.runner.SetModel(nil, ref, 0, 0, 0, 0)
	h.mu.Lock()
	for _, cr := range h.chats {
		cr.SetModel(nil, ref, 0, 0, 0, 0)
	}
	h.mu.Unlock()
	if h.store != nil {
		_ = h.store.SetGlobalModel(ctx, ref)
	}
	return fmt.Sprintf("🌐 **Model global diubah ke:** `%s`\nBerlaku untuk seluruh sesi, grup, dan topik.", ref), nil
}

func (h *telegramHandler) handlePersonaCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	cr := h.runnerForChat(ctx, chatID)
	trimmed := strings.TrimSpace(arg)
	scope := "Chat Ini"
	if tid := telegram.TopicIDFrom(ctx); tid > 0 {
		scope = fmt.Sprintf("Topik #%d", tid)
	} else if chatID < 0 {
		scope = fmt.Sprintf("Grup (%d)", chatID)
	}
	if trimmed == "" {
		cur := strings.TrimSpace(cr.PromptPrefix)
		if cur == "" {
			cur = "(default)"
		}
		return fmt.Sprintf("🎭 **Persona Aktif [%s]:**\n%s\n\n💡 _Ubah persona khusus topik/grup ini dengan `/persona <instruksi>` atau kembalikan ke default dengan `/persona reset`._", scope, cur), nil
	}
	cs := h.loadChatSettings(ctx, chatID)
	if strings.EqualFold(trimmed, "reset") || strings.EqualFold(trimmed, "clear") || trimmed == "-" {
		empty := ""
		cs.PromptPrefix = &empty
		if err := h.saveChatSettings(ctx, chatID, cs); err != nil {
			return "", err
		}
		return fmt.Sprintf("🎭 Persona untuk **%s** dikembalikan ke default.", scope), nil
	}
	cs.PromptPrefix = &trimmed
	if err := h.saveChatSettings(ctx, chatID, cs); err != nil {
		return "", err
	}
	return fmt.Sprintf("🎭 **Persona [%s] diperbarui:**\n> %s", scope, trimmed), nil
}
