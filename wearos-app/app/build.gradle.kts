plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("com.google.gms.google-services")
}

// Shared configuration: agent-watch.env at the repo root (see
// agent-watch.env.example). Optional: without it the defaults apply. Same
// rules as every other reader: KEY=value, '#' starts a comment, values are
// trimmed and literal, the last assignment wins; other lines are ignored.
val agentWatchEnv: Map<String, String> = run {
    val file = rootProject.layout.projectDirectory.file("../agent-watch.env")
    val text = providers.fileContents(file).asText.orNull ?: return@run emptyMap()
    text.lines().mapNotNull { raw ->
        val line = raw.substringBefore('#').trim()
        val eq = line.indexOf('=')
        if (eq <= 0) null else line.substring(0, eq).trim() to line.substring(eq + 1).trim()
    }.toMap()
}

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
    compileSdk = 34

    defaultConfig {
        applicationId = "com.gabriel.agentwatch"
        minSdk = 30
        targetSdk = 34
        versionCode = 1
        versionName = "1.0.0"
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
    kotlinOptions {
        jvmTarget = "17"
    }
    buildFeatures {
        compose = true
        buildConfig = true
    }
    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.8"
    }
    packaging {
        resources {
            excludes += "/META-INF/{AL2.0,LGPL2.1}"
        }
    }
    lint {
        abortOnError = false
        checkReleaseBuilds = false
    }
}

dependencies {
    // Wear OS Compose libraries
    implementation("androidx.wear.compose:compose-material:1.4.0")
    implementation("androidx.wear.compose:compose-foundation:1.4.0")

    // Markdown Renderer
    implementation("androidx.compose.material:material:1.6.1")
    implementation("com.mikepenz:multiplatform-markdown-renderer-m2:0.25.0")
    implementation("androidx.wear.compose:compose-navigation:1.4.0")

    // Core Android Compose
    implementation("androidx.activity:activity-compose:1.8.2")
    implementation("androidx.compose.ui:ui-tooling-preview:1.6.1")
    debugImplementation("androidx.compose.ui:ui-tooling:1.6.1")

    // Wear OS specific helpers
    implementation("androidx.wear:wear:1.3.0")
    implementation("com.google.android.gms:play-services-wearable:18.1.0")
    implementation("androidx.wear.watchface:watchface-complications-data-source-ktx:1.2.1")
    implementation("androidx.wear.tiles:tiles:1.4.0")
    implementation("androidx.wear.protolayout:protolayout:1.2.0")
    implementation("androidx.wear.protolayout:protolayout-material:1.2.0")
    implementation("androidx.concurrent:concurrent-futures:1.1.0")

    // HTTP / SSE client
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("com.squareup.okhttp3:okhttp-sse:4.12.0")

    // JSON parsing
    implementation("com.google.code.gson:gson:2.10.1")

    // Lifecycle
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.7.0")

    // Firebase Cloud Messaging
    implementation(platform("com.google.firebase:firebase-bom:32.7.2"))
    implementation("com.google.firebase:firebase-messaging-ktx")

    // Testing
    testImplementation("junit:junit:4.13.2")
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
}
