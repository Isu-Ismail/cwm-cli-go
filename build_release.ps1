# build_release.ps1
# Automated CWM release pipeline: Tagging, Docker GoReleaser Compilation, Package Structuring, and Git Deployer

param (
    [string]$Version = "%Version%"
)

$RootDir = "C:/Users/ismail/cwm-go/cwm-go"
Set-Location -Path $RootDir

if (-not $Version -or $Version -eq "%Version%") {
    $Version = "v2.0.0"
}

if (-not ($Version.StartsWith("v"))) {
    $Version = "v$Version"
}

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "      CWM COMPLETE RELEASE AUTOMATION PIPELINE: $Version" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. Stage and commit pending codebase changes
Write-Host "`n[1/7] Staging codebase changes..." -ForegroundColor Yellow
git add .
$status = git status --porcelain
if ($status) {
    git commit -m "chore: prepare codebase for release $Version"
}

# 2. Create and push Git tag
Write-Host "`n[2/7] Creating Git tag $Version..." -ForegroundColor Yellow
git tag -a "$Version" -m "Release $Version" -f
git push origin "$Version" -f
git push origin main --tags

# 3. Clean previous release folders
Write-Host "`n[3/7] Cleaning up previous build directories..." -ForegroundColor Blue
if (Test-Path "$RootDir/release") { Remove-Item -Recurse -Force "$RootDir/release" }
if (Test-Path "$RootDir/dist") { Remove-Item -Recurse -Force "$RootDir/dist" }

# 4. Build and run GoReleaser inside Docker
Write-Host "`n[4/7] Building Docker build image (cwm-builder)..." -ForegroundColor Blue
docker build -t cwm-builder "$RootDir"

if ($LASTEXITCODE -ne 0) {
    Write-Error "Docker image build failed!"
    Exit 1
}

Write-Host "Running GoReleaser inside Docker to compile packages..." -ForegroundColor Blue
docker run --rm -v "${RootDir}:/workdir" cwm-builder

if ($LASTEXITCODE -ne 0) {
    Write-Error "GoReleaser build inside Docker failed!"
    Exit 1
}

# 5. Create and structure release directories
Write-Host "`n[5/7] Structuring release packages..." -ForegroundColor Blue
New-Item -ItemType Directory -Path "$RootDir/release/win" -Force | Out-Null
New-Item -ItemType Directory -Path "$RootDir/release/mac" -Force | Out-Null
New-Item -ItemType Directory -Path "$RootDir/release/deb" -Force | Out-Null

Get-ChildItem -Path "$RootDir/dist" -Filter "*.zip" -Recurse | Copy-Item -Destination "$RootDir/release/win" -Force
Get-ChildItem -Path "$RootDir/dist" -Filter "*.deb" -Recurse | Copy-Item -Destination "$RootDir/release/deb" -Force
Get-ChildItem -Path "$RootDir/dist" -Filter "*.rpm" -Recurse | Copy-Item -Destination "$RootDir/release/deb" -Force
Get-ChildItem -Path "$RootDir/dist" -Filter "*darwin*.tar.gz" -Recurse | Copy-Item -Destination "$RootDir/release/mac" -Force

Remove-Item -Recurse -Force "$RootDir/dist"

# 6. Stage release binaries
Write-Host "`n[6/7] Staging release binaries..." -ForegroundColor Yellow
git add "$RootDir/release/"

# 7. Commit and push final release to origin main
Write-Host "`n[7/7] Committing and pushing final release to origin main..." -ForegroundColor Yellow
git commit -m "release: $Version [publish]"
git push origin main

Write-Host "`n==========================================================" -ForegroundColor Green
Write-Host " SUCCESS! RELEASE $Version PUBLISHED AND DEPLOYED!" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Green
