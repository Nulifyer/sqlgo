#!/usr/bin/env bash
# release.sh -- Create a release tag
# Usage: ./.scripts/release.sh [version]
# Example: ./.scripts/release.sh v0.1.3

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

B='\033[1m'
D='\033[2m'
G='\033[32m'
C='\033[36m'
R='\033[0m'

# Get latest tag
LATEST_TAG=$(git tag --sort=-v:refname | head -1)

# Show recent tags
echo -e "\n${B}Recent tags:${R}"
while read -r tag; do
    date=$(git log -1 --format='%ci' "$tag" 2>/dev/null | cut -d' ' -f1)
    if [ "$tag" = "$LATEST_TAG" ]; then
        echo -e "  ${C}${tag}${R}  ${D}${date}${R}"
    else
        echo -e "  ${D}${tag}  ${date}${R}"
    fi
done < <(git tag --sort=-v:refname | head -10)
if [ -z "$LATEST_TAG" ]; then
    echo -e "\n${D}No existing tags found.${R}"
    LATEST_TAG=""
fi

# Show commits since last tag
if [ -n "$LATEST_TAG" ]; then
    COMMIT_COUNT=$(git rev-list "$LATEST_TAG"..HEAD --count)
    echo -e "\n${B}Commits since ${C}${LATEST_TAG}${R}${B} (${COMMIT_COUNT}):${R}"
    if [ "$COMMIT_COUNT" -eq 0 ]; then
        echo -e "  ${D}(none)${R}"
        echo -e "\n${D}No new commits since last tag. Nothing to release.${R}"
        exit 0
    fi
    git log "$LATEST_TAG"..HEAD --format="  ${D}%h${R}  %s  ${D}%ar${R}" --no-decorate | while IFS= read -r line; do
        echo -e "$line"
    done
else
    echo -e "\n${B}All commits:${R}"
    git log --format="  ${D}%h${R}  %s  ${D}%ar${R}" --no-decorate -20 | while IFS= read -r line; do
        echo -e "$line"
    done
fi

# Determine version
if [ -n "${1:-}" ]; then
    NEW_TAG="$1"
else
    if [ -n "$LATEST_TAG" ]; then
        SUGGESTED=$(echo "$LATEST_TAG" | awk -F. '{OFS="."; $NF=$NF+1; print}')
    else
        SUGGESTED="v0.1.0"
    fi
    echo ""
    read -rp "$(echo -e "${B}Tag version${R} [${G}${SUGGESTED}${R}]: ")" NEW_TAG
    NEW_TAG="${NEW_TAG:-$SUGGESTED}"
fi

# Validate tag format
if [[ ! "$NEW_TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo -e "${D}Warning: tag '${NEW_TAG}' doesn't match vX.Y.Z format${R}"
    read -rp "Continue anyway? [y/N]: " confirm
    [[ "$confirm" =~ ^[yY]$ ]] || exit 0
fi

# Confirm
if [ -n "$LATEST_TAG" ]; then
    echo -e "\n${B}Will create tag: ${C}${LATEST_TAG}${R} -> ${G}${NEW_TAG}${R}"
else
    echo -e "\n${B}Will create tag: ${G}${NEW_TAG}${R}"
fi
read -rp "Proceed? [Y/n]: " confirm
if [[ "$confirm" =~ ^[nN]$ ]]; then
    echo "Aborted."
    exit 0
fi

# Create and push tag
git tag "$NEW_TAG"
echo -e "  ${D}created tag${R} ${G}${NEW_TAG}${R}"

git push origin "$NEW_TAG"
echo -e "  ${D}pushed tag${R} ${G}${NEW_TAG}${R}"

if [ -n "$LATEST_TAG" ]; then
    echo -e "\n${B}Release ${C}${LATEST_TAG}${R} -> ${G}${NEW_TAG}${R}${B} tagged and pushed.${R}"
else
    echo -e "\n${B}Release ${G}${NEW_TAG}${R}${B} tagged and pushed.${R}"
fi
