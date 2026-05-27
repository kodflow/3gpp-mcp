#!/bin/bash
set -e

FEATURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../shared/feature-utils.sh
source "${FEATURE_DIR}/feature-utils.sh" 2>/dev/null || \
source "${FEATURE_DIR}/../shared/feature-utils.sh" 2>/dev/null || {
    RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
    ok() { echo -e "${GREEN}✓${NC} $*"; }
    warn() { echo -e "${YELLOW}⚠${NC} $*"; }
    err() { echo -e "${RED}✗${NC} $*" >&2; }
    get_github_latest_version() {
        local repo="$1" version auth_args=()
        [[ -n "${GITHUB_TOKEN:-}" ]] && auth_args=(-H "Authorization: token ${GITHUB_TOKEN}")
        local attempt
        for attempt in 1 2 3; do
            version=$(curl -fsS --connect-timeout 5 --max-time 10 \
                "${auth_args[@]}" \
                "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null \
                | sed -n 's/.*"tag_name": *"v\?\([^"]*\)".*/\1/p' | head -n 1)
            [[ -n "$version" ]] && break
            sleep $((attempt * 2))
        done
        if [[ -z "$version" ]]; then
            echo -e "${RED}✗ Failed to resolve latest version for ${repo}${NC}" >&2
            return 1
        fi
        echo "$version"
    }
    get_github_latest_version_or_empty() {
        get_github_latest_version "$1" 2>/dev/null || echo ""
    }
}

print_banner "Java Development Environment" 2>/dev/null || {
    echo "========================================="
    echo "Installing Java Development Environment"
    echo "========================================="
}

# Environment variables
export SDKMAN_DIR="${SDKMAN_DIR:-/home/vscode/.cache/sdkman}"
export MAVEN_OPTS="${MAVEN_OPTS:--Dmaven.repo.local=/home/vscode/.cache/maven}"
export GRADLE_USER_HOME="${GRADLE_USER_HOME:-/home/vscode/.cache/gradle}"

# Install dependencies
echo -e "${YELLOW}Installing dependencies...${NC}"
sudo apt-get update && sudo apt-get install -y \
    curl \
    zip \
    unzip \
    git

# Install SDKMAN
echo -e "${YELLOW}Installing SDKMAN...${NC}"
curl -s "https://get.sdkman.io" | bash
source "$SDKMAN_DIR/bin/sdkman-init.sh"
echo -e "${GREEN}✓ SDKMAN installed${NC}"

# Install Java (latest LTS)
echo -e "${YELLOW}Installing Java...${NC}"
sdk install java
JAVA_VERSION=$(java -version 2>&1 | head -n 1)
echo -e "${GREEN}✓ ${JAVA_VERSION} installed${NC}"

# Install Maven
echo -e "${YELLOW}Installing Maven...${NC}"
sdk install maven
MAVEN_VERSION=$(mvn -version | head -n 1)
echo -e "${GREEN}✓ ${MAVEN_VERSION} installed${NC}"

# Install Gradle
echo -e "${YELLOW}Installing Gradle...${NC}"
sdk install gradle
GRADLE_VERSION=$(gradle -version | grep "Gradle" | head -n 1)
echo -e "${GREEN}✓ ${GRADLE_VERSION} installed${NC}"

# Create cache directories
mkdir -p /home/vscode/.cache/maven
mkdir -p /home/vscode/.cache/gradle

# ─────────────────────────────────────────────────────────────────────────────
# Install Java Development Tools — parallel JAR downloads
# ─────────────────────────────────────────────────────────────────────────────
echo -e "${YELLOW}Installing Java development tools...${NC}"

# Helper function: verify SHA-256 checksum of downloaded file
verify_checksum() {
    local file=$1
    local expected_sha256=$2
    local name=$3

    if [ -z "$expected_sha256" ]; then
        echo -e "${YELLOW}⚠ No checksum provided for ${name}, skipping verification${NC}"
        return 0
    fi

    # Check file exists
    if [ ! -f "$file" ]; then
        echo -e "${RED}✗ File not found: ${file}${NC}"
        return 1
    fi

    # Compute checksum with error handling
    local actual_sha256
    if ! actual_sha256=$(sha256sum "$file" 2>/dev/null | cut -d' ' -f1); then
        echo -e "${RED}✗ Failed to compute checksum for ${name}${NC}"
        rm -f "$file"
        return 1
    fi

    if [ -z "$actual_sha256" ]; then
        echo -e "${RED}✗ Empty checksum computed for ${name}${NC}"
        rm -f "$file"
        return 1
    fi

    if [ "$actual_sha256" = "$expected_sha256" ]; then
        echo -e "${GREEN}✓ ${name} checksum verified${NC}"
        return 0
    else
        echo -e "${RED}✗ ${name} checksum mismatch!${NC}"
        echo -e "${RED}  Expected: ${expected_sha256}${NC}"
        echo -e "${RED}  Actual:   ${actual_sha256}${NC}"
        rm -f "$file"
        return 1
    fi
}

mkdir -p /home/vscode/.local/share/java
mkdir -p /home/vscode/.local/bin

# Download all 3 tools in parallel
(
    # Google Java Format
    GOOGLE_JAVA_FORMAT_VERSION=$(get_github_latest_version_or_empty "google/google-java-format")
    GOOGLE_JAVA_FORMAT_VERSION="${GOOGLE_JAVA_FORMAT_VERSION#v}"
    if [ -z "$GOOGLE_JAVA_FORMAT_VERSION" ]; then
        echo -e "${YELLOW}⚠ Failed to resolve google-java-format version, skipping${NC}"
        exit 0
    fi
    echo -e "${YELLOW}Installing Google Java Format ${GOOGLE_JAVA_FORMAT_VERSION}...${NC}"
    GOOGLE_JAVA_FORMAT_JAR="/home/vscode/.local/share/java/google-java-format.jar"
    if curl -fsSL "https://github.com/google/google-java-format/releases/download/v${GOOGLE_JAVA_FORMAT_VERSION}/google-java-format-${GOOGLE_JAVA_FORMAT_VERSION}-all-deps.jar" \
        -o "$GOOGLE_JAVA_FORMAT_JAR" && [ -f "$GOOGLE_JAVA_FORMAT_JAR" ] && [ -s "$GOOGLE_JAVA_FORMAT_JAR" ]; then
        echo -e "${GREEN}✓ Google Java Format installed${NC}"
    else
        echo -e "${RED}✗ Google Java Format download failed${NC}"
        exit 1
    fi
) &
GJF_PID=$!

(
    # Checkstyle (tag format: "checkstyle-X.Y.Z" — shared helper strips 'v' prefix only,
    # so we fetch the raw tag and strip the "checkstyle-" prefix manually)
    CHECKSTYLE_TAG=""
    _cs_auth=()
    [[ -n "${GITHUB_TOKEN:-}" ]] && _cs_auth=(-H "Authorization: token ${GITHUB_TOKEN}")
    for _attempt in 1 2 3; do
        CHECKSTYLE_TAG=$(curl -fsS --connect-timeout 5 --max-time 10 \
            "${_cs_auth[@]}" \
            "https://api.github.com/repos/checkstyle/checkstyle/releases/latest" 2>/dev/null \
            | grep -o '"tag_name": *"[^"]*"' | head -1 | cut -d'"' -f4) || true
        [[ -n "$CHECKSTYLE_TAG" ]] && break
        sleep $((_attempt * 2))
    done
    CHECKSTYLE_VERSION="${CHECKSTYLE_TAG#checkstyle-}"
    if [ -z "$CHECKSTYLE_VERSION" ]; then
        echo -e "${YELLOW}⚠ Failed to resolve checkstyle version, skipping${NC}"
        exit 0
    fi
    echo -e "${YELLOW}Installing Checkstyle ${CHECKSTYLE_VERSION}...${NC}"
    CHECKSTYLE_JAR="/home/vscode/.local/share/java/checkstyle.jar"
    if curl -fsSL "https://github.com/checkstyle/checkstyle/releases/download/checkstyle-${CHECKSTYLE_VERSION}/checkstyle-${CHECKSTYLE_VERSION}-all.jar" \
        -o "$CHECKSTYLE_JAR" && [ -f "$CHECKSTYLE_JAR" ] && [ -s "$CHECKSTYLE_JAR" ]; then
        echo -e "${GREEN}✓ Checkstyle installed${NC}"
    else
        echo -e "${RED}✗ Checkstyle download failed${NC}"
        exit 1
    fi
) &
CS_PID=$!

(
    # SpotBugs (tag format: "X.Y.Z" — shared helper works directly)
    SPOTBUGS_VERSION=$(get_github_latest_version_or_empty "spotbugs/spotbugs")
    if [ -z "$SPOTBUGS_VERSION" ]; then
        echo -e "${YELLOW}⚠ Failed to resolve spotbugs version, skipping${NC}"
        exit 0
    fi
    echo -e "${YELLOW}Installing SpotBugs ${SPOTBUGS_VERSION}...${NC}"
    SPOTBUGS_DIR="/home/vscode/.local/share/spotbugs"
    SPOTBUGS_TGZ="/tmp/spotbugs.tgz"
    mkdir -p "$SPOTBUGS_DIR"
    if curl -fsSL "https://github.com/spotbugs/spotbugs/releases/download/${SPOTBUGS_VERSION}/spotbugs-${SPOTBUGS_VERSION}.tgz" \
        -o "$SPOTBUGS_TGZ" && [ -f "$SPOTBUGS_TGZ" ] && [ -s "$SPOTBUGS_TGZ" ]; then
        tar -xzf "$SPOTBUGS_TGZ" -C "$SPOTBUGS_DIR" --strip-components=1
        echo -e "${GREEN}✓ SpotBugs ${SPOTBUGS_VERSION} installed${NC}"
        rm -f "$SPOTBUGS_TGZ"
    else
        echo -e "${RED}✗ SpotBugs download failed${NC}"
        rm -f "$SPOTBUGS_TGZ"
        exit 1
    fi
) &
SB_PID=$!

# Wait for all downloads to complete
DOWNLOAD_FAILED=0
wait "$GJF_PID" || DOWNLOAD_FAILED=1
wait "$CS_PID" || DOWNLOAD_FAILED=1
wait "$SB_PID" || DOWNLOAD_FAILED=1

if [ "$DOWNLOAD_FAILED" -ne 0 ]; then
    echo -e "${RED}✗ One or more Java tool downloads failed${NC}"
    exit 1
fi

# Create wrapper scripts
# Create wrapper scripts only if the corresponding tool was installed
if [ -f /home/vscode/.local/share/java/google-java-format.jar ]; then
    cat > /home/vscode/.local/bin/google-java-format << 'EOF'
#!/bin/bash
java -jar /home/vscode/.local/share/java/google-java-format.jar "$@"
EOF
    chmod +x /home/vscode/.local/bin/google-java-format
fi

if [ -f /home/vscode/.local/share/java/checkstyle.jar ]; then
    cat > /home/vscode/.local/bin/checkstyle << 'EOF'
#!/bin/bash
java -jar /home/vscode/.local/share/java/checkstyle.jar "$@"
EOF
    chmod +x /home/vscode/.local/bin/checkstyle
fi

if [ -d /home/vscode/.local/share/spotbugs/bin ]; then
    cat > /home/vscode/.local/bin/spotbugs << 'EOF'
#!/bin/bash
/home/vscode/.local/share/spotbugs/bin/spotbugs "$@"
EOF
    chmod +x /home/vscode/.local/bin/spotbugs
fi

echo -e "${GREEN}✓ Java development tools installed${NC}"

print_success_banner "Java environment" 2>/dev/null || {
    echo ""
    echo -e "${GREEN}=========================================${NC}"
    echo -e "${GREEN}Java environment installed successfully!${NC}"
    echo -e "${GREEN}=========================================${NC}"
    echo ""
}
echo "Installed components:"
echo "  - SDKMAN (SDK Manager)"
echo "  - ${JAVA_VERSION}"
echo "  - ${MAVEN_VERSION}"
echo "  - ${GRADLE_VERSION}"
echo ""
echo "Development tools:"
if [ -f /home/vscode/.local/share/java/google-java-format.jar ]; then
    echo "  - Google Java Format (formatter)"
else
    echo "  - Google Java Format (skipped — version resolution failed)"
fi
if [ -f /home/vscode/.local/share/java/checkstyle.jar ]; then
    echo "  - Checkstyle (style checker)"
else
    echo "  - Checkstyle (skipped — version resolution failed)"
fi
if [ -d /home/vscode/.local/share/spotbugs/bin ]; then
    echo "  - SpotBugs (bug detector)"
else
    echo "  - SpotBugs (skipped — version resolution failed)"
fi
echo ""
echo "Cache directories:"
echo "  - SDKMAN: $SDKMAN_DIR"
echo "  - Maven: /home/vscode/.cache/maven"
echo "  - Gradle: $GRADLE_USER_HOME"
echo ""
