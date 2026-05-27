#!/bin/bash
set -e

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
}

print_banner "PHP Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing PHP Development Environment"
    echo "========================================="
}

# Environment variables
export PHP_VERSION="${PHP_VERSION:-8.5}"
export COMPOSER_HOME="${COMPOSER_HOME:-/home/vscode/.cache/composer}"
export COMPOSER_CACHE_DIR="${COMPOSER_CACHE_DIR:-/home/vscode/.cache/composer/cache}"

# Install dependencies
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    software-properties-common \
    curl \
    git \
    unzip

# Add PHP repository
echo -e "${YELLOW}Adding PHP repository...${NC}"
sudo add-apt-repository -y ppa:ondrej/php
sudo apt-get update

# Install PHP
echo -e "${YELLOW}Installing PHP ${PHP_VERSION}...${NC}"
sudo apt-get install -y \
    php${PHP_VERSION} \
    php${PHP_VERSION}-cli \
    php${PHP_VERSION}-common \
    php${PHP_VERSION}-curl \
    php${PHP_VERSION}-mbstring \
    php${PHP_VERSION}-xml \
    php${PHP_VERSION}-zip \
    php${PHP_VERSION}-mysql \
    php${PHP_VERSION}-pgsql \
    php${PHP_VERSION}-sqlite3 \
    php${PHP_VERSION}-redis \
    php${PHP_VERSION}-gd \
    php${PHP_VERSION}-intl \
    php${PHP_VERSION}-bcmath

PHP_INSTALLED=$(php -version | head -n 1)
echo -e "${GREEN}✓ ${PHP_INSTALLED} installed${NC}"

# Install Composer
echo -e "${YELLOW}Installing Composer...${NC}"
EXPECTED_CHECKSUM="$(curl -sS https://composer.github.io/installer.sig)"
php -r "copy('https://getcomposer.org/installer', 'composer-setup.php');"
ACTUAL_CHECKSUM="$(php -r "echo hash_file('sha384', 'composer-setup.php');")"

if [ "$EXPECTED_CHECKSUM" != "$ACTUAL_CHECKSUM" ]; then
    echo -e "${RED}ERROR: Invalid installer checksum${NC}"
    rm composer-setup.php
    exit 1
fi

sudo php composer-setup.php --quiet --install-dir=/usr/local/bin --filename=composer
rm composer-setup.php

COMPOSER_VERSION=$(composer --version)
echo -e "${GREEN}✓ ${COMPOSER_VERSION} installed${NC}"

# Create cache directories
mkdir -p "$COMPOSER_HOME"
mkdir -p "$COMPOSER_CACHE_DIR"

# ─────────────────────────────────────────────────────────────────────────────
# Install PHP Development Tools — batched for speed
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing PHP development tools...${NC}"

# Batch install: PHP-CS-Fixer, PHPStan, PHPUnit, PHP_CodeSniffer
composer global require --quiet \
    friendsofphp/php-cs-fixer \
    phpstan/phpstan \
    phpunit/phpunit \
    squizlabs/php_codesniffer
echo -e "${GREEN}✓ PHP-CS-Fixer, PHPStan, PHPUnit, PHP_CodeSniffer installed${NC}"

# Pest (BDD-style testing, optional — may fail due to dependency conflicts)
echo -e "${YELLOW}Installing Pest...${NC}"
PEST_OUTPUT=$(composer global require pestphp/pest --quiet 2>&1) && PEST_STATUS=$? || PEST_STATUS=$?
if [ "$PEST_STATUS" -eq 0 ]; then
    echo -e "${GREEN}✓ Pest installed${NC}"
else
    echo -e "${YELLOW}⚠ Pest install skipped (optional, may require project context)${NC}"
    # Log error for debugging if verbose
    [ -n "$PEST_OUTPUT" ] && echo -e "${YELLOW}  Details: ${PEST_OUTPUT}${NC}" | head -1
fi

# Add Composer global bin to PATH (both .bashrc and .zshrc for consistency)
COMPOSER_BIN="$COMPOSER_HOME/vendor/bin"
for rc_file in "$HOME/.bashrc" "$HOME/.zshrc"; do
    if [ -f "$rc_file" ] && ! grep -q "Composer global binaries" "$rc_file" 2>/dev/null; then
        echo "" >> "$rc_file"
        echo "# Composer global binaries" >> "$rc_file"
        echo "export PATH=\"\$PATH:$COMPOSER_BIN\"" >> "$rc_file"
    fi
done

echo -e "${GREEN}✓ PHP development tools installed${NC}"

print_success_banner "PHP environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}PHP environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - ${PHP_INSTALLED}"
echo "  - ${COMPOSER_VERSION}"
echo ""
echo "Development tools:"
echo "  - PHP-CS-Fixer (formatter)"
echo "  - PHPStan (static analysis)"
echo "  - PHPUnit (testing)"
echo "  - Pest (BDD testing)"
echo "  - PHP_CodeSniffer (PSR compliance)"
echo ""
echo "Cache directories:"
echo "  - Composer: $COMPOSER_HOME"
echo ""
