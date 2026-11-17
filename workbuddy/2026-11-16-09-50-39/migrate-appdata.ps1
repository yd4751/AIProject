<#
 migrate-appdata.ps1

 Relocate the CONTENT of C:\Users\Administrator\AppData to E:\AppData using
 NTFS directory junctions.  The AppData root folder itself is NEVER moved,
 renamed or deleted - only its child folders are turned into junctions.

 SAFETY DESIGN
   * Original folders are RENAMED to "<name>.bak-<stamp>" and KEPT.
     This script never deletes any file or folder under AppData.
   * Rollback removes the junctions only (recursive=false) and restores the
     .bak folders. Data on E: is left untouched.
   * Copy phase is non-destructive: it only writes to E:.
   * Switch phase refuses to run while Administrator is logged on, and refuses
     to proceed if the final sync reports failed files (unless -Force).
   * /XJ is mandatory: AppData\Local contains 3 legacy self-referencing
     junctions (Application Data / History / Temporary Internet Files). Those
     are skipped by robocopy. They are Vista-era compatibility aliases whose
     ACL denies Everyone read access; their absence is harmless on Win10/11.

 PHASES
   Status     read-only report (default)
   Copy       seed / incremental copy to E: (safe while online)
   Switch     OFFLINE ONLY: final sync, rename originals, create junctions
   Verify     check junctions and write-through
   Rollback   remove junctions, restore .bak folders
   Schedule   register an ONSTART task (SYSTEM, before logon), then reboot
   Unschedule remove that task

 USAGE
   powershell -NoProfile -ExecutionPolicy Bypass -File .\migrate-appdata.ps1 -Phase Status
   powershell -NoProfile -ExecutionPolicy Bypass -File .\migrate-appdata.ps1 -Phase Copy -Mode All
   powershell -NoProfile -ExecutionPolicy Bypass -File .\migrate-appdata.ps1 -Phase Schedule -Mode All
   powershell -NoProfile -ExecutionPolicy Bypass -File .\migrate-appdata.ps1 -Phase Verify -Mode All
   powershell -NoProfile -ExecutionPolicy Bypass -File .\migrate-appdata.ps1 -Phase Rollback -Mode All

 Requires Administrator. Switch / Schedule / Rollback must NOT be run from a
 logged-on Administrator session - use another admin account, WinPE, or the
 ONSTART task.
#>

[CmdletBinding()]
param(
    [ValidateSet('Status','Copy','Switch','Verify','Rollback','Schedule','Unschedule')]
    [string]$Phase = 'Status',

    [ValidateSet('All','Big')]
    [string]$Mode = 'All',

    [string]$DestRoot = 'E:\AppData',

    [switch]$Force
)

$ErrorActionPreference = 'Stop'

$AppDataRoot = 'C:\Users\Administrator\AppData'
$Stamp       = Get-Date -Format 'yyyyMMdd-HHmmss'
$LogDir      = Join-Path $DestRoot '_migration_logs'
$BakTag      = ".bak-$Stamp"
$TaskName    = 'AppDataJunctionSwitch'

# Mode "All": junction the three standard children. AppData root stays on C:.
# This moves 100% of the data while keeping the logon/profile path off the link.
$AllTargets = @('Local','Roaming','LocalLow')

# Mode "Big": conservative subset - caches and non-critical app data only.
# High-risk items are intentionally commented out:
#   running executables (WXWork), UWP/credential store, browser profiles,
#   installed apps, and this agent's own updater.
$BigTargets = @(
    'Local\pip',
    'Local\npm-cache',
    'Local\go-build',
    'Local\pyppeteer',
    'Local\lm-studio-updater',
    'Local\Netease',
    'Local\Temp',
    'Roaming\Code',
    'Roaming\WXDrive',
    'Roaming\npm',
    'Roaming\Godot',
    'Roaming\bilibili',
    'Roaming\Xmind'
    # 'Roaming\Tencent',    # WXWork runs .exe from here - fully quit WXWork first
    # 'Local\Microsoft',    # Edge / Teams / UWP / credential store
    # 'Local\Programs',     # installed applications
    # 'Local\Google',       # Chrome profile - quit Chrome first
    # 'Local\Postman',
    # 'Roaming\Postman',
    # 'Local\@genieworkbuddy-desktop-updater'
)

