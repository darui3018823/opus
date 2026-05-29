# Rebuilds the instrumented-libopus CELT oracle used to debug bit-exact decoding.
# Run from anywhere (paths are absolute-ish via $PSScriptRoot).
#
#   pwsh scripts/oracle/build.ps1
#   $env:TEMP\opusoracle\oracle.exe <path-to>\testvectorNN.bit <pktIndex>
#
# The oracle decodes ONE CELT-only packet with REAL libopus internals and prints,
# to stderr, the range-coder state (ec_tell / ec_tell_frac / rng) after every decode
# stage, plus per-band (QB band..) and per-decode_pulses (DP n=..) traces, and the
# final range vs the .bit-stored expected value.
#
# Requires: gcc (msys2 mingw64), curl, tar. No cmake/make needed.

$ErrorActionPreference = 'Stop'
$srcRoot = "$env:TEMP\opussrc"
$srcDir  = "$srcRoot\opus-1.5.2"
$bld     = "$env:TEMP\opusoracle"
New-Item -ItemType Directory -Force $srcRoot | Out-Null
New-Item -ItemType Directory -Force $bld | Out-Null

if (-not (Test-Path "$srcDir\celt\bands.c")) {
    Write-Host "Downloading opus 1.5.2 source..."
    $tar = "$srcRoot\opus.tar.gz"
    curl.exe -sL -o $tar https://github.com/xiph/opus/releases/download/v1.5.2/opus-1.5.2.tar.gz
    tar -xzf $tar -C $srcRoot
}

# Use the instrumented copies kept in the repo (this folder) instead of the
# stock celt_decoder.c / bands.c / cwrs.c.
Copy-Item "$PSScriptRoot\celt_decoder_instr.c" "$bld\celt_decoder_instr.c" -Force
Copy-Item "$PSScriptRoot\bands_instr.c"        "$bld\bands_instr.c"        -Force
Copy-Item "$PSScriptRoot\cwrs_instr.c"         "$bld\cwrs_instr.c"         -Force
Copy-Item "$PSScriptRoot\oracle.c"             "$bld\oracle.c"             -Force

$celt = "$srcDir\celt"
# Stock decode-subset sources MINUS the three we instrumented (and minus encoder/demo).
$stock = @('celt','celt_lpc','entcode','entdec','entenc','kiss_fft','laplace',
           'mathops','mdct','modes','pitch','quant_bands','rate','vq') |
         ForEach-Object { "$celt\$_.c" }
$srcs = $stock + @("$bld\bands_instr.c","$bld\cwrs_instr.c","$bld\celt_decoder_instr.c","$bld\oracle.c")
$inc  = @("-I$celt","-I$srcDir\include","-I$srcDir","-I$srcDir\silk")

# NOTE: build WITHOUT -DCUSTOM_MODES so the exact static 48000/960 mode (shipped tables) is used.
& gcc -O1 -DOPUS_BUILD -DVAR_ARRAYS @inc @srcs -lm -o "$bld\oracle.exe"
if ($LASTEXITCODE -eq 0) { Write-Host "OK: $bld\oracle.exe" } else { Write-Host "BUILD FAILED" }
