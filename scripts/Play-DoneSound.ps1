[CmdletBinding()]
param([string] $AudioPath = 'assets/done.mp3')

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$AudioPath = [IO.Path]::GetFullPath($AudioPath, (Split-Path -Parent $PSScriptRoot))
if (-not (Test-Path -LiteralPath $AudioPath -PathType Leaf)) {
    throw 'Completion audio file is missing.'
}

# Play without opening a media-player window. No network or paid API calls.
if ($IsMacOS) {
    $player = Get-Command afplay -CommandType Application -ErrorAction Stop
    & $player.Path $audioPath
}
else {
    $player = Get-Command ffplay -CommandType Application -ErrorAction SilentlyContinue
    if ($null -eq $player) { throw 'Completion audio requires ffplay on Windows or Linux.' }
    & $player.Path -nodisp -autoexit -loglevel error $audioPath
}
if ($LASTEXITCODE -ne 0) { throw "Completion audio player failed with exit code $LASTEXITCODE." }