$Targets = if ($Mode -eq 'All') { $AllTargets } else { $BigTargets }

function Assert-Admin {
    $p = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Administrator privileges required.'
    }
}

function Get-SrcPath ([string]$rel) { Join-Path $AppDataRoot $rel }
function Get-DstPath ([string]$rel) { Join-Path $DestRoot   $rel }

function Get-LinkInfo ([string]$path) {
    if (-not (Test-Path -LiteralPath $path)) { return $null }
    $i = Get-Item -LiteralPath $path -Force
    [pscustomobject]@{
        IsLink = [bool]($i.Attributes -band [IO.FileAttributes]::ReparsePoint)
        Target = ($i.Target -join ';')
        Mode   = $i.Attributes.ToString()
    }
}

function Get-Backups ([string]$src) {
    $leaf   = Split-Path $src -Leaf
    $parent = Split-Path $src -Parent
    @(Get-ChildItem -LiteralPath $parent -Force -Directory -Filter "$leaf.bak-*" -ErrorAction SilentlyContinue |
        Sort-Object Name -Descending)
}

function Invoke-Robo ([string]$src, [string]$dst, [string]$logPath) {
    if (-not (Test-Path -LiteralPath $dst)) {
        New-Item -ItemType Directory -Path $dst -Force | Out-Null
    }
    # /E          include empty dirs
    # /COPY:DATSO data + attributes + timestamps + ACL + owner (no auditing flag)
    # /DCOPY:DAT  keep directory timestamps
    # /XJ         exclude junctions - MANDATORY here
    # /R:1 /W:1   short retry on locked files
    # NO /MIR     never delete anything at the destination
    #
    # /LOG+ file content is ALWAYS English, but console output is localized
    # (Chinese on this host). Parse the log file first and fall back to the
    # console text, accepting both label sets. The exit code remains the
    # authoritative failure signal because it is locale independent.
    $rcArgs = @(
        $src, $dst,
        '/E','/COPY:DATSO','/DCOPY:DAT','/XJ',
        '/R:1','/W:1','/MT:8','/NFL','/NDL','/NP',
        "/LOG+:$logPath"
    )
    $console = @(& robocopy.exe @rcArgs)
    $code    = $LASTEXITCODE

    $fromLog = if (Test-Path -LiteralPath $logPath) { @(Get-Content -LiteralPath $logPath) } else { @() }
    $sources = @($fromLog) + @($console)

    $result = [pscustomobject]@{
        ExitCode = $code
        Total    = 0
        Copied   = 0
        Failed   = 0
        HardFail = ($code -ge 8)   # robocopy 0..7 = success/informational, >=8 = failure
        Log      = $logPath
    }
    # Summary columns: Total Copied Skipped Mismatch FAILED Extras
    # English labels "Files :" / "Dirs :", Chinese labels "文件:" / "目录:".
    foreach ($pair in @(@('Files','文件'), @('Dirs','目录'))) {
        $pattern = "^\s*({0})\s*:\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)" -f ($pair -join '|')
        $line = $sources | Where-Object { $_ -match $pattern } | Select-Object -Last 1
        if ($line -and $line -match $pattern) {
            if ($pair[0] -eq 'Files') {
                $result.Total  = [int]$Matches[2]
                $result.Copied = [int]$Matches[3]
            }
            $result.Failed += [int]$Matches[6]
        }
    }
    return $result
}

function Get-Holders {
    # Best-effort: processes whose executable lives inside AppData.
    Get-Process -ErrorAction SilentlyContinue | Where-Object {
        $_.Path -and $_.Path.StartsWith($AppDataRoot, [StringComparison]::OrdinalIgnoreCase)
    } | Select-Object ProcessName, Id, Path
}

