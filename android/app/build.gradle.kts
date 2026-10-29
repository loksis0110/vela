import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

// Release signing: create android/keystore.properties (see README) or set
// VELA_KEYSTORE_* environment variables in CI.
val keystoreProps = Properties().apply {
    val f = rootProject.file("keystore.properties")
    if (f.exists()) f.inputStream().use { load(it) }
}
fun signingValue(key: String, env: String): String? =
    keystoreProps.getProperty(key) ?: System.getenv(env)

android {
    namespace = "app.vela.client"
    compileSdk = 37

    defaultConfig {
        applicationId = "app.vela.client"
        minSdk = 24
        targetSdk = 37
        versionCode = 13
        versionName = "1.4.5"
        base.archivesName.set("Vela-$versionName")
    }

    signingConfigs {
        val storePath = signingValue("storeFile", "VELA_KEYSTORE_FILE")
        create("release") {
            if (storePath != null) {
                storeFile = file(storePath)
                storePassword = signingValue("storePassword", "VELA_KEYSTORE_PASSWORD")
                keyAlias = signingValue("keyAlias", "VELA_KEY_ALIAS")
                keyPassword = signingValue("keyPassword", "VELA_KEY_PASSWORD")
            } else {
                // Test key kept in the repo so every build can be installed
                // over the previous one. Replace it before publishing.
                storeFile = rootProject.file("vela-test.jks")
                storePassword = "velatest"
                keyAlias = "vela"
                keyPassword = "velatest"
            }
        }
    }

    // full: APKs for direct download (current behavior).
    // play: Google Play build; ad blocking is off because Play's VpnService
    // policy doesn't allow apps to manipulate ads. Nothing else differs.
    flavorDimensions += "store"
    productFlavors {
        create("full") {
            dimension = "store"
            buildConfigField("boolean", "PLAY", "false")
        }
        create("play") {
            dimension = "store"
            buildConfigField("boolean", "PLAY", "true")
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            signingConfig = signingConfigs.getByName("release")
            vcsInfo.include = false
        }
    }

    // The Play Store must not receive a dependency metadata blob it can't read.
    dependenciesInfo {
        includeInApk = false
        includeInBundle = false
    }

    splits {
        abi {
            // per-ABI APKs for direct download; App Bundles split by ABI on their
            // own and can't be built with APK splits on (build them separately)
            isEnable = gradle.startParameter.taskNames.none { it.contains("bundle", ignoreCase = true) }
            isUniversalApk = true
            reset()
            include("arm64-v8a", "armeabi-v7a")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    packaging {
        jniLibs { useLegacyPackaging = true }
    }
}

kotlin {
    jvmToolchain(17)
}

dependencies {
    // sing-box core + shared Vela logic, built by `make android-lib`
    implementation(files("libs/libvela.aar"))

    implementation("androidx.core:core-ktx:1.19.0")
    implementation("androidx.activity:activity-compose:1.13.0")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.11.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.11.0")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.11.0")
    // QR scanner for subscription links (camera screen + decoder)
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")

    val composeBom = platform("androidx.compose:compose-bom:2026.06.01")
    implementation(composeBom)
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.foundation:foundation")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    debugImplementation("androidx.compose.ui:ui-tooling")
    implementation("androidx.compose.ui:ui-tooling-preview")
}
