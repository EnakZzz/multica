param(
    [ValidateSet("up", "update", "status", "logs", "stop")]
    [string]$Command = "up",
    [int]$Port = 9111
)

$ErrorActionPreference = "Stop"

$RepoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$EnvFile = Join-Path $RepoRoot ".env"
$EnvExample = Join-Path $RepoRoot ".env.example"
$ComposeBuild = @("compose", "-f", "docker-compose.selfhost.yml", "-f", "docker-compose.selfhost.build.yml", "--profile", "ai-gateway")

function Invoke-DockerCompose {
    param([string[]]$CommandArgs)

    Push-Location $RepoRoot
    try {
        & docker @ComposeBuild @CommandArgs
        if ($LASTEXITCODE -ne 0) {
            throw "docker $($ComposeBuild + $CommandArgs -join ' ') failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        Pop-Location
    }
}

function Ensure-EnvFile {
    if (Test-Path $EnvFile) {
        return
    }
    if (Test-Path $EnvExample) {
        Copy-Item $EnvExample $EnvFile
    }
    else {
        New-Item -ItemType File -Path $EnvFile | Out-Null
    }
}

function Get-EnvValue {
    param(
        [string]$Name,
        [string]$Default
    )

    if (Test-Path $EnvFile) {
        $line = Get-Content $EnvFile | Where-Object { $_ -match "^$Name=" } | Select-Object -First 1
        if ($line) {
            return ($line -split "=", 2)[1]
        }
    }
    return $Default
}

function Set-AIGatewayPortEnv {
    if ($Port -le 0) {
        throw "Port must be a positive integer."
    }
    $env:AI_GATEWAY_PORT = [string]$Port
}

function Wait-AIGateway {
    $port = $env:AI_GATEWAY_PORT
    if ([string]::IsNullOrWhiteSpace($port)) {
        $port = Get-EnvValue -Name "AI_GATEWAY_PORT" -Default "9111"
    }
    Write-Host "==> Waiting for AI gateway to be ready..."
    for ($i = 0; $i -lt 30; $i++) {
        try {
            Invoke-WebRequest -Uri "http://127.0.0.1:$port/health" -UseBasicParsing -TimeoutSec 2 | Out-Null
            Write-Host ""
            Write-Host "AI gateway is running."
            Write-Host "  Base URL: http://127.0.0.1:$port/v1"
            Write-Host "  Health:   http://127.0.0.1:$port/health"
            return
        }
        catch {
            Start-Sleep -Seconds 2
        }
    }

    Write-Warning "AI gateway did not respond yet. Check logs with:"
    Write-Host "  .\scripts\docker-ai-gateway.ps1 logs"
}

switch ($Command) {
    "up" {
        Ensure-EnvFile
        Set-AIGatewayPortEnv
        Write-Host "==> Starting AI gateway container..."
        Invoke-DockerCompose -CommandArgs @("up", "-d", "--build", "--force-recreate", "ai-gateway")
        Wait-AIGateway
        Invoke-DockerCompose -CommandArgs @("ps", "ai-gateway")
    }
    "update" {
        Ensure-EnvFile
        Set-AIGatewayPortEnv
        Write-Host "==> Rebuilding and restarting AI gateway container..."
        Invoke-DockerCompose -CommandArgs @("up", "-d", "--build", "--force-recreate", "ai-gateway")
        Wait-AIGateway
        Invoke-DockerCompose -CommandArgs @("ps", "ai-gateway")
    }
    "status" {
        Set-AIGatewayPortEnv
        Invoke-DockerCompose -CommandArgs @("ps", "ai-gateway")
    }
    "logs" {
        Set-AIGatewayPortEnv
        Invoke-DockerCompose -CommandArgs @("logs", "-f", "--tail=200", "ai-gateway")
    }
    "stop" {
        Set-AIGatewayPortEnv
        Write-Host "==> Stopping AI gateway container..."
        Invoke-DockerCompose -CommandArgs @("stop", "ai-gateway")
    }
}
