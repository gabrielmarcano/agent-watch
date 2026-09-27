import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("com.google.gms.google-services")
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
// WEAROS_VERSION_NAME and WEAROS_VERSION_CODE; the code must always increase.
val versions: Map<String, String> = readRepoKeyValues("VERSIONS")
    ?: error("VERSIONS not found at the repo root: it is committed, restore it (git checkout VERSIONS)")
val wearVersionName: String = versions["WEAROS_VERSION_NAME"].orEmpty().also {
    require(it.isNotEmpty()) { "WEAROS_VERSION_NAME is not set in VERSIONS" }
}
val wearVersionCode: Int = versions["WEAROS_VERSION_CODE"]?.toIntOrNull()?.takeIf { it > 0 }
    ?: error("WEAROS_VERSION_CODE in VERSIONS must be a positive integer (got \"${versions["WEAROS_VERSION_CODE"].orEmpty()}\")")

// The pairing screen's default relay URL: https://<AW_RELAY_DOMAIN>, or ""
// (the user types it) when the file or the key is missing.
val defaultRelayUrl: String = agentWatchEnv["AW_RELAY_DOMAIN"].orEmpty().let { domain ->
    require(domain.isEmpty() || Regex("^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$").matches(domain)) {
        "AW_RELAY_DOMAIN in agent-watch.env must be a bare host name such as relay.example.com (no scheme, path or quotes)"
    }
    if (domain.isEmpty()) "" else "https://$domain"
}

android {
    namespace = "com.gabriel.agentwatch"
    compileSdk = 36

    defaultConfig {
        applicationId = "com.gabriel.agentwatch"
        minSdk = 30
        targetSdk = 34
        versionCode = wearVersionCode
        versionName = wearVersionName
        vectorDrawables {
            useSupportLibrary = true
        }
        buildConfigField("String", "DEFAULT_RELAY_URL", "\"$defaultRelayUrl\"")
    }

    buildTypes {
        release {
            isMinifyEnabled = true
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
        // Errors fail `lint`, and release builds run the fatal checks (lintVital).
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
    // Wear OS Compose Material 3. 1.6.x is the newest line that builds with AGP 8
    // (1.7 needs AGP 9.1 and compileSdk 37).
    implementation("androidx.wear.compose:compose-material3:1.6.2")
    implementation("androidx.wear.compose:compose-foundation:1.6.2")
    implementation("androidx.wear.compose:compose-navigation:1.6.2")
    // System text input (keyboard / handwriting / voice) for the relay URL and pairing code
    implementation("androidx.wear:wear-input:1.2.0")

    // Activity and lifecycle
    implementation("androidx.activity:activity-compose:1.12.4")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.10.0")

    // Complication, tile
    implementation("androidx.wear.watchface:watchface-complications-data-source-ktx:1.2.1")
    implementation("androidx.wear.tiles:tiles:1.6.2")
    implementation("androidx.wear.protolayout:protolayout:1.4.2")
    implementation("androidx.wear.protolayout:protolayout-material3:1.4.2")

    // HTTP / SSE client
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.squareup.okhttp3:okhttp-sse:4.12.0")

    // JSON parsing
    implementation("com.google.code.gson:gson:2.10.1")

    // Firebase Cloud Messaging
    implementation(platform("com.google.firebase:firebase-bom:32.7.2"))
    implementation("com.google.firebase:firebase-messaging-ktx")

    constraints {
        // watchface-complications-data-source 1.2.1 pulls Fragment 1.1.0 (preference → appcompat); the
        // Activity Result API (the notification permission request) needs 1.3.0 or newer.
        implementation("androidx.fragment:fragment:1.8.9")
    }

    // Testing
    testImplementation("junit:junit:4.13.2")
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
}