function Do-Status {
    Write-Host "AppDataRoot : $AppDataRoot  (never moved)"
    Write-Host "DestRoot    : $DestRoot"
    Write-Host "Mode        : $Mode   Targets: $($Targets.Count)"
    $rows = foreach ($rel in $Targets) {
        $src = Get-SrcPath $rel
        $dst = Get-DstPath $rel
        $li  = Get-LinkInfo $src
        $state = if (-not $li) { 'MISSING' } elseif ($li.IsLink) { 'JUNCTION -> ' + $li.Target } else { 'REAL DIR' }
        [pscustomobject]@{
            Target     = $rel
            Source     = $state
            DestExists = (Test-Path -LiteralPath $dst)
            Backups    = (Get-Backups $src).Count
        }
    }
    $rows | Format-Table -AutoSize | Out-String | Write-Host
    $drive = Get-PSDrive -Name E -ErrorAction SilentlyContinue
    if ($drive) { Write-Host ("E: free = {0} GB" -f [math]::Round($drive.Free/1GB,1)) }
    $h = @(Get-Holders)
    Write-Host "Processes running from inside AppData: $($h.Count)"
    if ($h.Count -gt 0) { $h | Format-Table -AutoSize | Out-String | Write-Host }
}

function Do-Copy {
    Assert-Admin
    New-Item -ItemType Directory -Path $LogDir -Force | Out-Null
    Write-Host "Copy phase: $($Targets.Count) target(s), Mode=$Mode -> $DestRoot"
    Write-Host "Nothing on C: will be modified."
    foreach ($rel in $Targets) {
        $src = Get-SrcPath $rel
        $dst = Get-DstPath $rel
        if (-not (Test-Path -LiteralPath $src)) { Write-Warning "Skip missing source: $src"; continue }
        $li = Get-LinkInfo $src
        if ($li.IsLink) { Write-Host "[$rel] already a junction -> $($li.Target), skip"; continue }
        $log = Join-Path $LogDir ("copy-" + ($rel -replace '\\','_') + ".log")
        Write-Host "[$rel] copying $src -> $dst ..."
        $r = Invoke-Robo $src $dst $log
        Write-Host ("[$rel] exit={0} total={1} copied={2} failed={3}" -f $r.ExitCode, $r.Total, $r.Copied, $r.Failed)
        if ($r.HardFail -or $r.Failed -gt 0) {
            Write-Warning "[$rel] locked/skipped files - normal while online. Switch phase re-syncs and re-checks. Log: $log"
        }
    }
    Write-Host "Copy phase finished. C: untouched."
}

