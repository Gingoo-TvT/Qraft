Unicode true
!include "MUI2.nsh"
!include "FileFunc.nsh"
!include "LogicLib.nsh"
!include "x64.nsh"
Name "Qraft ${VERSION}"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\Qraft"
InstallDirRegKey HKCU "Software\Qraft" "InstallDir"
RequestExecutionLevel user
ManifestDPIAware true
SetCompressor /SOLID lzma
Var TestInstall
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\Qraft.exe"
!define MUI_FINISHPAGE_RUN_TEXT "启动 Qraft"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=2052 "ProductName" "Qraft"
VIAddVersionKey /LANG=2052 "FileDescription" "Qraft Windows 安装程序"
VIAddVersionKey /LANG=2052 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=2052 "LegalCopyright" "Qraft contributors"

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "Qraft 需要 64 位 Windows。"
    Abort
  ${EndIf}
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "/TEST" $1
  ${IfNot} ${Errors}
    StrCpy $TestInstall "1"
  ${EndIf}
FunctionEnd

Section "Qraft"
  SetOutPath "$INSTDIR"
  File "${PAYLOAD}/Qraft.exe"
  File "${PAYLOAD}/使用说明.md"
  File "${PAYLOAD}/LICENSE"
  File "${PAYLOAD}/THIRD_PARTY_NOTICES.md"
  File "${PAYLOAD}/runtime-manifest.json"
  File "${PAYLOAD}/MicrosoftEdgeWebview2Setup.exe"
  File /r "${PAYLOAD}/licenses"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  ${If} $TestInstall == "1"
    FileOpen $0 "$INSTDIR\.test-install" w
    FileClose $0
  ${Else}
    CreateDirectory "$SMPROGRAMS\Qraft"
    CreateShortcut "$SMPROGRAMS\Qraft\Qraft.lnk" "$INSTDIR\Qraft.exe"
    WriteRegStr HKCU "Software\Qraft" "InstallDir" "$INSTDIR"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Qraft" "DisplayName" "Qraft"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Qraft" "DisplayVersion" "${VERSION}"
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Qraft" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
    WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Qraft" "DisplayIcon" "$INSTDIR\Qraft.exe"
    WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Qraft" "NoModify" 1
    WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Qraft" "NoRepair" 1
  ${EndIf}
  ; Install the Microsoft-signed runtime only when absent, as recommended by Microsoft.
  ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
    SetRegView 32
    ReadRegStr $0 HKLM "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
    SetRegView lastused
  ${EndIf}
  ${If} $0 == ""
    ExecWait '"$INSTDIR\MicrosoftEdgeWebview2Setup.exe" /silent /install' $1
    ${If} $1 != 0
      MessageBox MB_ICONINFORMATION "WebView2 Runtime 尚未完成安装。可运行安装目录中的 MicrosoftEdgeWebview2Setup.exe，或启动 Qraft 查看官方下载入口。"
    ${EndIf}
  ${EndIf}
SectionEnd

Section "Uninstall"
  ; Local client data and Docker volumes are deliberately preserved.
  IfFileExists "$INSTDIR\.test-install" skipregistry
    Delete "$SMPROGRAMS\Qraft\Qraft.lnk"
    RMDir "$SMPROGRAMS\Qraft"
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Qraft"
    DeleteRegKey HKCU "Software\Qraft"
  skipregistry:
  Delete "$INSTDIR\Qraft.exe"
  Delete "$INSTDIR\使用说明.md"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\THIRD_PARTY_NOTICES.md"
  Delete "$INSTDIR\runtime-manifest.json"
  Delete "$INSTDIR\MicrosoftEdgeWebview2Setup.exe"
  Delete "$INSTDIR\.test-install"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir /r "$INSTDIR\licenses"
  RMDir "$INSTDIR"
SectionEnd
