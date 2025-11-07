param(
  [string]$Path = ".",
  [int]$Depth = 4,
  [switch]$FoldersOnly = $false,
  [string[]]$Ignore = @(".git","node_modules",".idea",".vscode",".DS_Store"),
  [switch]$Ascii = $false
)

# --- hübsche Linien (UTF-8); wenn Terminal zickt, nutze -Ascii ---
try { [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding $false } catch {}

# --- Linien-Zeichen (ASCII-Fallback möglich) ---
if ($Ascii) {
  $chars = @{ vert="|  "; tee="+- "; elbow="\- "; space="   " }
} else {
  $chars = @{ vert="│  "; tee="├─ "; elbow="└─ "; space="   " }
}

function Should-Ignore([IO.FileSystemInfo]$item, [string[]]$patterns) {
  foreach ($p in $patterns) {
    if ($item.Name -like $p) { return $true }
    if ($item.FullName -like "*$p*") { return $true }
  }
  return $false
}

function Get-Children([string]$dir, [bool]$foldersOnly) {
  $all = @(Get-ChildItem -LiteralPath $dir -Force:$false -ErrorAction SilentlyContinue)
  if ($foldersOnly) { $all = @($all | Where-Object { $_.PSIsContainer }) }

  # Ignore anwenden
  $all = @($all | Where-Object { -not (Should-Ignore $_ $Ignore) })

  # Verzeichnisse zuerst, dann Dateien (alphabetisch)
  $dirs  = @($all | Where-Object { $_.PSIsContainer } | Sort-Object Name)
  $files = @($all | Where-Object { -not $_.PSIsContainer } | Sort-Object Name)
  return @($dirs + $files)
}

function Print-Tree([string]$currentPath, [int]$remaining, [string]$prefix = "") {
  if ($remaining -lt 0) { return }

  $children = @(Get-Children $currentPath $FoldersOnly)
  for ($i = 0; $i -lt $children.Count; $i++) {
    $isLast = ($i -eq $children.Count - 1)
    $item = $children[$i]
    $connector = $chars.tee
    if ($isLast) { $connector = $chars.elbow }

    # Zeile ausgeben
    Write-Output ("{0}{1}{2}" -f $prefix, $connector, $item.Name)

    # Bei Ordnern rekursiv tiefer, solange Tiefe vorhanden
    if ($item.PSIsContainer -and $remaining -gt 0) {
      $pad = $chars.vert
      if ($isLast) { $pad = $chars.space }
      $newPrefix = $prefix + $pad
      Print-Tree -currentPath $item.FullName -remaining ($remaining - 1) -prefix $newPrefix
    }
  }
}

# --- Root vorbereiten ---
$resolvedObj = Resolve-Path -LiteralPath $Path
$resolved = $resolvedObj.Path
$rootLabel = "."
if ($Path -ne "." -and $Path -ne ".\") {
  $rootLabel = Split-Path -Leaf $resolved
}

# Ausgabe starten
Write-Output $rootLabel
Print-Tree -currentPath $resolved -remaining $Depth -prefix ""
