import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.library")
    id("org.jetbrains.kotlin.android")
}

// The shared data layer: JSON contracts, the relay client and engine, the agent store and the
// approval presentational logic. The Wear OS app (:app) and the Android phone app (:mobile)
// both depend on it, so the Kotlin contracts keep one copy (AGENTS.md §1.2, contracts.md).
android {
    namespace = "com.gabriel.agentwatch.core"
    compileSdk = 36

    defaultConfig {
        minSdk = 30
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    lint {
        // Errors fail `lint`, like the app modules.
        abortOnError = true
        checkReleaseBuilds = true
    }
}

kotlin {
    compilerOptions {
        jvmTarget.set(JvmTarget.JVM_17)
    }
}

dependencies {
    // Exposed to the clients, not just used internally: both apps call the relay through these types.
    api("com.squareup.okhttp3:okhttp:4.12.0")
    api("com.squareup.okhttp3:okhttp-sse:4.12.0")
    api("com.google.code.gson:gson:2.10.1")
    api("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.9.0")
    api("androidx.annotation:annotation:1.9.1")

    // Testing
    testImplementation("junit:junit:4.13.2")
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
}
