#!/usr/bin/env python3
"""Generate a dependency-free, reviewable Xcode project; no signing credentials."""
from pathlib import Path
import hashlib
import json

def uid(value): return hashlib.sha256(value.encode()).hexdigest()[:24].upper()
def quote(value): return json.dumps(str(value))
objects = {}
def add(key, body): objects[uid(key)] = body; return uid(key)
def ref(path, typ):
    key = 'file:'+path
    return add(key, '{isa = PBXFileReference; lastKnownFileType = '+typ+'; path = '+quote(path)+'; sourceTree = SOURCE_ROOT;}')
def phase(target, kind, files, extra=''):
    entries=[]
    for path, typ in files:
        file=ref(path,typ)
        entries.append(add('build:'+target+':'+kind+':'+path, '{isa = PBXBuildFile; fileRef = '+file+'; '+('settings = {ATTRIBUTES = (CodeSignOnCopy, RemoveHeadersOnCopy);};' if kind=='embed' else '')+'}'))
    return add('phase:'+target+':'+kind, '{isa = '+('PBXCopyFilesBuildPhase' if kind=='embed' else 'PBXSourcesBuildPhase' if kind=='sources' else 'PBXFrameworksBuildPhase')+'; buildActionMask = 2147483647; files = ('+','.join(entries)+'); runOnlyForDeploymentPostprocessing = 0; '+extra+'}')
core=[('../macos/Sources/MeshwayClientCore/'+p.name,'sourcecode.swift') for p in Path(__file__).parent.parent.joinpath('macos/Sources/MeshwayClientCore').glob('*.swift')]
framework=('../../dist/Meshway.xcframework','wrapper.xcframework')
products=[];targets=[]
for platform,sdk in [('macOS','macosx'),('iOS','iphoneos')]:
    app=platform+'App'; tunnel=platform+'Tunnel'
    for target,provider in [(tunnel,True),(app,False)]:
        product=add('product:'+target,'{isa = PBXFileReference; explicitFileType = '+('"wrapper.app-extension"' if provider else 'wrapper.application')+'; includeInIndex = 0; path = '+quote('Meshway'+('PacketTunnel' if provider else '')+'.'+('appex' if provider else 'app'))+'; sourceTree = BUILT_PRODUCTS_DIR;}')
        products.append(product)
        if provider:
            sources=[('PacketTunnel/PacketTunnelProvider.swift','sourcecode.swift'),('../macos/Sources/MeshwayClientCore/PacketTunnelPlan.swift','sourcecode.swift'),('../macos/Sources/MeshwayClientCore/Configuration.swift','sourcecode.swift'),('../macos/Sources/MeshwayClientCore/NativeStateKey.swift','sourcecode.swift')]
        elif platform=='macOS':sources=core+[('../macos/Sources/MeshwayMac/MeshwayApp.swift','sourcecode.swift')]
        else:sources=[('../ios/Sources/MeshwayIOSApp.swift','sourcecode.swift'),('../macos/Sources/MeshwayClientCore/AppleVPNController.swift','sourcecode.swift'),('../macos/Sources/MeshwayClientCore/NativeStateKey.swift','sourcecode.swift'),('../macos/Sources/MeshwayClientCore/Configuration.swift','sourcecode.swift')]
        phases=[phase(target,'sources',sources),phase(target,'frameworks',[framework] if provider or platform=='iOS' else [])]
        if not provider:
            # Use the extension's existing product file reference.
            embed=add('embed:'+target,'{isa = PBXBuildFile; fileRef = '+uid('product:'+tunnel)+'; settings = {ATTRIBUTES = (RemoveHeadersOnCopy,);};}')
            phases.append(add('embedphase:'+target,'{isa = PBXCopyFilesBuildPhase; buildActionMask = 2147483647; dstPath = ""; dstSubfolderSpec = 13; files = ('+embed+'); name = "Embed Packet Tunnel"; runOnlyForDeploymentPostprocessing = 0;}'))
            if platform=='macOS':
                phases.append(add('daemon:'+target,'{isa = PBXShellScriptBuildPhase; buildActionMask = 2147483647; files = (); inputPaths = (); outputPaths = (); runOnlyForDeploymentPostprocessing = 0; shellPath = /bin/bash; shellScript = '+quote('set -e\narch=amd64\nif [ "$NATIVE_ARCH_ACTUAL" = arm64 ]; then arch=arm64; fi\ncore="$SRCROOT/../../dist/bin/meshway-darwin-$arch"\nif [ ! -x "$core" ]; then echo "Build shared Go daemon first" >&2; exit 1; fi\nmkdir -p "$TARGET_BUILD_DIR/$UNLOCALIZED_RESOURCES_FOLDER_PATH"\ncp "$core" "$TARGET_BUILD_DIR/$UNLOCALIZED_RESOURCES_FOLDER_PATH/meshway"')+';}'))
        deps=[]
        if not provider:
            proxy=add('proxy:'+target,'{isa = PBXContainerItemProxy; containerPortal = '+uid('project')+'; proxyType = 1; remoteGlobalIDString = '+uid('target:'+tunnel)+'; remoteInfo = '+quote(tunnel)+';}')
            deps.append(add('dep:'+target,'{isa = PBXTargetDependency; target = '+uid('target:'+tunnel)+'; targetProxy = '+proxy+';}'))
        configs=[]
        for configuration in ('Debug','Release'):
            settings={'PRODUCT_NAME':'MeshwayPacketTunnel' if provider else 'Meshway','PRODUCT_MODULE_NAME':'MeshwayPacketTunnel' if provider else 'MeshwayClient','PRODUCT_BUNDLE_IDENTIFIER':('network.meshway.desktop' if platform=='macOS' else 'network.meshway.ios')+('.PacketTunnel' if provider else ''),'INFOPLIST_FILE':'Config/'+target+'.plist','CODE_SIGN_ENTITLEMENTS':'Config/'+target+'.entitlements','SWIFT_VERSION':'5.0','SDKROOT':sdk,'CODE_SIGN_STYLE':'Automatic','GENERATE_INFOPLIST_FILE':'NO','ENABLE_USER_SCRIPT_SANDBOXING':'NO','SWIFT_OPTIMIZATION_LEVEL':'-O' if configuration=='Release' else '-Onone','OTHER_LDFLAGS':'$(inherited) -lresolv','LD_RUNPATH_SEARCH_PATHS':'$(inherited) @executable_path/Frameworks @executable_path/../../Frameworks','SKIP_INSTALL':'YES' if provider else 'NO'}
            if provider:settings.update(APPLICATION_EXTENSION_API_ONLY='YES')
            if platform=='macOS':settings.update(MACOSX_DEPLOYMENT_TARGET='13.0',SUPPORTED_PLATFORMS='macosx',ENABLE_HARDENED_RUNTIME='YES')
            else:settings.update(IPHONEOS_DEPLOYMENT_TARGET='16.0',SUPPORTED_PLATFORMS='iphoneos iphonesimulator',TARGETED_DEVICE_FAMILY='1,2')
            configs.append(add('config:'+target+configuration,'{isa = XCBuildConfiguration; name = '+configuration+'; buildSettings = {'+' '.join(k+' = '+quote(v)+';' for k,v in settings.items())+'};}'))
        configlist=add('configs:'+target,'{isa = XCConfigurationList; buildConfigurations = ('+','.join(configs)+'); defaultConfigurationIsVisible = 0; defaultConfigurationName = Release;}')
        targets.append(add('target:'+target,'{isa = PBXNativeTarget; name = '+target+'; productName = '+quote('MeshwayPacketTunnel' if provider else 'Meshway')+'; productReference = '+product+'; productType = '+quote('com.apple.product-type.app-extension' if provider else 'com.apple.product-type.application')+'; buildConfigurationList = '+configlist+'; buildPhases = ('+','.join(phases)+'); buildRules = (); dependencies = ('+','.join(deps)+');}'))
