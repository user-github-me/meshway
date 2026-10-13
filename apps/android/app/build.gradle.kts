plugins { id("com.android.application"); id("org.jetbrains.kotlin.android") }
android {
    namespace = "network.meshway.android"
    compileSdk = 35
    defaultConfig {
        applicationId = "network.meshway.android"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    kotlinOptions { jvmTarget = "17" }
    buildTypes { release { isMinifyEnabled = false } }
    packaging { jniLibs { useLegacyPackaging = true } }
}
dependencies { implementation(files("libs/meshway.aar")) }
