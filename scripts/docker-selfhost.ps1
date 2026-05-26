param(
    [ValidateSet("deploy", "update", "status", "logs", "stop")]
    [string]$Command = "deploy"
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