rootgroup=add('group','{isa = PBXGroup; children = ('+','.join(uid(k) for k in [] )+'); sourceTree = "<group>";}')
productgroup=add('products','{isa = PBXGroup; children = ('+','.join(products)+'); name = Products; sourceTree = "<group>";}')
objects[rootgroup]='{isa = PBXGroup; children = ('+productgroup+','+','.join(key for key,value in objects.items() if 'lastKnownFileType' in value)+'); sourceTree = "<group>";}'
pc=[]
for configuration in ('Debug','Release'):
    pc.append(add('project-config:'+configuration,'{isa = XCBuildConfiguration; name = '+configuration+'; buildSettings = {CLANG_ENABLE_MODULES = YES;};}'))
cl=add('project-configlist','{isa = XCConfigurationList; buildConfigurations = ('+','.join(pc)+'); defaultConfigurationIsVisible = 0; defaultConfigurationName = Release;}')
project=add('project','{isa = PBXProject; attributes = {LastUpgradeCheck = 1600;}; buildConfigurationList = '+cl+'; compatibilityVersion = "Xcode 14.0"; developmentRegion = en; hasScannedForEncodings = 0; knownRegions = (en,Base); mainGroup = '+rootgroup+'; productRefGroup = '+productgroup+'; projectDirPath = ""; projectRoot = ""; targets = ('+','.join(targets)+');}')
out=Path(__file__).parent/'Meshway.xcodeproj/project.pbxproj'
out.parent.mkdir(parents=True,exist_ok=True)
out.write_text('// !$*UTF8*$!\n{archiveVersion = 1; classes = {}; objectVersion = 56; objects = {\n'+ '\n'.join(k+' = '+v+';' for k,v in objects.items())+'\n}; rootObject = '+project+';}\n')
schemes=out.parent/'xcshareddata/xcschemes';schemes.mkdir(parents=True,exist_ok=True)
for platform in ('macOS','iOS'):
    name=platform+'App'
    (schemes/(name+'.xcscheme')).write_text('<?xml version="1.0" encoding="UTF-8"?><Scheme LastUpgradeVersion="1600" version="1.3"><BuildAction parallelizeBuildables="YES" buildImplicitDependencies="YES"><BuildActionEntries><BuildActionEntry buildForTesting="YES" buildForRunning="YES" buildForProfiling="YES" buildForArchiving="YES" buildForAnalyzing="YES"><BuildableReference BuildableIdentifier="primary" BlueprintIdentifier="'+uid('target:'+name)+'" BuildableName="Meshway.app" BlueprintName="'+name+'" ReferencedContainer="container:Meshway.xcodeproj"/></BuildActionEntry></BuildActionEntries></BuildAction><LaunchAction buildConfiguration="Debug"/><ProfileAction buildConfiguration="Release"/><AnalyzeAction buildConfiguration="Debug"/><ArchiveAction buildConfiguration="Release" revealArchiveInOrganizer="YES"/></Scheme>')
