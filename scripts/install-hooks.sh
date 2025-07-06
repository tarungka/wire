#!/bin/bash
# Script to install git hooks for Wire project

set -e

HOOKS_DIR=".git/hooks"
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

echo "🔧 Installing Git hooks for Wire..."

# Create pre-commit hook
cat > "$HOOKS_DIR/pre-commit" << 'EOF'
#!/bin/bash
# Pre-commit hook for Wire project

echo "🔍 Running pre-commit checks..."

# Check if we have any Go files staged
if git diff --cached --name-only | grep -q "\.go$"; then
    echo "📋 Running formatter..."
    make format
    
    echo "🔍 Running linter on changed files..."
    make lint-fast
    
    echo "🧪 Running quick tests..."
    make test-fast
    
    # Check if any files were reformatted
    if ! git diff --quiet; then
        echo "⚠️  Some files were reformatted. Please review and stage the changes."
        echo "   Run 'git diff' to see the changes."
        exit 1
    fi
else
    echo "ℹ️  No Go files to check"
fi

echo "✅ Pre-commit checks passed!"
EOF

# Create prepare-commit-msg hook
cat > "$HOOKS_DIR/prepare-commit-msg" << 'EOF'
#!/bin/bash
# Prepare commit message hook - adds helpful reminders

COMMIT_MSG_FILE=$1
COMMIT_SOURCE=$2

# Only add template for new commits (not amends or merges)
if [ -z "$COMMIT_SOURCE" ]; then
    cat >> "$COMMIT_MSG_FILE" << 'EOM'

# ============================================
# CI/CD Trigger Keywords (add to commit message):
#   [test]     - Run full test suite with race detection
#   [build]    - Build release binaries for all platforms
#   [release]  - Create a GitHub release (use: v1.2.3 format)
#   [security] - Run security vulnerability scans
#   [skip ci]  - Skip all CI workflows
#
# Version bumps for [release]:
#   [major]    - Bump major version (1.0.0 -> 2.0.0)
#   [minor]    - Bump minor version (1.0.0 -> 1.1.0)
#   Default    - Bump patch version (1.0.0 -> 1.0.1)
#
# Example: "feat: add clustering [test] [build]"
# ============================================
EOM
fi
EOF

# Create post-commit hook
cat > "$HOOKS_DIR/post-commit" << 'EOF'
#!/bin/bash
# Post-commit hook - reminds about CI triggers

LAST_MSG=$(git log -1 --pretty=%B)

echo ""
echo "📝 Commit created successfully!"

# Check if any triggers were used
if [[ ! "$LAST_MSG" =~ \[(test|build|release|security|skip ci)\] ]]; then
    echo ""
    echo "💡 Tip: No CI triggers detected in your commit."
    echo "   Push will only run minimal validation (3 minutes)."
    echo "   Use 'git commit --amend' to add triggers if needed."
fi

# Remind about local testing
echo ""
echo "🧪 Before pushing, you can run:"
echo "   make ci-local    # Simulate CI checks locally"
echo "   make test-full   # Run full test suite"
echo ""
EOF

# Make hooks executable
chmod +x "$HOOKS_DIR/pre-commit"
chmod +x "$HOOKS_DIR/prepare-commit-msg"
chmod +x "$HOOKS_DIR/post-commit"

echo "✅ Git hooks installed successfully!"
echo ""
echo "📋 Installed hooks:"
echo "   - pre-commit: Runs formatter, linter, and quick tests"
echo "   - prepare-commit-msg: Adds CI trigger reminders"
echo "   - post-commit: Shows tips about CI triggers"
echo ""
echo "🔧 To bypass hooks (emergency only):"
echo "   git commit --no-verify"
echo ""
echo "🗑️  To uninstall hooks:"
echo "   rm .git/hooks/pre-commit .git/hooks/prepare-commit-msg .git/hooks/post-commit"