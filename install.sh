#!/usr/bin/env bash
# ==============================================================================
# RENSTRA Installer
# Self-Hosted Telegram-First AI Agent in Pure Go (Zero-CGO)
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/firdausmntp/RENSTRA/main/install.sh | bash
# ==============================================================================

set -euo pipefail

REPO="firdausmntp/RENSTRA"

# Styling & Colors
BOLD="$(tput bold 2>/dev/null || echo '')"
CYAN="$(tput setaf 6 2>/dev/null || echo '')"
GREEN="$(tput setaf 2 2>/dev/null || echo '')"
YELLOW="$(tput setaf 3 2>/dev/null || echo '')"
RED="$(tput setaf 1 2>/dev/null || echo '')"
RESET="$(tput sgr0 2>/dev/null || echo '')"

info()    { echo -e "${CYAN}==>${RESET} ${BOLD}$*${RESET}"; }
success() { echo -e "${GREEN}==>${RESET} ${BOLD}$*${RESET}"; }
warn()    { echo -e "${YELLOW}warning:${RESET} $*"; }
error()   { echo -e "${RED}error:${RESET} $*" >&2; exit 1; }

echo -e "${CYAN}"
cat << 'EOF'
  ██████╗ ███████╗███╗   ██╗███████╗████████╗██████╗  █████╗ 
  ██╔══██╗██╔════╝████╗  ██║██╔════╝╚══██╔══╝██╔══██╗██╔══██╗
  ██████╔╝█████╗  ██╔██╗ ██║███████╗   ██║   ██████╔╝███████║
  ██╔══██╗██╔══╝  ██║╚██╗██║╚════██║   ██║   ██╔══██╗██╔══██║
  ██║  ██║███████╗██║ ╚████║███████║   ██║   ██║  ██║██║  ██║
  ╚═╝  ╚═╝╚══════╝╚═╝  ╚═══╝╚══════╝   ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝
EOF
echo -e "${RESET}"
echo -e "${BOLD}RENSTRA Installer${RESET} — Self-Hosted Telegram-First AI Agent\n"

# 1. Detect OS
OS_RAW="$(uname -s)"
case "${OS_RAW}" in
    Linux*)  OS="linux" ;;
    Darwin*) OS="darwin" ;;
    *)
        error "Sistem operasi '${OS_RAW}' tidak didukung oleh script ini. Untuk Windows, unduh file 'renstra-windows-amd64.exe' dari https://github.com/${REPO}/releases."
        ;;
esac

# 2. Detect Architecture
ARCH_RAW="$(uname -m)"
case "${ARCH_RAW}" in
    x86_64|amd64)   ARCH="amd64" ;;
    aarch64|arm64)  ARCH="arm64" ;;
    *)
        error "Arsitektur CPU '${ARCH_RAW}' saat ini belum didukung pre-built binary."
        ;;
esac

info "Mendeteksi platform: ${OS}/${ARCH}"

ASSET_NAME="renstra-${OS}-${ARCH}"

# 3. Resolve Latest Release Version
info "Memeriksa rilis terbaru dari GitHub (${REPO})..."
LATEST_TAG=""
if command -v curl >/dev/null 2>&1; then
    LATEST_TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name":' | head -n 1 | cut -d '"' -f 4 || true)"
elif command -v wget >/dev/null 2>&1; then
    LATEST_TAG="$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name":' | head -n 1 | cut -d '"' -f 4 || true)"
fi

if [ -n "${LATEST_TAG}" ]; then
    info "Versi terbaru: ${BOLD}${LATEST_TAG}${RESET}"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${LATEST_TAG}/${ASSET_NAME}"
else
    warn "Tidak dapat membaca tag via GitHub API (mungkin batas rate limit tercapai). Menggunakan endpoint latest download..."
    DOWNLOAD_URL="https://github.com/${REPO}/releases/latest/download/${ASSET_NAME}"
fi

# 4. Download Binary
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT
TMP_FILE="${TMP_DIR}/${ASSET_NAME}"

info "Mengunduh binary dari: ${DOWNLOAD_URL}"
if command -v curl >/dev/null 2>&1; then
    curl -fL --progress-bar "${DOWNLOAD_URL}" -o "${TMP_FILE}" || error "Gagal mengunduh ${DOWNLOAD_URL}"
elif command -v wget >/dev/null 2>&1; then
    wget -q --show-progress "${DOWNLOAD_URL}" -O "${TMP_FILE}" || error "Gagal mengunduh ${DOWNLOAD_URL}"
else
    error "Diperlukan 'curl' atau 'wget' untuk mengunduh binary."
fi

chmod +x "${TMP_FILE}"

# 5. Determine Install Directory
INSTALL_DIR="/usr/local/bin"
USE_SUDO=false

if [ -w "${INSTALL_DIR}" ]; then
    USE_SUDO=false
elif [ "$(id -u)" -eq 0 ]; then
    USE_SUDO=false
elif command -v sudo >/dev/null 2>&1; then
    # Try asking for sudo for /usr/local/bin
    if sudo -n true 2>/dev/null || [ -t 0 ]; then
        USE_SUDO=true
    else
        INSTALL_DIR="${HOME}/.local/bin"
        mkdir -p "${INSTALL_DIR}"
    fi
else
    INSTALL_DIR="${HOME}/.local/bin"
    mkdir -p "${INSTALL_DIR}"
fi

TARGET_BIN="${INSTALL_DIR}/renstra"
info "Memasang binary ke: ${TARGET_BIN}"

if [ "${USE_SUDO}" = true ]; then
    sudo mv "${TMP_FILE}" "${TARGET_BIN}"
    sudo chmod +x "${TARGET_BIN}"
else
    mkdir -p "${INSTALL_DIR}"
    mv "${TMP_FILE}" "${TARGET_BIN}"
    chmod +x "${TARGET_BIN}"
fi

# 6. Verify Installation
if command -v renstra >/dev/null 2>&1; then
    INSTALLED_VER="$(renstra version 2>/dev/null || echo 'terpasang')"
    success "RENSTRA berhasil dipasang! (${INSTALLED_VER})"
else
    success "Binary berhasil ditaruh di ${TARGET_BIN}"
    warn "${INSTALL_DIR} belum ada di \$PATH lingkunganmu."
    echo -e "Tambahkan baris berikut ke file profil shell (~/.bashrc, ~/.zshrc, atau ~/.profile):"
    echo -e "  ${BOLD}export PATH=\"${INSTALL_DIR}:\$PATH\"${RESET}\n"
fi

# 7. Next Steps Guidance
echo -e "\n${BOLD}Langkah Pertama:${RESET}"
echo -e "  1. Jalankan wizard konfigurasi Telegram bot & API key:"
echo -e "     ${GREEN}renstra setup${RESET}"
echo -e "  2. Jalankan bot Telegram gateway:"
echo -e "     ${GREEN}renstra serve${RESET}"
echo -e "  3. Atau mulai sesi chat interaktif langsung di terminal:"
echo -e "     ${GREEN}renstra chat${RESET}\n"
