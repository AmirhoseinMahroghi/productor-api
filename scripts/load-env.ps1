param(
    [string]$Path = ".env"
)

$ErrorActionPreference = "Stop"
if (-not (Test-Path -LiteralPath $Path)) {
    throw "فایل $Path پیدا نشد؛ ابتدا .env.example را به .env کپی کنید."
}

Get-Content -LiteralPath $Path | ForEach-Object {
    $line = $_.Trim()
    if (-not $line -or $line.StartsWith("#")) { return }

    $parts = $line -split "=", 2
    if ($parts.Count -ne 2) { return }

    $name = $parts[0].Trim()
    $value = $parts[1].Trim()
    if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
        $value = $value.Substring(1, $value.Length - 2)
    }
    if ($name -match "^[A-Z][A-Z0-9_]*$") {
        [Environment]::SetEnvironmentVariable($name, $value, "Process")
    }
}
