# build_release.ps1
# This script runs GoReleaser locally to compile all binaries, packages them, 
# and structures them into the release/ directory for easy local verification 
# and git deployment.

# 1. Clean previous release folders
Write-Host "Cleaning up previous build directories..." -ForegroundColor Blue
if (Test-Path "release") { Remove-Item -Recurse -Force "release" }
if (Test-Path "dist") { Remove-Item -Recurse -Force "dist" }

# 2. Build and run GoReleaser inside custom Docker container (skips publishing to GitHub)
Write-Host "Building Docker build image (cwm-builder)..." -ForegroundColor Blue
docker build -t cwm-builder .

if ($LASTEXITCODE -ne 0) {
    Write-Error "Docker image build failed!"
    Exit 1
}

Write-Host "Running GoReleaser inside Docker to compile and package all platforms (including MSI)..." -ForegroundColor Blue
docker run --rm -v "${PWD}:/workdir" cwm-builder

if ($LASTEXITCODE -ne 0) {
    Write-Error "GoReleaser build inside Docker failed!"
    Exit 1
}

# 3. Create release directories
Write-Host "Structuring build files into release/ folders..." -ForegroundColor Blue
New-Item -ItemType Directory -Path "release/win" -Force | Out-Null
New-Item -ItemType Directory -Path "release/mac" -Force | Out-Null
New-Item -ItemType Directory -Path "release/deb" -Force | Out-Null

# 4. Copy Windows ZIP packages
Write-Host "Copying Windows packages..." -ForegroundColor Green
Get-ChildItem -Path "dist" -Filter "*.zip" -Recurse | Copy-Item -Destination "release/win" -Force

# 5. Copy Debian and RPM Linux packages
Write-Host "Copying Linux packages..." -ForegroundColor Green
Get-ChildItem -Path "dist" -Filter "*.deb" -Recurse | Copy-Item -Destination "release/deb" -Force
Get-ChildItem -Path "dist" -Filter "*.rpm" -Recurse | Copy-Item -Destination "release/deb" -Force

# 6. Copy macOS tarball packages
Write-Host "Copying macOS packages..." -ForegroundColor Green
Get-ChildItem -Path "dist" -Filter "*darwin*.tar.gz" -Recurse | Copy-Item -Destination "release/mac" -Force

# 7. Clean up intermediate dist folder
Remove-Item -Recurse -Force "dist"

Write-Host "`nRelease build completed successfully!" -ForegroundColor Green
Write-Host "Structure created:" -ForegroundColor Green
Write-Host "  - release/win/ (ZIP)"
Write-Host "  - release/mac/ (tar.gz)"
Write-Host "  - release/deb/ (DEB & RPM)"
Write-Host "`nReady to commit. Add '[publish]' to your commit message to release online." -ForegroundColor Yellow
