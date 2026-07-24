# Script: push-release
# Description: Automated CWM release tagger, cross-platform builder, and git deployer
#
param (
    [string]$Version = "%Version%"
)

$RootDir = "C:/Users/ismail/cwm-go/cwm-go"
Set-Location -Path $RootDir

if (-not $Version -or $Version -eq "%Version%") {
    Write-Error "Error: Release version is required (e.g. v2.0.0)."
    Exit 1
}

if (-not ($Version.StartsWith("v"))) {
    $Version = "v$Version"
}

$ReleaseDirName = "release-$Version"
$TargetReleaseDir = "$RootDir/release/$ReleaseDirName"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "   CWM ATOMIC RELEASE PIPELINE: $Version ($ReleaseDirName)" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. Stage and commit local codebase changes (LOCAL ONLY - DO NOT PUSH YET)
Write-Host "`n[1/6] Staging local codebase changes..." -ForegroundColor Yellow
git add .
$status = git status --porcelain
if ($status) {
    git commit -m "chore: prepare codebase for release $Version"
}

# 2. Clean temporary GoReleaser dist folder (keep existing release/ archives intact)
Write-Host "`n[2/6] Cleaning temporary dist folder..." -ForegroundColor Blue
if (Test-Path "$RootDir/dist") { Remove-Item -Recurse -Force "$RootDir/dist" }

# 3. Refresh local Git tag to HEAD and build via Docker
Write-Host "`n[3/6] Refreshing local tag $Version and building Docker image..." -ForegroundColor Blue
git tag -a "$Version" -m "Release $Version" -f
docker build -t cwm-builder "$RootDir"

if ($LASTEXITCODE -ne 0) {
    Write-Error "Docker image build failed! Aborting release (nothing pushed)."
    Exit 1
}

Write-Host "Running GoReleaser inside Docker to compile packages..." -ForegroundColor Blue
docker run --rm -v "${RootDir}:/workdir" cwm-builder

if ($LASTEXITCODE -ne 0) {
    Write-Error "GoReleaser build inside Docker failed! Aborting release (nothing pushed)."
    Exit 1
}

# 4. Create versioned release directories under release/release-vX.Y.Z/
Write-Host "`n[4/6] Structuring release packages into $ReleaseDirName..." -ForegroundColor Blue
if (Test-Path "$TargetReleaseDir") { Remove-Item -Recurse -Force "$TargetReleaseDir" }

New-Item -ItemType Directory -Path "$TargetReleaseDir/win" -Force | Out-Null
New-Item -ItemType Directory -Path "$TargetReleaseDir/mac" -Force | Out-Null
New-Item -ItemType Directory -Path "$TargetReleaseDir/deb" -Force | Out-Null

Get-ChildItem -Path "$RootDir/dist" -Filter "*.zip" -Recurse | Copy-Item -Destination "$TargetReleaseDir/win" -Force
Get-ChildItem -Path "$RootDir/dist" -Filter "*.deb" -Recurse | Copy-Item -Destination "$TargetReleaseDir/deb" -Force
Get-ChildItem -Path "$RootDir/dist" -Filter "*.rpm" -Recurse | Copy-Item -Destination "$TargetReleaseDir/deb" -Force
Get-ChildItem -Path "$RootDir/dist" -Filter "*darwin*.tar.gz" -Recurse | Copy-Item -Destination "$TargetReleaseDir/mac" -Force

Remove-Item -Recurse -Force "$RootDir/dist"

# 5. ATOMIC COMMIT OF RELEASE ASSETS
Write-Host "`n[5/6] Committing release assets to repository..." -ForegroundColor Yellow
git add .
git commit -m "release: $Version [publish]"

# 6. TAG ATOMIC COMMIT AND PUSH TO REMOTE
Write-Host "`n[6/6] Tagging atomic commit and pushing to origin main & $Version..." -ForegroundColor Yellow
git tag -a "$Version" -m "Release $Version" -f
git push origin main
git push origin "$Version" -f

Write-Host "`n==========================================================" -ForegroundColor Green
Write-Host " SUCCESS! RELEASE $Version PUBLISHED ATOMICALLY!" -ForegroundColor Green
Write-Host " Target Directory: $TargetReleaseDir" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Green
