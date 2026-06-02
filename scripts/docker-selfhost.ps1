param(
    [ValidateSet("deploy", "update", "status", "logs", "stop")]
    [string]$Command = "deploy",
    [switch]$SkipDaemonUpdate
)

$ErrorActionPreference = "Stop"

$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$EnvFile = Join-Path $RepoRoot ".env"
$EnvExample = Join-Path $RepoRoot ".env.example"
$ComposeSelfhost = @("compose", "-f", "docker-compose.selfhost.yml")
$ComposeBuild = @("compose", "-f", "docker-compose.selfhost.yml", "-f", "docker-compose.selfhost.build.yml")

function Invoke-DockerCompose {
    param(
        [string[]]$ComposeArgs,
        [string[]]$CommandArgs
    )

    Push-Location $RepoRoot
    try {
        & docker @ComposeArgs @CommandArgs
        if ($LASTEXITCODE -ne 0) {
            throw "docker $($ComposeArgs + $CommandArgs -join ' ') failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

function Get-EnvValue {
    param(
        [string]$Name,
        [string]$Default
    )

    if (-not (Test-Path $EnvFile)) {
        return $Default
    }

    $match = Get-Content $EnvFile | Where-Object { $_ -match "^\s*$([regex]::Escape($Name))\s*=" } | Select-Object -First 1
    if (-not $match) {
        return $Default
    }

    $value = ($match -split "=", 2)[1].Trim().Trim('"').Trim("'")
    if ([string]::IsNullOrWhiteSpace($value)) {
        return $Default
    }
    return $value
}

function Import-GitProviderEnvFromEnvFile {
    $loaded = @()
    foreach ($name in @(
        "GITLAB_BASE_URL",
        "GITLAB_TOKEN",
        "GLAB_TOKEN",
        "GITLAB_PRIVATE_TOKEN",
        "CI_JOB_TOKEN",
        "CI_SERVER_URL"
    )) {
        $value = Get-EnvValue -Name $name -Default ""
        if ([string]::IsNullOrWhiteSpace($value)) {
            continue
        }

        [Environment]::SetEnvironmentVariable($name, $value, "Process")
        $loaded += $name
    }

    return $loaded
}

function New-HexSecret {
    $bytes = [byte[]]::new(32)
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($bytes)
    return -join ($bytes | ForEach-Object { $_.ToString("x2") })
}

function Ensure-EnvFile {
    if (Test-Path $EnvFile) {
        return
    }
    if (-not (Test-Path $EnvExample)) {
        throw "Missing .env and .env.example"
    }

    Write-Host "==> Creating .env from .env.example..."
    Copy-Item $EnvExample $EnvFile
    $jwt = New-HexSecret
    $content = Get-Content $EnvFile -Raw
    if ($content -match "(?m)^JWT_SECRET=") {
        $content = $content -replace "(?m)^JWT_SECRET=.*$", "JWT_SECRET=$jwt"
    }
    else {
        $content = $content.TrimEnd() + "`nJWT_SECRET=$jwt`n"
    }
    Set-Content -Path $EnvFile -Value $content -NoNewline
    Write-Host "==> Generated random JWT_SECRET"
}

function Wait-Backend {
    $port = Get-EnvValue -Name "PORT" -Default "8080"
    $hostAddresses = @("127.0.0.1", "localhost")
    $hostAddresses += Get-NetIPAddress -AddressFamily IPv4 |
        Where-Object {
            $_.IPAddress -notlike "127.*" -and
            $_.IPAddress -notlike "169.254.*" -and
            $_.PrefixOrigin -ne "WellKnown"
        } |
        Sort-Object InterfaceMetric |
        Select-Object -ExpandProperty IPAddress -Unique

    Write-Host "==> Waiting for backend to be ready..."
    for ($i = 0; $i -lt 30; $i++) {
        foreach ($hostAddress in $hostAddresses) {
            $url = "http://${hostAddress}:$port/health"
            try {
                Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2 | Out-Null
                Write-Host ""
                Write-Host "Multica is running."
                Write-Host "  Frontend: http://${hostAddress}:$(Get-EnvValue -Name 'FRONTEND_PORT' -Default '3000')"
                Write-Host "  Backend:  http://${hostAddress}:$port"
                return
            }
            catch {
            }
        }
        Start-Sleep -Seconds 2
    }

    Write-Host ""
    Write-Host "Services are still starting. Check logs with:"
    Write-Host "  .\scripts\docker-selfhost.ps1 logs"
}

function Get-GitValue {
    param(
        [string[]]$GitArgs,
        [string]$Default
    )

    Push-Location $RepoRoot
    try {
        $value = (& git @GitArgs 2>$null | Select-Object -First 1)
        if ([string]::IsNullOrWhiteSpace($value)) {
            return $Default
        }
        return $value.Trim()
    }
    catch {
        return $Default
    }
    finally {
        Pop-Location
    }
}

function Get-InstalledCliPath {
    return Join-Path $env:USERPROFILE ".multica\bin\multica.exe"
}

function Get-SelfhostServerUrl {
    $port = Get-EnvValue -Name "PORT" -Default "8080"
    return "http://127.0.0.1:$port"
}

function Get-SelfhostAppUrl {
    $port = Get-EnvValue -Name "FRONTEND_PORT" -Default "3000"
    return "http://127.0.0.1:$port"
}

function Build-LocalCli {
    $serverDir = Join-Path $RepoRoot "server"
    $outputDir = Join-Path $serverDir "bin"
    $outputPath = Join-Path $outputDir "multica.exe"
    $version = Get-GitValue -GitArgs @("describe", "--tags", "--always", "--dirty") -Default "dev"
    $commit = Get-GitValue -GitArgs @("rev-parse", "--short", "HEAD") -Default "unknown"
    $date = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw "Go is required to build the local Multica CLI, but 'go' was not found in PATH."
    }

    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
    Push-Location $serverDir
    try {
        Write-Host "==> Building local Multica CLI ($version)..."
        & go build -ldflags "-X main.version=$version -X main.commit=$commit -X main.date=$date" -o $outputPath ./cmd/multica
        if ($LASTEXITCODE -ne 0) {
            throw "go build ./cmd/multica failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }

    return $outputPath
}

function Invoke-CliChecked {
    param(
        [string]$CliPath,
        [string[]]$CommandArgs
    )

    & $CliPath @CommandArgs
    if ($LASTEXITCODE -ne 0) {
        throw "multica $($CommandArgs -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Set-CliSelfhostConfig {
    param([string]$CliPath)

    Invoke-CliChecked -CliPath $CliPath -CommandArgs @("config", "set", "server_url", (Get-SelfhostServerUrl))
    Invoke-CliChecked -CliPath $CliPath -CommandArgs @("config", "set", "app_url", (Get-SelfhostAppUrl))
}

function Test-CliAuthenticated {
    param([string]$CliPath)

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = & $CliPath auth status 2>&1
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    return $exitCode -eq 0 -and (($output -join "`n") -match "User:")
}

function Test-DaemonRunning {
    param([string]$CliPath)

    $previousErrorActionPreference = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        $output = & $CliPath daemon status 2>&1
    }
    finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    return ($output -join "`n") -match "running \(pid"
}

function Install-LocalCli {
    param(
        [string]$BuiltCliPath,
        [bool]$CanStopDaemon
    )

    $cliPath = Get-InstalledCliPath
    $binDir = Split-Path $cliPath -Parent
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null

    if ((Test-Path $cliPath) -and (Test-DaemonRunning -CliPath $cliPath)) {
        if (-not $CanStopDaemon) {
            Write-Warning "Daemon is running but the CLI is not authenticated from disk; leaving it untouched. Run 'multica login', then rerun update."
            return $false
        }
        Write-Host "==> Stopping daemon before replacing CLI..."
        Invoke-CliChecked -CliPath $cliPath -CommandArgs @("daemon", "stop")
    }

    if (Test-Path $cliPath) {
        $backupPath = "$cliPath.bak.before-selfhost-update.$(Get-Date -Format 'yyyyMMddHHmmss')"
        Copy-Item -LiteralPath $cliPath -Destination $backupPath -Force
        Write-Host "==> Backed up existing CLI to $backupPath"
    }

    Copy-Item -LiteralPath $BuiltCliPath -Destination $cliPath -Force
    Write-Host "==> Installed local CLI to $cliPath"
    return $true
}

function Update-LocalDaemonCli {
    if ($SkipDaemonUpdate) {
        Write-Host "==> Skipping local CLI/daemon update."
        return
    }

    $gitProviderEnv = Import-GitProviderEnvFromEnvFile
    if ($gitProviderEnv.Count -gt 0) {
        Write-Host "==> Loaded Git provider env for local daemon: $($gitProviderEnv -join ', ')"
    }

    $builtCli = Build-LocalCli
    $cliPath = Get-InstalledCliPath
    $authenticated = $false

    if (Test-Path $cliPath) {
        Set-CliSelfhostConfig -CliPath $cliPath
        $authenticated = Test-CliAuthenticated -CliPath $cliPath
    }

    $installed = Install-LocalCli -BuiltCliPath $builtCli -CanStopDaemon $authenticated
    if (-not $installed) {
        return
    }

    Set-CliSelfhostConfig -CliPath $cliPath
    if (-not $authenticated) {
        Write-Warning "Local CLI was updated, but no valid login token was found. Run 'multica login', then 'multica daemon start'."
        return
    }

    Write-Host "==> Restarting daemon with the updated CLI..."
    Invoke-CliChecked -CliPath $cliPath -CommandArgs @("daemon", "start")
    Invoke-CliChecked -CliPath $cliPath -CommandArgs @("daemon", "status")
}

switch ($Command) {
    "deploy" {
        Ensure-EnvFile
        Write-Host "==> Building Docker images from this checkout and starting Multica..."
        Invoke-DockerCompose -ComposeArgs $ComposeBuild -CommandArgs @("up", "-d", "--build")
        Wait-Backend
    }
    "update" {
        Ensure-EnvFile
        Write-Host "==> Rebuilding Docker images from this checkout and restarting Multica..."
        Invoke-DockerCompose -ComposeArgs $ComposeBuild -CommandArgs @("up", "-d", "--build")
        Wait-Backend
        Update-LocalDaemonCli
    }
    "status" {
        Invoke-DockerCompose -ComposeArgs $ComposeSelfhost -CommandArgs @("ps")
    }
    "logs" {
        Invoke-DockerCompose -ComposeArgs $ComposeSelfhost -CommandArgs @("logs", "-f", "--tail=200")
    }
    "stop" {
        Write-Host "==> Stopping Multica services..."
        Invoke-DockerCompose -ComposeArgs $ComposeSelfhost -CommandArgs @("down")
    }
}
