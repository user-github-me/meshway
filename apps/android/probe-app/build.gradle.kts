plugins { id("com.android.application") }
android {
    namespace = "network.meshway.probe"
    compileSdk = 35
    defaultConfig { applicationId = "network.meshway.probe"; minSdk = 26; targetSdk = 35; versionCode = 1; versionName = "0.1" }
    compileOptions { sourceCompatibility = JavaVersion.VERSION_17; targetCompatibility = JavaVersion.VERSION_17 }
}
