# Script: push-release
# Description: Automated CWM release tagger, cross-platform builder, and git deployer
#
param (
    [string]$Version = "%Version%"
)

if (-not $Version -or $Version -eq "%Version%") {
    Write-Error "Error: Release version is required (e.g. v2.0.0)."
    Exit 1
}

if (-not ($Version.StartsWith("v"))) {
    $Version = "v$Version"
}

Write-Host "==========================================" -ForegroundColor Cyan
Write-Host " STARTING CWM RELEASE AUTOMATION: $Version" -ForegroundColor Cyan
Write-Host "==========================================" -ForegroundColor Cyan

# 1. Stage any pending code changes
Write-Host "`n[1/5] Staging code changes..." -ForegroundColor Yellow
git add .
$status = git status --porcelain
if ($status) {
    git commit -m "chore: prepare codebase for release $Version"
}

# 2. Tag current release commit
Write-Host "`n[2/5] Creating Git tag $Version..." -ForegroundColor Yellow
git tag -a "$Version" -m "Release $Version" -f
git push origin "$Version" -f

# 3. Push code changes and tags to origin main
Write-Host "`n[3/5] Pushing to origin main with tags..." -ForegroundColor Yellow
git push origin main --tags

# 4. Run release build pipeline (build_release.ps1)
Write-Host "`n[4/5] Executing release build script (build_release.ps1)..." -ForegroundColor Yellow
if (Test-Path "build_release.ps1") {
    powershell -ExecutionPolicy Bypass -File "build_release.ps1"
} elseif (Test-Path "cwm-go/build_release.ps1") {
    powershell -ExecutionPolicy Bypass -File "cwm-go/build_release.ps1"
} else {
    Write-Error "build_release.ps1 not found in current directory."
    Exit 1
}

# 5. Stage generated release binaries and push final release commit
Write-Host "`n[5/5] Committing and pushing compiled release packages..." -ForegroundColor Yellow
git add release/
git commit -m "release: $Version [publish]"
git push origin main

Write-Host "`n==========================================" -ForegroundColor Green
Write-Host " RELEASE $Version SUCCESSFULLY PUBLISHED! " -ForegroundColor Green
Write-Host "==========================================" -ForegroundColor Green