function Do-Switch {
    Assert-Admin
    New-Item -ItemType Directory -Path $LogDir -Force | Out-Null

    if ($env:USERNAME -eq 'Administrator' -and -not $Force) {
        throw "Refusing: user '$env:USERNAME' is logged on. Run from another admin account, WinPE, or via -Phase Schedule."
    }
    $holders = @(Get-Holders)
    if ($holders.Count -gt 0 -and -not $Force) {
        $holders | Format-Table -AutoSize | Out-String | Write-Host
        throw "Processes are still running from inside AppData. Close them, or pass -Force to accept the risk."
    }

    # ---- Pre-flight: verify everything BEFORE touching C: ----
    $plan = @()
    foreach ($rel in $Targets) {
        $src = Get-SrcPath $rel
        $dst = Get-DstPath $rel
        $li  = Get-LinkInfo $src
        if ($li -and $li.IsLink) { continue }   # already switched
        if (-not $li)            { throw "Pre-flight failed: source missing $src" }
        if (-not (Test-Path -LiteralPath $dst)) { throw "Pre-flight failed: destination missing $dst (run -Phase Copy first)" }
        if ((Get-Backups $src).Count -gt 0)     { throw "Pre-flight failed: a .bak folder already exists for $src (interrupted run?). Run -Phase Status / -Phase Rollback first." }
        $plan += $rel
    }
    if ($plan.Count -eq 0) { Write-Host 'Nothing to switch - all targets are already junctions.'; return }
    Write-Host "Pre-flight OK. Will switch: $($plan -join ', ')"
    Write-Host "Originals will be renamed to *$BakTag and KEPT for rollback."

    foreach ($rel in $plan) {
        $src    = Get-SrcPath $rel
        $dst    = Get-DstPath $rel
        $leaf   = Split-Path $src -Leaf
        $parent = Split-Path $src -Parent
        $bak    = Join-Path $parent "$leaf$BakTag"

        # final incremental sync
        $log = Join-Path $LogDir ("switch-" + ($rel -replace '\\','_') + ".log")
        Write-Host "[$rel] final incremental sync ..."
        $r = Invoke-Robo $src $dst $log
        Write-Host ("[$rel] exit={0} total={1} copied={2} failed={3}" -f $r.ExitCode, $r.Total, $r.Copied, $r.Failed)
        if ($r.HardFail -or $r.Failed -gt 0) {
            $failedPaths = @(Get-Content -LiteralPath $log -ErrorAction SilentlyContinue |
                Where-Object { $_ -match 'Copying File (.+)$' } |
                ForEach-Object { $Matches[1].Trim() } | Sort-Object -Unique)
            if ($failedPaths.Count -gt 0) {
                Write-Host "[$rel] failed items ($($failedPaths.Count)):"
                $failedPaths | ForEach-Object { Write-Host "  $_" }
            }
            if (-not $Force) {
                throw "[$rel] final sync has $($r.Failed) failed item(s), exit=$($r.ExitCode). NO changes made to C:. Inspect $log, then re-run. Use -Force ONLY if every remaining failure is a disposable cache/auth file - never if UsrClass.dat or any settings.dat is still listed."
            }
            Write-Warning "[$rel] continuing despite failures because -Force was supplied"
        }

        Write-Host "[$rel] rename original -> $bak"
        Rename-Item -LiteralPath $src -NewName "$leaf$BakTag" -ErrorAction Stop

        try {
            New-Item -ItemType Junction -Path $src -Target $dst -ErrorAction Stop | Out-Null
            Write-Host "[$rel] junction created: $src -> $dst"
        }
        catch {
            Write-Warning "[$rel] junction creation failed ($($_.Exception.Message)); rolling back this rename"
            Rename-Item -LiteralPath $bak -NewName $leaf -ErrorAction SilentlyContinue
            throw
        }
    }
    # Remove our own startup task so it does not keep running on every boot.
    if (Get-Command Unregister-ScheduledTask -ErrorAction SilentlyContinue) {
        if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
            Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
            Write-Host "Startup task '$TaskName' removed (switch completed)."
        }
    }
    Write-Host 'Switch phase finished. Run -Phase Verify after logging back in.'
}

