#!/usr/bin/env bash
set -euo pipefail
project_root=$(cd "$(dirname "$0")/../../.." && pwd)
android_sdk=${ANDROID_HOME:-"$project_root/.tools/native/android-sdk"}
java_root=${JAVA_HOME:-"$project_root/.tools/native/jdk/Contents/Home"}
out="$project_root/dist/android-probe"
mkdir -p "$out/classes"
"$java_root/bin/javac" -source 8 -target 8 -classpath "$android_sdk/platforms/android-35/android.jar" -d "$out/classes" "$project_root/apps/android/probe/src/NetworkProbe.java"
JAVA_HOME="$java_root" "$android_sdk/build-tools/35.0.0/d8" --min-api 26 --output "$out" "$out/classes/NetworkProbe.class"
(cd "$out" && "$java_root/bin/jar" cf network-probe.jar classes.dex)
echo "Built Android shell UID network probe. It changes no routes, DNS, VPNs, or proxies."
