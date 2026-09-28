plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.plugin.compose")
}

android {
    namespace = "ru.rassvet.chat"
    compileSdk = 37

    defaultConfig {
        applicationId = "ru.rassvet.chat"
        minSdk = 23
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
    }
    buildFeatures { compose = true }
}

dependencies {
    implementation(platform("androidx.compose:compose-bom:2026.09.00"))
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
    implementation("androidx.core:core:1.6.0")
    implementation("androidx.fragment:fragment:1.3.6")
    implementation("com.google.zxing:core:3.5.4")
}
