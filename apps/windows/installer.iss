; Standard per-user Meshway installer. Installation never starts the daemon,
; enables a VPN, installs a service or changes networking. Wintun is loaded
; only after the user explicitly connects with administrator consent.
#ifndef PayloadDir
  #error PayloadDir must name the complete tested native app directory
#endif
#ifndef OutputDir
  #define OutputDir "."
#endif
#ifndef TargetArch
  #define TargetArch "x64"
#endif
#ifndef AppVersion
  #define AppVersion "0.1.0-alpha.1"
#endif

[Setup]
AppId=Meshway-{#TargetArch}
AppName=Meshway
AppVersion={#AppVersion}
AppPublisher=Meshway contributors
AppPublisherURL=https://github.com/user-github-me/meshway
AppSupportURL=https://github.com/user-github-me/meshway/issues
DefaultDirName={localappdata}\Programs\Meshway
DefaultGroupName=Meshway
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
UninstallDisplayIcon={app}\Meshway.exe
OutputDir={#OutputDir}
OutputBaseFilename=Meshway-setup-{#TargetArch}
Compression=lzma2/fast
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no
SetupLogging=yes
LicenseFile={#PayloadDir}\LICENSE
#if TargetArch == "arm64"
ArchitecturesAllowed=arm64
ArchitecturesInstallIn64BitMode=arm64
#else
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
#endif

[Files]
Source: "{#PayloadDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs; Excludes: "*.pdb,meshway-firewall-test.exe,*.test.exe"

[Tasks]
Name: "desktopicon"; Description: "Create a desktop shortcut"; GroupDescription: "Shortcuts:"; Flags: unchecked

[Icons]
Name: "{userprograms}\Meshway\Meshway"; Filename: "{app}\Meshway.exe"
Name: "{userprograms}\Meshway\Uninstall Meshway"; Filename: "{uninstallexe}"
Name: "{userdesktop}\Meshway"; Filename: "{app}\Meshway.exe"; Tasks: desktopicon

; No [Run] section: network startup always follows an explicit app action.
