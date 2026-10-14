plugins { id("com.android.application"); id("org.jetbrains.kotlin.android") }
android {
    namespace = "network.meshway.android"
    compileSdk = 35
    defaultConfig {
        applicationId = "network.meshway.android"
        minSdk = 26
        targetSdk = 35
        versionCode = 2
        versionName = "0.1.0-alpha.2"
    }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
    kotlinOptions { jvmTarget = "17" }
    val signingStore = providers.environmentVariable("MESHWAY_ANDROID_KEYSTORE").orNull
    if (signingStore != null) {
        signingConfigs {
            create("ownerRelease") {
                storeFile = file(signingStore)
                storePassword = providers.environmentVariable("MESHWAY_ANDROID_STORE_PASSWORD").get()
                keyAlias = providers.environmentVariable("MESHWAY_ANDROID_KEY_ALIAS").get()
                keyPassword = providers.environmentVariable("MESHWAY_ANDROID_KEY_PASSWORD").get()
            }
        }
    }
    buildTypes {
        release {
            isMinifyEnabled = false
            if (signingStore != null) signingConfig = signingConfigs.getByName("ownerRelease")
        }
    }
    packaging { jniLibs { useLegacyPackaging = true } }
}
dependencies { implementation(files("libs/meshway.aar")) }
