import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
}

// Firebase config is the owner's file (`mobile/google-services.json`, never committed). Without it
// the app still builds and runs; it just cannot fetch an FCM token or receive push.
if (file("google-services.json").exists()) {
    apply(plugin = "com.google.gms.google-services")
}

// Reads a KEY=value file at the repo root with the rules every reader shares
// (make's): '#' starts a comment, values are trimmed and literal, the last
// assignment wins; other lines are ignored. null when the file is missing.
fun readRepoKeyValues(name: String): Map<String, String>? {
    val file = rootProject.layout.projectDirectory.file("../$name")
    val text = providers.fileContents(file).asText.orNull ?: return null
    return text.lines().mapNotNull { raw ->
        val line = raw.substringBefore('#').trim()
        val eq = line.indexOf('=')
        if (eq <= 0) null else line.substring(0, eq).trim() to line.substring(eq + 1).trim()
    }.toMap()
}

// Shared configuration: agent-watch.env at the repo root (see
// agent-watch.env.example). Optional: without it the defaults apply.
val agentWatchEnv: Map<String, String> = readRepoKeyValues("agent-watch.env") ?: emptyMap()

// Component versions: VERSIONS at the repo root (committed). The app's are
// MOBILE_VERSION_NAME and MOBILE_VERSION_CODE; the code must always increase.
val versions: Map<String, String> = readRepoKeyValues("VERSIONS")
    ?: error("VERSIONS not found at the repo root: it is committed, restore it (git checkout VERSIONS)")
val mobileVersionName: String = versions["MOBILE_VERSION_NAME"].orEmpty().also {
    require(it.isNotEmpty()) { "MOBILE_VERSION_NAME is not set in VERSIONS" }
}
val mobileVersionCode: Int = versions["MOBILE_VERSION_CODE"]?.toIntOrNull()?.takeIf { it > 0 }
    ?: error("MOBILE_VERSION_CODE in VERSIONS must be a positive integer (got \"${versions["MOBILE_VERSION_CODE"].orEmpty()}\")")

// The pairing screen's default relay URL: https://<AW_RELAY_DOMAIN>, or ""
// (the user types it) when the file or the key is missing.
val defaultRelayUrl: String = agentWatchEnv["AW_RELAY_DOMAIN"].orEmpty().let { domain ->
    require(domain.isEmpty() || Regex("^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$").matches(domain)) {
        "AW_RELAY_DOMAIN in agent-watch.env must be a bare host name such as relay.example.com (no scheme, path or quotes)"
    }
    if (domain.isEmpty()) "" else "https://$domain"
}

// The Android phone client: a first-class relay client next to the watch (AGENTS.md §1.6),
// sharing the data layer with it through :core. It has its own application id and signing,
// so it never touches the watch app's pairing or push registration.
android {
    namespace = "com.gabriel.agentwatch.mobile"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.gabriel.agentwatch.mobile"
        // :core is minSdk 30 (Android 11).
        minSdk = 30
        targetSdk = 34
        versionCode = mobileVersionCode
        versionName = mobileVersionName
        buildConfigField("String", "DEFAULT_RELAY_URL", "\"$defaultRelayUrl\"")
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            // No release key yet: signed with the debug key, like the watch app (docs/STATUS.md).
            signingConfig = signingConfigs.getByName("debug")
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro"
            )
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
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
    lint {
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
    // The shared data layer: contracts, relay client/engine, store, approval logic.
    implementation(project(":core"))

    // Compose Material 3 for phones (the watch uses the Wear Compose Material 3 line).
    implementation(platform("androidx.compose:compose-bom:2025.09.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-graphics")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")

    implementation("androidx.activity:activity-compose:1.12.4")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.10.0")

    debugImplementation("androidx.compose.ui:ui-tooling")

    // Firebase Cloud Messaging: notifications are built locally from data-only pushes
    // (contracts.md §4.1). The BOM matches the watch app's.
    implementation(platform("com.google.firebase:firebase-bom:32.7.2"))
    implementation("com.google.firebase:firebase-messaging-ktx")

    constraints {
        // firebase-messaging pulls Fragment 1.0.0 (play-services-base); the Activity Result API (the
        // notification permission request) needs 1.3.0 or newer.
        implementation("androidx.fragment:fragment:1.8.9")
    }

    // Testing
    testImplementation("junit:junit:4.13.2")
}
