# Verifies the Godot 4.7.2 Android export environment installed under E:\install
# Usage: powershell -NoProfile -ExecutionPolicy Bypass -File verify-android-env.ps1
$ErrorActionPreference = "Continue"
$script:pass = 0
$script:fail = 0

function Check([string]$name, [bool]$ok, [string]$detail) {
    if ($ok) { $script:pass++; "  PASS  $name  ($detail)" }
    else { $script:fail++; "  FAIL  $name  ($detail)" }
}

$root   = "E:\install"
$jdk    = Join-Path $root "jdk-17"
$sdk    = Join-Path $root "android-sdk"
$godota = Join-Path $env:APPDATA "Godot"
$tpl    = Join-Path $godota "export_templates\4.7.2.stable"
$ks     = Join-Path $godota "keystores\debug.keystore"
$java   = Join-Path $jdk "bin\java.exe"
$kt     = Join-Path $jdk "bin\keytool.exe"
$smJar  = Join-Path $sdk "cmdline-tools\latest\lib\sdkmanager-classpath.jar"
$tools  = Join-Path $sdk "cmdline-tools\latest"

"=== Godot 4.7.2 Android export environment ==="
"JDK     : $jdk"
"SDK     : $sdk"
"Template: $tpl"
""

Check "JDK 17 present" (Test-Path $java) $java
if (Test-Path $java) {
    $v = & $java -version 2>&1 | Select-Object -First 1
    Check "JDK major version 17" ($v -match 'version "17') "$v"
}

Check "sdkmanager jar present" (Test-Path $smJar) $smJar
if ((Test-Path $java) -and (Test-Path $smJar)) {
    $sv = & $java "-Dcom.android.sdklib.toolsdir=$tools" -classpath $smJar com.android.sdklib.tool.sdkmanager.SdkManagerCli --version 2>&1
    Check "sdkmanager runs on JDK 17" ($LASTEXITCODE -eq 0) "version $sv"
}

Check "platform-tools/adb.exe" (Test-Path (Join-Path $sdk "platform-tools\adb.exe")) "adb"
Check "build-tools 35.0.1"    (Test-Path (Join-Path $sdk "build-tools\35.0.1\aapt2.exe")) "aapt2"
Check "platform android-35"   (Test-Path (Join-Path $sdk "platforms\android-35\android.jar")) "android.jar"
Check "SDK licenses accepted" (Test-Path (Join-Path $sdk "licenses\android-sdk-license")) "license file"

Check "export template dir" (Test-Path $tpl) $tpl
Check "version.txt"         (Test-Path (Join-Path $tpl "version.txt")) "4.7.2.stable"
Check "android_debug.apk"   (Test-Path (Join-Path $tpl "android_debug.apk")) "debug template"
Check "android_release.apk" (Test-Path (Join-Path $tpl "android_release.apk")) "release template"

Check "debug keystore" (Test-Path $ks) $ks
if ((Test-Path $kt) -and (Test-Path $ks)) {
    $out = & $kt -list -keystore $ks -storepass android 2>&1
    Check "keystore readable" ($LASTEXITCODE -eq 0 -and ($out -join " ") -match "androiddebugkey") "alias androiddebugkey"
}

$settings = Join-Path $godota "editor_settings-4.7.tres"
if (Test-Path $settings) {
    $txt = Get-Content $settings -Raw
    Check "editor java_sdk_path"   ($txt -match 'java_sdk_path = "E:/install/jdk-17"') "E:/install/jdk-17"
    Check "editor android_sdk_path" ($txt -match 'android_sdk_path = "E:/install/android-sdk"') "E:/install/android-sdk"
}

Check "JAVA_HOME (User)" ([Environment]::GetEnvironmentVariable("JAVA_HOME","User") -eq $jdk) $jdk
Check "ANDROID_HOME (User)" ([Environment]::GetEnvironmentVariable("ANDROID_HOME","User") -eq $sdk) $sdk

""
"RESULT: $script:pass passed, $script:fail failed"
if ($script:fail -gt 0) { exit 1 } else { exit 0 }
