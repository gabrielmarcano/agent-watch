pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
        maven { url = uri("https://jitpack.io") }
    }
}

rootProject.name = "agent-watch"
// The shared Kotlin data layer (:core), the Wear OS app (:app) and the Android phone app (:mobile).
include(":core")
include(":app")
include(":mobile")
