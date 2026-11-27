; guatnet 安装包脚本
; 编译：ISCC.exe installer\guatnet.iss

#define MyAppName "GuatNet"
#define MyAppVersion "1.0.3"
#define MyAppExeName "guatnet.exe"

[Setup]
AppId={{7C3E2B41-9A5D-4F2E-8B61-2D4A9C0E5F17}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher=guatnet
DefaultDirName={autopf}\guatnet
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
; 按用户安装，不弹 UAC，装到 %LOCALAPPDATA%\Programs
PrivilegesRequired=lowest
OutputDir=..\dist
OutputBaseFilename=GuatNet-v{#MyAppVersion}-win-x64-setup
SetupIconFile=..\assets\icon.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
MinVersion=10.0
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
Name: "chinese"; MessagesFile: "ChineseSimplified.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加任务："; Flags: checkedonce
Name: "autostart"; Description: "开机自动启动 GuatNet（在托盘后台保活）"; GroupDescription: "附加任务："

[Files]
Source: "..\guatnet.exe"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Registry]
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; \
  ValueName: "guatnet"; ValueData: """{app}\{#MyAppExeName}"""; \
  Flags: uninsdeletevalue; Tasks: autostart

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "立即运行 GuatNet"; \
  Flags: nowait postinstall skipifsilent

[Code]
{ 检查 WebView2 运行时是否安装（guatnet 的界面依赖它） }
function WebView2Installed(): Boolean;
var
  pv: string;
begin
  Result :=
    RegQueryStringValue(HKLM, 'SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', pv) or
    RegQueryStringValue(HKLM, 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', pv) or
    RegQueryStringValue(HKCU, 'Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}', 'pv', pv);
end;

function InitializeSetup(): Boolean;
begin
  Result := True;
  if not WebView2Installed() then
    MsgBox('没有检测到 Microsoft Edge WebView2 Runtime。' + #13#10#13#10 +
           'guatnet 的界面依赖它。Windows 11 和多数 Windows 10 已自带；' +
           '如果启动时提示缺少运行时，请到微软官网下载安装：' + #13#10 +
           'https://developer.microsoft.com/microsoft-edge/webview2/',
           mbInformation, MB_OK);
end;
