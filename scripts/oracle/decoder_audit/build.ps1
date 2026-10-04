$ErrorActionPreference = 'Stop'
$bld = Join-Path ([System.IO.Path]::GetTempPath()) "opusdecaudit"
New-Item -ItemType Directory -Force $bld | Out-Null
$srcDir = (Resolve-Path (Join-Path $PSScriptRoot "../../../libopus")).Path
$celt = "$srcDir/celt"; $silk = "$srcDir/silk"; $opusSrc = "$srcDir/src"
$celtSrcs = Get-ChildItem "$celt/*.c" | Where-Object { $_.Name -notmatch '^(opus_custom_demo|dump_modes|.*_test.*)' } | ForEach-Object { $_.FullName }
$silkSrcs = Get-ChildItem "$silk/*.c" | ForEach-Object { $_.FullName }
$silkFloat = Get-ChildItem "$silk/float/*.c" | ForEach-Object { $_.FullName }
$opusStock = @('opus','opus_decoder','opus_encoder','extensions','opus_multistream','opus_multistream_encoder',
  'opus_multistream_decoder','repacketizer','opus_projection_encoder','opus_projection_decoder',
  'mapping_matrix','analysis','mlp','mlp_data') | ForEach-Object { "$opusSrc/$_.c" }
$srcs = $celtSrcs + $silkSrcs + $silkFloat + $opusStock + @("$PSScriptRoot/dec_oracle.c")
$inc = @("-I$celt", "-I$srcDir/include", "-I$srcDir", "-I$silk", "-I$silk/float", "-I$opusSrc")
& gcc -O1 -ffp-contract=off -DOPUS_BUILD -DVAR_ARRAYS -DHAVE_LRINTF @inc @srcs -lm -o "$bld/dec_oracle.exe"
if ($LASTEXITCODE -eq 0) { Write-Host "OK: $bld/dec_oracle.exe" } else { Write-Host "BUILD FAILED"; exit 1 }
