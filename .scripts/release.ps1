# release.ps1 -- Create a release tag
# Usage: .\.scripts\release.ps1 [version]
# Example: .\.scripts\release.ps1 v0.1.3

$ErrorActionPreference = "Stop"

Push-Location (Split-Path $PSScriptRoot)

$e = [char]27
$B = "$e[1m"
$D = "$e[2m"
$G = "$e[32m"
$C = "$e[36m"
$R = "$e[0m"

# Get latest tag
$allTags = git tag --sort=-v:refname
$LATEST_TAG = if ($allTags) { ($allTags | Select-Object -First 1) } else { "" }

# Show recent tags
Write-Host "`n${B}Recent tags:${R}"
$recentTags = if ($allTags) { $allTags | Select-Object -First 10 } else { @() }
foreach ($tag in $recentTags) {
    $date = (git log -1 --format='%ci' $tag 2>$null) -replace ' .*', ''
    if ($tag -eq $LATEST_TAG) {
        Write-Host "  ${C}${tag}${R}  ${D}${date}${R}"
    } else {
        Write-Host "  ${D}${tag}  ${date}${R}"
    }
}
if (-not $LATEST_TAG) {
    Write-Host "`n${D}No existing tags found.${R}"
}

# Show commits since last tag
if ($LATEST_TAG) {
    $COMMIT_COUNT = git rev-list "$LATEST_TAG..HEAD" --count
    Write-Host "`n${B}Commits since ${C}${LATEST_TAG}${R}${B} (${COMMIT_COUNT}):${R}"
    if ([int]$COMMIT_COUNT -eq 0) {
        Write-Host "  ${D}(none)${R}"
        Write-Host "`n${D}No new commits since last tag. Nothing to release.${R}"
        Pop-Location
        exit 0
    }
    git log "$LATEST_TAG..HEAD" --format="  ${D}%h${R}  %s  ${D}%ar${R}" --no-decorate | ForEach-Object { Write-Host $_ }
} else {
    Write-Host "`n${B}All commits:${R}"
    git log --format="  ${D}%h${R}  %s  ${D}%ar${R}" --no-decorate -20 | ForEach-Object { Write-Host $_ }
}

# Determine version
if ($args.Count -gt 0) {
    $NEW_TAG = $args[0]
} else {
    if ($LATEST_TAG) {
        $parts = $LATEST_TAG -split '\.'
        $parts[-1] = [int]$parts[-1] + 1
        $SUGGESTED = $parts -join '.'
    } else {
        $SUGGESTED = "v0.1.0"
    }
    Write-Host ""
    $input = Read-Host "${B}Tag version${R} [${G}${SUGGESTED}${R}]"
    $NEW_TAG = if ($input) { $input } else { $SUGGESTED }
}

# Validate tag format
if ($NEW_TAG -notmatch '^v\d+\.\d+\.\d+$') {
    Write-Host "${D}Warning: tag '${NEW_TAG}' doesn't match vX.Y.Z format${R}"
    $confirm = Read-Host "Continue anyway? [y/N]"
    if ($confirm -notmatch '^[yY]$') { Pop-Location; exit 0 }
}

# Confirm
if ($LATEST_TAG) {
    Write-Host "`n${B}Will create tag: ${C}${LATEST_TAG}${R} -> ${G}${NEW_TAG}${R}"
} else {
    Write-Host "`n${B}Will create tag: ${G}${NEW_TAG}${R}"
}
$confirm = Read-Host "Proceed? [Y/n]"
if ($confirm -match '^[nN]$') {
    Write-Host "Aborted."
    Pop-Location
    exit 0
}

# Create and push tag
git tag $NEW_TAG
Write-Host "  ${D}created tag${R} ${G}${NEW_TAG}${R}"

git push origin $NEW_TAG
Write-Host "  ${D}pushed tag${R} ${G}${NEW_TAG}${R}"

if ($LATEST_TAG) {
    Write-Host "`n${B}Release ${C}${LATEST_TAG}${R} -> ${G}${NEW_TAG}${R}${B} tagged and pushed.${R}"
} else {
    Write-Host "`n${B}Release ${G}${NEW_TAG}${R}${B} tagged and pushed.${R}"
}

Pop-Location