function Do-Verify {
    $rows = foreach ($rel in $Targets) {
        $src = Get-SrcPath $rel
        $dst = Get-DstPath $rel
        $li  = Get-LinkInfo $src
        $probeName = "_junction_probe_$Stamp.tmp"
        $ok = $false
        if ($li -and $li.IsLink) {
            try {
                Set-Content -LiteralPath (Join-Path $src $probeName) -Value 'ok' -ErrorAction Stop
                $ok = Test-Path -LiteralPath (Join-Path $dst $probeName)
                Remove-Item -LiteralPath (Join-Path $src $probeName) -Force -ErrorAction SilentlyContinue
            } catch { $ok = $false }
        }
        [pscustomobject]@{
            Target      = $rel
            IsJunction  = [bool]($li -and $li.IsLink)
            ResolvesTo  = if ($li) { $li.Target } else { '' }
            WriteThrough= $ok
            Backups     = (Get-Backups $src).Count
        }
    }
    $rows | Format-Table -AutoSize | Out-String | Write-Host

    # Logon-critical paths. After the switch these resolve through the junction,
    # so running this while logged on proves the data really lives on E:.
    $critical = @(
        'Local\Microsoft\Windows\UsrClass.dat',
        'Local\Microsoft\Windows\WebCache\WebCacheV01.dat',
        'Roaming\Tencent\WXWork'
    )
    Write-Host 'Key paths:'
    foreach ($c in $critical) {
        $p = Join-Path $AppDataRoot $c
        $tag = if (Test-Path -LiteralPath $p) { 'OK     ' } else { 'MISSING' }
        Write-Host "  [$tag] $c"
    }
    Write-Host ''

    if (@($rows | Where-Object { -not $_.IsJunction }).Count -gt 0) {
        Write-Warning 'Some targets are still real directories - switch not complete for them.'
    } else {
        Write-Host 'All targets are junctions. Keep the .bak folders for a few days before removing them manually.'
    }
}

function Do-Rollback {
    Assert-Admin
    foreach ($rel in $Targets) {
        $src    = Get-SrcPath $rel
        $leaf   = Split-Path $src -Leaf
        $li     = Get-LinkInfo $src
        if ($li -and $li.IsLink) {
            # Delete the LINK ONLY. recursive=false never touches the E: content.
            [System.IO.Directory]::Delete($src, $false)
            Write-Host "[$rel] junction removed (E: data untouched)"
        }
        $baks = Get-Backups $src
        if ($baks.Count -gt 0) {
            if (Test-Path -LiteralPath $src) { throw "Cannot restore: $src still exists." }
            Rename-Item -LiteralPath $baks[0].FullName -NewName $leaf -ErrorAction Stop
            Write-Host "[$rel] restored from $($baks[0].Name)"
        } else {
            Write-Host "[$rel] no .bak folder found, nothing to restore"
        }
    }
    Write-Host 'Rollback finished. E: copies remain on disk.'
}

function Do-Schedule {
    Assert-Admin
    if (-not $PSCommandPath) { throw 'Cannot resolve script path; run -Phase Switch manually instead.' }
    if (-not (Get-Command Register-ScheduledTask -ErrorAction SilentlyContinue)) {
        throw 'ScheduledTasks module unavailable. Use another admin account or WinPE and run -Phase Switch manually.'
    }
    # Deliberately NOT passing -Force: the startup run must stay subject to the
    # final-sync FAILED check, otherwise a locked UsrClass.dat could slip through.
    $arg = '-NoProfile -ExecutionPolicy Bypass -File "{0}" -Phase Switch -Mode {1}' -f $PSCommandPath, $Mode
    $action    = New-ScheduledTaskAction    -Execute 'powershell.exe' -Argument $arg
    $trigger   = New-ScheduledTaskTrigger  -AtStartup
    $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
    $settings  = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Minutes 120) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    Write-Host "Task '$TaskName' registered (ONSTART, SYSTEM, before logon)."
    Write-Host 'IMPORTANT: after reboot, WAIT at the logon screen ~2 minutes before signing in, so the task can finish.'
    Write-Host 'Then run -Phase Verify. To cancel BEFORE rebooting: -Phase Unschedule'
    Write-Host 'If Verify shows the switch did not happen, use another admin account / WinPE and run -Phase Switch manually.'
}

function Do-Unschedule {
    Assert-Admin
    if (Get-Command Unregister-ScheduledTask -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
        Write-Host "Task '$TaskName' removed."
    } else {
        Write-Host 'ScheduledTasks module unavailable; nothing removed.'
    }
}

switch ($Phase) {
    'Status'     { Do-Status }
    'Copy'       { Do-Copy }
    'Switch'     { Do-Switch }
    'Verify'     { Do-Verify }
    'Rollback'   { Do-Rollback }
    'Schedule'   { Do-Schedule }
    'Unschedule' { Do-Unschedule }
}
